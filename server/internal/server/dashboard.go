package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

var landingHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>LaunchKit — Tell Claude, and your app is live</title>
<style>
* { box-sizing: border-box; margin: 0; padding: 0; }
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #0a0a0a; color: #e8e8e8; line-height: 1.6; }
.hero { max-width: 720px; margin: 0 auto; padding: 80px 24px 60px; text-align: center; }
h1 { font-size: 2.8rem; font-weight: 700; letter-spacing: -0.03em; margin-bottom: 16px; }
h1 span { color: #7c6af0; }
.sub { font-size: 1.2rem; color: #888; margin-bottom: 48px; }
.cta { display: inline-block; background: #7c6af0; color: #fff; padding: 14px 36px; border-radius: 8px; font-size: 1rem; font-weight: 600; text-decoration: none; }
.cta:hover { background: #6b5ce0; }
.steps { max-width: 720px; margin: 0 auto 80px; padding: 0 24px; display: grid; grid-template-columns: repeat(3, 1fr); gap: 24px; }
.step { background: #141414; border: 1px solid #222; border-radius: 12px; padding: 28px 24px; }
.step-num { font-size: 0.8rem; font-weight: 700; color: #7c6af0; text-transform: uppercase; letter-spacing: 0.1em; margin-bottom: 10px; }
.step h3 { font-size: 1rem; font-weight: 600; margin-bottom: 8px; }
.step p { font-size: 0.9rem; color: #888; }
.section { max-width: 720px; margin: 0 auto 80px; padding: 0 24px; }
.section h2 { font-size: 1.4rem; font-weight: 600; margin-bottom: 24px; }
.grid2 { display: grid; grid-template-columns: repeat(2, 1fr); gap: 16px; }
.card { background: #141414; border: 1px solid #222; border-radius: 10px; padding: 20px; }
.card .icon { font-size: 1.4rem; margin-bottom: 10px; }
.card h4 { font-size: 0.95rem; font-weight: 600; margin-bottom: 6px; }
.card p { font-size: 0.85rem; color: #888; }
.card.featured { border-color: #7c6af0; }
.card .price { font-size: 2rem; font-weight: 700; margin: 8px 0; }
.card .price span { font-size: 1rem; font-weight: 400; color: #888; }
.card ul { list-style: none; margin-top: 16px; }
.card ul li { font-size: 0.9rem; color: #aaa; padding: 4px 0; }
.card ul li::before { content: "✓  "; color: #7c6af0; }
footer { border-top: 1px solid #1a1a1a; text-align: center; padding: 32px; color: #555; font-size: 0.85rem; }
</style>
</head>
<body>
<div class="hero">
  <h1>Tell <span>Claude</span>,<br>and your app is live</h1>
  <p class="sub">No Docker to learn, no GCP to figure out, no terminal to touch.<br>Claude reads your code and, a few minutes later, hands you a real URL.</p>
  <a href="/dashboard" class="cta">Start free →</a>
</div>
<div class="steps">
  <div class="step"><div class="step-num">Step 1</div><h3>Sign in, get an API key</h3><p>Sign in with Google and get a key in 30 seconds.</p></div>
  <div class="step"><div class="step-num">Step 2</div><h3>Paste one line of config</h3><p>Add the key to your Claude Desktop config. One time, done.</p></div>
  <div class="step"><div class="step-num">Step 3</div><h3>Tell Claude to deploy</h3><p>Say "Deploy /myapp" and Claude reads your code and puts it live.</p></div>
</div>
<div class="section">
  <h2>The four things you care about most</h2>
  <div class="grid2">
    <div class="card"><div class="icon">🔒</div><h4>.env files and passwords are never stored</h4><p>LaunchKit reads your code only to package it. Secrets go through a separate encrypted form and never appear in build logs.</p></div>
    <div class="card"><div class="icon">☁️</div><h4>You choose whether to use your own cloud</h4><p>Deploy to the LaunchKit-managed cloud by default, or choose BYOC to deploy into your own GCP / AWS account with full control.</p></div>
    <div class="card"><div class="icon">↩️</div><h4>A bad deploy can be undone</h4><p>Every deploy is recorded. Tell Claude "roll back to the previous version" and it reverts immediately.</p></div>
    <div class="card"><div class="icon">💸</div><h4>No traffic, no charge</h4><p>Cloud Run scale-to-zero. A few friends using it usually costs $0 a month.</p></div>
  </div>
</div>
<div class="section">
  <h2>Pricing</h2>
  <div class="grid2">
    <div class="card">
      <div>Free for individuals</div>
      <div class="price">$0 <span>/ month</span></div>
      <ul><li>Up to 3 apps</li><li>Unlimited redeploys</li><li>Cloud Run scale-to-zero</li><li>Neon free DB (0.5GB)</li><li>Cloudflare Pages frontend</li></ul>
    </div>
    <div class="card featured">
      <div>Pro</div>
      <div class="price">$19 <span>/ month</span></div>
      <ul><li>Unlimited apps</li><li>Custom domains</li><li>BYOC (your own GCP/AWS)</li><li>Priority support</li><li>Coming soon</li></ul>
    </div>
  </div>
  <p style="margin-top:16px;font-size:0.85rem;color:#555">Cloud costs (if you use BYOC) are paid directly from your own account; LaunchKit never handles them. Small apps usually cost $0-$2 a month.</p>
</div>
<footer>LaunchKit &copy; 2025 &nbsp;·&nbsp; <a href="mailto:help@launchkit.dev" style="color:#555">help@launchkit.dev</a></footer>
</body>
</html>`

var dashboardTmpl = template.Must(template.New("dashboard").Funcs(template.FuncMap{
	"formatDate": func(t time.Time) string { return t.Format("2006-01-02") },
	"formatDatePtr": func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.Format("2006-01-02")
	},
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Dashboard — LaunchKit</title>
<style>
* { box-sizing: border-box; margin: 0; padding: 0; }
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #0a0a0a; color: #e8e8e8; }
nav { border-bottom: 1px solid #1a1a1a; padding: 0 24px; height: 56px; display: flex; align-items: center; justify-content: space-between; }
.logo { font-weight: 700; font-size: 1.1rem; }
.logo span { color: #7c6af0; }
.main { max-width: 760px; margin: 0 auto; padding: 48px 24px; }
h2 { font-size: 1.4rem; font-weight: 600; margin-bottom: 24px; }
.card { background: #141414; border: 1px solid #222; border-radius: 12px; padding: 28px; margin-bottom: 24px; }
.card h3 { font-size: 1rem; font-weight: 600; margin-bottom: 16px; }
input[type=text] { width: 100%; background: #1a1a1a; border: 1px solid #2a2a2a; color: #e8e8e8; border-radius: 6px; padding: 10px 14px; font-size: 0.95rem; outline: none; }
input[type=text]:focus { border-color: #7c6af0; }
.btn { display: inline-block; background: #7c6af0; color: #fff; padding: 10px 24px; border-radius: 6px; font-size: 0.9rem; font-weight: 600; border: none; cursor: pointer; }
.btn:hover { background: #6b5ce0; }
.btn-sm { padding: 6px 14px; font-size: 0.82rem; }
.btn-danger { background: #8b2020; }
.btn-danger:hover { background: #a02424; }
.key-row { display: flex; align-items: center; justify-content: space-between; padding: 14px 0; border-bottom: 1px solid #1e1e1e; gap: 12px; }
.key-row:last-child { border-bottom: none; }
.key-name { font-size: 0.95rem; font-weight: 500; }
.key-meta { font-size: 0.8rem; color: #666; margin-top: 3px; font-family: monospace; }
.code-block { background: #111; border: 1px solid #2a2a2a; border-radius: 8px; padding: 16px; font-family: monospace; font-size: 0.82rem; color: #aaa; white-space: pre; overflow-x: auto; position: relative; margin-top: 12px; }
.copy-btn { position: absolute; top: 10px; right: 10px; background: #2a2a2a; border: none; color: #888; padding: 4px 12px; border-radius: 4px; cursor: pointer; font-size: 0.8rem; }
.alert-success { background: #0a1a0a; border: 1px solid #1a4a1a; border-radius: 8px; padding: 16px; margin-bottom: 24px; }
.alert-key { font-family: monospace; font-size: 0.9rem; word-break: break-all; color: #4ade80; margin-top: 8px; padding: 10px; background: #111; border-radius: 6px; }
.empty { color: #555; text-align: center; padding: 32px; }
.login-center { text-align: center; padding: 80px 24px; }
.login-center h3 { font-size: 1.2rem; margin-bottom: 12px; }
.login-center p { color: #888; margin-bottom: 28px; }
</style>
</head>
<body>
<nav>
  <div class="logo">Launch<span>Kit</span></div>
  {{if .UserID}}<span style="color:#555;font-size:0.82rem">{{.UserID}}</span>{{end}}
</nav>
<div class="main">
{{if not .UserID}}
  <div class="login-center">
    <h3>Sign in to continue</h3>
    <p>Sign in free with Google and get your API key in 30 seconds</p>
    <a href="/auth/login" class="btn">Sign in with Google</a>
  </div>
{{else}}
  {{if .NewKey}}
  <div class="alert-success">
    <strong>✓ API key created! Save it now, it will not be shown again:</strong>
    <div class="alert-key" id="new-key-val">{{.NewKey}}</div>
    <button class="btn btn-sm" style="margin-top:10px" onclick="copyNewKey()">Copy</button>
  </div>
  {{end}}

  <h2>API Keys</h2>
  <div class="card">
    <h3>Create a new key</h3>
    <form method="POST" action="/dashboard/keys" style="display:flex;gap:12px">
      <input type="text" name="name" placeholder="e.g. Claude Desktop" required>
      <button type="submit" class="btn">Create</button>
    </form>
  </div>
  <div class="card">
    <h3>Your keys</h3>
    {{if .Keys}}
    {{range .Keys}}
    <div class="key-row">
      <div>
        <div class="key-name">{{.Name}}</div>
        <div class="key-meta">{{.Prefix}}...  ·  Created {{formatDate .CreatedAt}}{{if .LastUsedAt}}  ·  Last used {{formatDatePtr .LastUsedAt}}{{end}}</div>
      </div>
      <form method="POST" action="/dashboard/keys/{{.ID}}/revoke">
        <button type="submit" class="btn btn-sm btn-danger" onclick="return confirm('Revoke this key?')">Revoke</button>
      </form>
    </div>
    {{end}}
    {{else}}
    <div class="empty">No API keys yet</div>
    {{end}}
  </div>

  <h2>Connect Claude Desktop</h2>
  <div class="card">
    <h3>Add this to your Claude Desktop config</h3>
    <p style="font-size:0.82rem;color:#666">Path: <code>~/Library/Application Support/Claude/claude_desktop_config.json</code></p>
    <div class="code-block" id="cfg-block">{{.ConfigSnippet}}<button class="copy-btn" onclick="copyCfg()">Copy</button></div>
    <p style="margin-top:16px;font-size:0.85rem;color:#888">After adding it, restart Claude Desktop, then say:<br>
    <strong style="color:#e8e8e8">"Deploy /Users/yourname/myapp"</strong></p>
  </div>
{{end}}
</div>
<script>
function copyNewKey(){navigator.clipboard.writeText(document.getElementById('new-key-val').innerText)}
function copyCfg(){
  const t=document.getElementById('cfg-block').innerText.replace('Copy','').trim();
  navigator.clipboard.writeText(t).then(()=>{
    document.querySelector('.copy-btn').textContent='✓ Copied';
    setTimeout(()=>document.querySelector('.copy-btn').textContent='Copy',2000);
  });
}
</script>
</body>
</html>`))

type dashboardData struct {
	UserID        string
	NewKey        string
	Keys          []apiKeyRow
	ConfigSnippet string
}

type apiKeyRow struct {
	ID         string
	Name       string
	Prefix     string
	LastUsedAt *time.Time
	CreatedAt  time.Time
}

// webDashboardHandler serves the web dashboard.
type webDashboardHandler struct {
	db      *pgxpool.Pool
	baseURL string
}

func (h *webDashboardHandler) serveGet(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	data := dashboardData{UserID: userID}

	if userID != "" {
		rows, err := h.db.Query(r.Context(),
			`SELECT id, name, key_prefix, last_used_at, created_at
			 FROM api_keys WHERE user_id = $1 AND revoked_at IS NULL ORDER BY created_at DESC`,
			userID,
		)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var k apiKeyRow
				_ = rows.Scan(&k.ID, &k.Name, &k.Prefix, &k.LastUsedAt, &k.CreatedAt)
				data.Keys = append(data.Keys, k)
			}
		}

		keyHint := "lk_your_key_here"
		if len(data.Keys) > 0 {
			keyHint = data.Keys[0].Prefix + "..."
		}
		data.ConfigSnippet = buildConfigSnippet(h.baseURL, keyHint)

		// Flash: new key from redirect query param
		data.NewKey = r.URL.Query().Get("new_key")
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	dashboardTmpl.Execute(w, data) //nolint:errcheck
}

func (h *webDashboardHandler) serveCreateKey(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	name := r.FormValue("name")
	if name == "" {
		name = "My Key"
	}

	plaintext, err := generateAPIKey("dashboard")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	keyHash := auth.HashAPIKey(plaintext)
	prefix := plaintext[:11]

	_, err = h.db.Exec(r.Context(),
		`INSERT INTO api_keys (user_id, name, key_hash, key_prefix) VALUES ($1, $2, $3, $4)`,
		userID, name, keyHash, prefix,
	)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard?new_key="+plaintext, http.StatusSeeOther)
}

func (h *webDashboardHandler) serveRevokeKey(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	keyID := r.PathValue("id")
	h.db.Exec(r.Context(), //nolint:errcheck
		`UPDATE api_keys SET revoked_at = NOW() WHERE id = $1 AND user_id = $2`,
		keyID, userID,
	)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func serveLanding(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, landingHTML) //nolint:errcheck
}

func buildConfigSnippet(baseURL, keyHint string) string {
	data, _ := json.MarshalIndent(map[string]any{
		"mcpServers": map[string]any{
			"launchkit": map[string]any{
				"url": baseURL + "/mcp",
				"headers": map[string]string{
					"Authorization": fmt.Sprintf("Bearer %s", keyHint),
				},
			},
		},
	}, "", "  ")
	return string(data)
}
