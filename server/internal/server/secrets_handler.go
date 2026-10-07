package server

import (
	"crypto/subtle"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alan890104/launchkit/server/internal/deploy"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SecretsHandler serves the secret collection web form.
type SecretsHandler struct {
	db  *pgxpool.Pool
	enc *deploy.Encryptor // nil in dev mode — secrets stored as plaintext

	// Simple in-memory rate limiter: IP → last N request times.
	rateMu    sync.Mutex
	rateStore map[string][]time.Time
}

func newSecretsHandler(db *pgxpool.Pool, enc *deploy.Encryptor) *SecretsHandler {
	return &SecretsHandler{
		db:        db,
		enc:       enc,
		rateStore: make(map[string][]time.Time),
	}
}

// HandleGet renders the secret entry form.
func (h *SecretsHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("secretRequestID")
	if requestID == "" {
		http.NotFound(w, r)
		return
	}

	view, err := deploy.GetSecretRequestForForm(r.Context(), h.db, requestID)
	if err != nil {
		slog.Error("secret form: request not found", "id", requestID, "error", err)
		http.NotFound(w, r)
		return
	}

	if view.Expired {
		w.WriteHeader(http.StatusGone)
		expiredTmpl.Execute(w, nil)
		return
	}

	// Check if all keys already set
	allSet := true
	for _, k := range view.Keys {
		if !k.IsSet {
			allSet = false
			break
		}
	}
	if allSet {
		alreadySetTmpl.Execute(w, view)
		return
	}

	// Ensure CSRF token exists
	token, err := deploy.EnsureCSRFToken(r.Context(), h.db, requestID)
	if err != nil {
		slog.Error("secret form: csrf token", "id", requestID, "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	view.CSRFToken = token

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	formTmpl.Execute(w, view)
}

// HandlePost processes the secret form submission.
func (h *SecretsHandler) HandlePost(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("secretRequestID")
	if requestID == "" {
		http.NotFound(w, r)
		return
	}

	// Rate limit: max 10 POSTs per minute per IP
	ip := clientIP(r)
	if !h.allowRequest(ip, 10, time.Minute) {
		http.Error(w, "Too many requests. Please try again later.", http.StatusTooManyRequests)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	view, err := deploy.GetSecretRequestForForm(r.Context(), h.db, requestID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if view.Expired {
		w.WriteHeader(http.StatusGone)
		expiredTmpl.Execute(w, nil)
		return
	}

	// Validate CSRF token
	formToken := r.FormValue("_csrf")
	if view.CSRFToken == "" || subtle.ConstantTimeCompare([]byte(formToken), []byte(view.CSRFToken)) != 1 {
		http.Error(w, "Invalid form submission. Please reload the page and try again.", http.StatusForbidden)
		return
	}

	// Collect values — only for keys that aren't already set
	values := make(map[string]string)
	var missing []string
	for _, k := range view.Keys {
		if k.IsSet {
			continue
		}
		val := strings.TrimSpace(r.FormValue("secret_" + k.Name))
		if val == "" {
			missing = append(missing, k.Name)
		} else {
			values[k.Name] = val
		}
	}

	if len(missing) > 0 {
		// Re-render form with error
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		formErrorTmpl.Execute(w, struct {
			*deploy.SecretRequestView
			Missing []string
		}{view, missing})
		return
	}

	// Store values
	if err := deploy.SetSecretValues(r.Context(), h.db, view.ProjectID, values, h.enc); err != nil {
		slog.Error("secret form: store values", "id", requestID, "error", err)
		http.Error(w, "Failed to save secrets. Please try again.", http.StatusInternalServerError)
		return
	}

	// Mark request completed if all keys are now set
	allSet := true
	for _, k := range view.Keys {
		if !k.IsSet && values[k.Name] == "" {
			allSet = false
			break
		}
	}
	if allSet {
		deploy.MarkSecretRequestCompleted(r.Context(), h.db, requestID)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	successTmpl.Execute(w, view)
}

// allowRequest returns true if the IP hasn't exceeded maxRequests in the given window.
func (h *SecretsHandler) allowRequest(ip string, maxRequests int, window time.Duration) bool {
	h.rateMu.Lock()
	defer h.rateMu.Unlock()

	now := time.Now()
	cutoff := now.Add(-window)

	// Prune old entries
	times := h.rateStore[ip]
	valid := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= maxRequests {
		h.rateStore[ip] = valid
		return false
	}

	h.rateStore[ip] = append(valid, now)
	return true
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if parts := strings.SplitN(xff, ",", 2); len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	// Strip port from RemoteAddr
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}

// ── HTML Templates ──────────────────────────────────────────────────────────

var formTmpl = template.Must(template.New("form").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>LaunchKit &middot; Secure Secret Entry</title>
<style>
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #f5f5f5; color: #1a1a1a; line-height: 1.6; }
  .container { max-width: 520px; margin: 60px auto; padding: 0 20px; }
  .card { background: #fff; border-radius: 12px; padding: 32px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
  h1 { font-size: 20px; margin-bottom: 4px; }
  .project { color: #666; font-size: 14px; margin-bottom: 24px; }
  .field { margin-bottom: 20px; }
  .field label { display: block; font-weight: 600; font-size: 14px; margin-bottom: 4px; font-family: monospace; }
  .field .hint { font-size: 13px; color: #888; margin-bottom: 6px; }
  .field input { width: 100%; padding: 10px 12px; border: 1px solid #ddd; border-radius: 8px; font-size: 14px; font-family: monospace; }
  .field input:focus { outline: none; border-color: #0066ff; box-shadow: 0 0 0 3px rgba(0,102,255,0.1); }
  button { width: 100%; padding: 12px; background: #0066ff; color: #fff; border: none; border-radius: 8px; font-size: 15px; font-weight: 600; cursor: pointer; margin-top: 8px; }
  button:hover { background: #0052cc; }
  .security { margin-top: 16px; font-size: 12px; color: #999; text-align: center; }
</style>
</head>
<body>
<div class="container">
<div class="card">
  <h1>Secure Secret Entry</h1>
  <p class="project">Project: {{.ProjectName}}</p>
  <form method="POST">
    <input type="hidden" name="_csrf" value="{{.CSRFToken}}">
    {{range .Keys}}{{if not .IsSet}}
    <div class="field">
      <label>{{.Name}}</label>
      {{if .Hint}}<p class="hint">{{.Hint}}</p>{{end}}
      <input type="password" name="secret_{{.Name}}" autocomplete="off" required>
    </div>
    {{end}}{{end}}
    <button type="submit">Submit &amp; Encrypt</button>
  </form>
  <p class="security">Secrets are stored encrypted and never pass through AI.</p>
</div>
</div>
</body>
</html>`))

var formErrorTmpl = template.Must(template.New("formError").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>LaunchKit &middot; Secure Secret Entry</title>
<style>
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #f5f5f5; color: #1a1a1a; line-height: 1.6; }
  .container { max-width: 520px; margin: 60px auto; padding: 0 20px; }
  .card { background: #fff; border-radius: 12px; padding: 32px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
  h1 { font-size: 20px; margin-bottom: 4px; }
  .project { color: #666; font-size: 14px; margin-bottom: 24px; }
  .error { background: #fff0f0; border: 1px solid #ffcdd2; border-radius: 8px; padding: 12px; margin-bottom: 20px; color: #c62828; font-size: 14px; }
  .field { margin-bottom: 20px; }
  .field label { display: block; font-weight: 600; font-size: 14px; margin-bottom: 4px; font-family: monospace; }
  .field .hint { font-size: 13px; color: #888; margin-bottom: 6px; }
  .field input { width: 100%; padding: 10px 12px; border: 1px solid #ddd; border-radius: 8px; font-size: 14px; font-family: monospace; }
  .field input:focus { outline: none; border-color: #0066ff; box-shadow: 0 0 0 3px rgba(0,102,255,0.1); }
  button { width: 100%; padding: 12px; background: #0066ff; color: #fff; border: none; border-radius: 8px; font-size: 15px; font-weight: 600; cursor: pointer; margin-top: 8px; }
  button:hover { background: #0052cc; }
  .security { margin-top: 16px; font-size: 12px; color: #999; text-align: center; }
</style>
</head>
<body>
<div class="container">
<div class="card">
  <h1>Secure Secret Entry</h1>
  <p class="project">Project: {{.ProjectName}}</p>
  <div class="error">Please fill in all required secrets: {{range .Missing}}<code>{{.}}</code> {{end}}</div>
  <form method="POST">
    <input type="hidden" name="_csrf" value="{{.CSRFToken}}">
    {{range .Keys}}{{if not .IsSet}}
    <div class="field">
      <label>{{.Name}}</label>
      {{if .Hint}}<p class="hint">{{.Hint}}</p>{{end}}
      <input type="password" name="secret_{{.Name}}" autocomplete="off" required>
    </div>
    {{end}}{{end}}
    <button type="submit">Submit &amp; Encrypt</button>
  </form>
  <p class="security">Secrets are stored encrypted and never pass through AI.</p>
</div>
</div>
</body>
</html>`))

var successTmpl = template.Must(template.New("success").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>LaunchKit &middot; Secrets Saved</title>
<style>
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #f5f5f5; color: #1a1a1a; line-height: 1.6; }
  .container { max-width: 520px; margin: 60px auto; padding: 0 20px; }
  .card { background: #fff; border-radius: 12px; padding: 32px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); text-align: center; }
  h1 { font-size: 20px; margin-bottom: 8px; color: #2e7d32; }
  p { font-size: 15px; color: #666; }
</style>
</head>
<body>
<div class="container">
<div class="card">
  <h1>Secrets Saved</h1>
  <p>Your secrets for <strong>{{.ProjectName}}</strong> have been stored securely.</p>
  <p style="margin-top: 12px;">The deployment will continue automatically. You can close this page.</p>
</div>
</div>
</body>
</html>`))

var expiredTmpl = template.Must(template.New("expired").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>LaunchKit &middot; Link Expired</title>
<style>
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #f5f5f5; color: #1a1a1a; line-height: 1.6; }
  .container { max-width: 520px; margin: 60px auto; padding: 0 20px; }
  .card { background: #fff; border-radius: 12px; padding: 32px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); text-align: center; }
  h1 { font-size: 20px; margin-bottom: 8px; color: #c62828; }
  p { font-size: 15px; color: #666; }
</style>
</head>
<body>
<div class="container">
<div class="card">
  <h1>Link Expired</h1>
  <p>This secret entry link has expired.</p>
  <p style="margin-top: 12px;">Please redeploy your project to get a new link.</p>
</div>
</div>
</body>
</html>`))

var alreadySetTmpl = template.Must(template.New("alreadySet").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>LaunchKit &middot; Secrets Already Submitted</title>
<style>
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #f5f5f5; color: #1a1a1a; line-height: 1.6; }
  .container { max-width: 520px; margin: 60px auto; padding: 0 20px; }
  .card { background: #fff; border-radius: 12px; padding: 32px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); text-align: center; }
  h1 { font-size: 20px; margin-bottom: 8px; color: #2e7d32; }
  p { font-size: 15px; color: #666; }
</style>
</head>
<body>
<div class="container">
<div class="card">
  <h1>Secrets Already Submitted</h1>
  <p>All secrets for <strong>{{.ProjectName}}</strong> have already been provided.</p>
  <p style="margin-top: 12px;">Your deployment is continuing. You can close this page.</p>
</div>
</div>
</body>
</html>`))
