package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alan890104/launchkit/server/internal/deploy"
	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/alan890104/launchkit/server/internal/worker"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// githubWebhookHandler handles POST /webhooks/github for push-to-deploy.
// Authentication is per-connection HMAC-SHA256, not the global auth middleware.
type githubWebhookHandler struct {
	db          *pgxpool.Pool
	buildStore  provider.BuildStorage // nil in local mode
	planEngine  *deploy.Engine
	riverClient *river.Client[pgx.Tx]
	githubToken string // PAT for downloading private repos (optional)
	baseURL     string
	isLocal     bool
}

func (h *githubWebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Only handle push events.
	event := r.Header.Get("X-GitHub-Event")
	if event == "ping" {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "pong"})
		return
	}
	if event != "push" {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ignored", "event": event})
		return
	}

	// Read body (needed for both HMAC verification and JSON parsing).
	body, err := io.ReadAll(io.LimitReader(r.Body, 25<<20)) // 25 MB max
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}

	// Parse the push payload.
	var payload struct {
		Ref        string `json:"ref"`
		After      string `json:"after"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		HeadCommit struct {
			Message string `json:"message"`
		} `json:"head_commit"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid JSON payload", http.StatusBadRequest)
		return
	}

	repoFullName := payload.Repository.FullName
	commitSHA := payload.After
	// Extract branch from ref (refs/heads/main → main)
	branch := strings.TrimPrefix(payload.Ref, "refs/heads/")

	if repoFullName == "" {
		http.Error(w, "missing repository.full_name", http.StatusBadRequest)
		return
	}

	// Ignore tag pushes (refs/tags/...) and delete pushes (all zeros SHA).
	if !strings.HasPrefix(payload.Ref, "refs/heads/") {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ignored", "reason": "not a branch push"})
		return
	}
	if commitSHA == "0000000000000000000000000000000000000000" {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ignored", "reason": "branch deleted"})
		return
	}

	// Look up the github_connection for this repo + branch.
	var connID, projectID, webhookSecret, lastPushSHA string
	err = h.db.QueryRow(ctx, `
		SELECT gc.id, gc.project_id, gc.webhook_secret, COALESCE(gc.last_push_sha, '')
		FROM github_connections gc
		WHERE gc.repo_full_name = $1 AND gc.branch = $2 AND gc.status = 'active'
	`, repoFullName, branch).Scan(&connID, &projectID, &webhookSecret, &lastPushSHA)
	if err != nil {
		http.Error(w, "no active connection for this repo/branch", http.StatusNotFound)
		return
	}

	// Verify HMAC-SHA256 signature.
	sigHeader := r.Header.Get("X-Hub-Signature-256")
	if !verifyGitHubSignature(body, webhookSecret, sigHeader) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	// Dedup: skip if we already processed this exact commit.
	if lastPushSHA == commitSHA {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "skipped", "reason": "duplicate commit SHA"})
		return
	}

	// Find the most recent plan for this project (same pattern as redeploy).
	var planID string
	var planData []byte
	err = h.db.QueryRow(ctx, `
		SELECT id, plan_data FROM plans
		WHERE project_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, projectID).Scan(&planID, &planData)
	if err != nil {
		slog.Warn("github webhook: no plan found for project", "repo", repoFullName, "project_id", projectID)
		http.Error(w, "no deployment plan found — run plan_deployment first via Claude", http.StatusBadRequest)
		return
	}

	// Download source tarball from GitHub.
	tarballURL := fmt.Sprintf("https://api.github.com/repos/%s/tarball/%s", repoFullName, commitSHA)
	sourceReader, err := h.downloadGitHubTarball(ctx, tarballURL)
	if err != nil {
		slog.Error("github webhook: download tarball failed", "url", tarballURL, "error", err)
		http.Error(w, fmt.Sprintf("download source from GitHub failed: %v", err), http.StatusBadGateway)
		return
	}
	defer sourceReader.Close()

	// Upload source to build storage (cloud mode) or handle local mode.
	if !h.isLocal {
		if h.buildStore == nil {
			http.Error(w, "build storage not configured", http.StatusInternalServerError)
			return
		}
		sourceKey := fmt.Sprintf("sources/%s/source.archive", planID)
		if err := h.buildStore.Upload(ctx, sourceKey, sourceReader); err != nil {
			slog.Error("github webhook: upload source failed", "key", sourceKey, "error", err)
			http.Error(w, fmt.Sprintf("upload source to storage failed: %v", err), http.StatusInternalServerError)
			return
		}
	}
	// Note: local mode — the plan's source_path already points to the local directory.
	// For local GitHub webhooks, the user's working directory should already be up to date
	// after git push (or they're pushing from the same machine). The plan references the
	// local source path which is updated by git.

	// Mark the plan as confirmed.
	if err := h.planEngine.UpdateStatus(ctx, planID, deploy.PlanConfirmed); err != nil {
		slog.Error("github webhook: confirm plan failed", "plan_id", planID, "error", err)
	}

	// Create deployment record.
	deploymentID := uuid.NewString()
	_, err = h.db.Exec(ctx,
		"INSERT INTO deployments (id, status, plan_id, trigger) VALUES ($1, 'pending', $2, 'github_push')",
		deploymentID, planID,
	)
	if err != nil {
		slog.Error("github webhook: create deployment record failed", "error", err)
		http.Error(w, "create deployment record failed", http.StatusInternalServerError)
		return
	}

	// Enqueue the deploy job.
	if h.riverClient == nil {
		http.Error(w, "deploy queue not initialized", http.StatusInternalServerError)
		return
	}
	_, err = h.riverClient.Insert(ctx, &worker.DeployJobArgs{
		DeploymentID: deploymentID,
		PlanID:       planID,
		IsRecovery:   false,
	}, &river.InsertOpts{
		UniqueOpts: river.UniqueOpts{ByArgs: true},
	})
	if err != nil {
		slog.Error("github webhook: enqueue deploy failed", "error", err)
		http.Error(w, "enqueue deployment failed", http.StatusInternalServerError)
		return
	}

	// Update last_push_sha for dedup.
	_, _ = h.db.Exec(ctx,
		"UPDATE github_connections SET last_push_sha = $1, updated_at = NOW() WHERE id = $2",
		commitSHA, connID,
	)

	slog.Info("github webhook: deployment enqueued",
		"repo", repoFullName,
		"branch", branch,
		"commit", commitSHA[:8],
		"plan_id", planID,
		"deployment_id", deploymentID,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"status":        "queued",
		"deployment_id": deploymentID,
		"plan_id":       planID,
		"commit":        commitSHA,
	})
}

// verifyGitHubSignature checks the X-Hub-Signature-256 HMAC.
func verifyGitHubSignature(body []byte, secret, sigHeader string) bool {
	if sigHeader == "" || secret == "" {
		return false
	}
	// sigHeader format: "sha256=<hex>"
	sig, ok := strings.CutPrefix(sigHeader, "sha256=")
	if !ok {
		return false
	}
	decoded, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)

	return hmac.Equal(decoded, expected)
}

// downloadGitHubTarball fetches a source tarball from the GitHub API.
func (h *githubWebhookHandler) downloadGitHubTarball(ctx context.Context, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if h.githubToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.githubToken)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub API %d: %s", resp.StatusCode, string(respBody))
	}

	return resp.Body, nil
}
