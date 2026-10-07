package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/alan890104/launchkit/server/internal/deploy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type apiSecretsHandler struct {
	db  *pgxpool.Pool
	enc *deploy.Encryptor
}

type secretListItem struct {
	Name  string `json:"name"`
	IsSet bool   `json:"is_set"`
}

func (h *apiSecretsHandler) list(w http.ResponseWriter, r *http.Request) {
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
		`SELECT name, status FROM secrets WHERE project_id = $1 ORDER BY name ASC`,
		projectID,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	secrets := []secretListItem{}
	for rows.Next() {
		var item secretListItem
		var status string
		if err := rows.Scan(&item.Name, &status); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		item.IsSet = status == "set"
		secrets = append(secrets, item)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"secrets": secrets})
}

func (h *apiSecretsHandler) set(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	projectID := r.PathValue("id")
	key := r.PathValue("key")
	if projectID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing project id"})
		return
	}
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing secret key"})
		return
	}
	if !isValidEnvVarName(key) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key must match [A-Z_][A-Z0-9_]*"})
		return
	}

	var req struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
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

	var valueEnc, nonce []byte
	if h.enc != nil {
		var err error
		valueEnc, nonce, err = h.enc.Encrypt(req.Value)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "encrypt failed"})
			return
		}
	} else {
		valueEnc = []byte(req.Value)
	}

	_, err := h.db.Exec(r.Context(), `
		INSERT INTO secrets (project_id, name, value_enc, nonce, status)
		VALUES ($1, $2, $3, $4, 'set')
		ON CONFLICT (project_id, name) DO UPDATE SET
			value_enc = EXCLUDED.value_enc,
			nonce = EXCLUDED.nonce,
			status = 'set',
			updated_at = NOW()
	`, projectID, key, valueEnc, nonce)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *apiSecretsHandler) delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	projectID := r.PathValue("id")
	key := r.PathValue("key")
	if projectID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing project id"})
		return
	}
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing secret key"})
		return
	}
	if !isValidEnvVarName(key) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key must match [A-Z_][A-Z0-9_]*"})
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

	if _, err := h.db.Exec(r.Context(), `DELETE FROM secrets WHERE project_id = $1 AND name = $2`, projectID, key); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
