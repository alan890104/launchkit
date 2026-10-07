package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type apiGitHubHandler struct {
	db *pgxpool.Pool
}

type githubConnection struct {
	ID           string    `json:"id"`
	RepoFullName string    `json:"repo_full_name"`
	Branch       string    `json:"branch"`
	Status       string    `json:"status"`
	LastPushSHA  string    `json:"last_push_sha"`
	CreatedAt    time.Time `json:"created_at"`
}

func (h *apiGitHubHandler) list(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	projectID := r.PathValue("id")
	if projectID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing project id"})
		return
	}

	if _, err := projectTeamForUser(r.Context(), h.db, projectID, userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	rows, err := h.db.Query(r.Context(),
		`SELECT id, repo_full_name, branch, status, last_push_sha, created_at FROM github_connections WHERE project_id = $1 ORDER BY created_at DESC`,
		projectID,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	connections := []githubConnection{}
	for rows.Next() {
		var c githubConnection
		if err := rows.Scan(&c.ID, &c.RepoFullName, &c.Branch, &c.Status, &c.LastPushSHA, &c.CreatedAt); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		connections = append(connections, c)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"connections": connections})
}

func (h *apiGitHubHandler) disconnect(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	projectID := r.PathValue("id")
	connectionID := r.PathValue("connectionId")
	if projectID == "" || connectionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing project id or connection id"})
		return
	}

	if _, err := projectTeamForUser(r.Context(), h.db, projectID, userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	// Verify connection belongs to project
	var exists string
	err := h.db.QueryRow(r.Context(),
		`SELECT id FROM github_connections WHERE id = $1 AND project_id = $2`,
		connectionID, projectID,
	).Scan(&exists)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "connection not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	if _, err := h.db.Exec(r.Context(), `DELETE FROM github_connections WHERE id = $1`, connectionID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
