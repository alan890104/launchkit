package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

type apiKeyHandler struct {
	db *pgxpool.Pool
}

// generateAPIKey returns a cryptographically random key with the appropriate prefix.
// keyType "cli"  → prefix "lk_cli_" (format: lk_cli_<64 hex chars>)
// otherwise      → prefix "lk_"     (format: lk_<64 hex chars>)
// Both encode 32 random bytes → 256 bits of entropy.
func generateAPIKey(keyType string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	prefix := "lk_"
	if keyType == "cli" {
		prefix = "lk_cli_"
	}
	return prefix + hex.EncodeToString(b), nil
}

// POST /auth/keys
// Body: {"name": "Claude Desktop", "expires_in_days": 0, "scopes": "*", "key_type": "dashboard"}
//
//	expires_in_days: 0 = default (90 days), -1 = never, N > 0 = N days
//	scopes:          "" or omitted = "*" (full access)
//	key_type:        "" or omitted = "dashboard"; "cli" = CLI key with lk_cli_ prefix
//
// Returns: {"id", "key" (shown once), "name", "prefix", "scopes", "key_type", "expires_at", "created_at"}
func (h *apiKeyHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
		return
	}

	var body struct {
		Name          string `json:"name"`
		ExpiresInDays int    `json:"expires_in_days"` // 0 = default (90), -1 = never
		Scopes        string `json:"scopes"`          // "" = "*"
		KeyType       string `json:"key_type"`        // "" = "dashboard"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
		return
	}

	// Normalise key_type and scopes.
	keyType := body.KeyType
	if keyType != "cli" {
		keyType = "dashboard"
	}
	scopes := body.Scopes
	if scopes == "" {
		scopes = "*"
	}

	// Compute expiry: -1 = never (NULL), 0 = 90 days, N = N days.
	var expiresAt *time.Time
	switch {
	case body.ExpiresInDays == -1:
		expiresAt = nil // never expires
	case body.ExpiresInDays == 0:
		t := time.Now().UTC().AddDate(0, 0, 90)
		expiresAt = &t
	default:
		t := time.Now().UTC().AddDate(0, 0, body.ExpiresInDays)
		expiresAt = &t
	}

	plaintext, err := generateAPIKey(keyType)
	if err != nil {
		slog.Error("generate api key", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	hash := auth.HashAPIKey(plaintext)
	// key_prefix: "lk_" key → first 11 chars (lk_ + 8 hex); "lk_cli_" key → first 15 chars (lk_cli_ + 8 hex).
	prefixLen := 11
	if keyType == "cli" {
		prefixLen = 15
	}
	prefix := plaintext[:min(prefixLen, len(plaintext))]

	var id string
	var createdAt time.Time
	err = h.db.QueryRow(r.Context(), `
		INSERT INTO api_keys (user_id, name, key_hash, key_prefix, scopes, key_type, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at
	`, userID, body.Name, hash, prefix, scopes, keyType, expiresAt).Scan(&id, &createdAt)
	if err != nil {
		slog.Error("insert api key", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":         id,
		"key":        plaintext, // shown ONCE — never retrievable again
		"name":       body.Name,
		"prefix":     prefix,
		"scopes":     scopes,
		"key_type":   keyType,
		"expires_at": expiresAt,
		"created_at": createdAt,
		"note":       "Save this key now — it will not be shown again.",
	})
}

// GET /auth/keys
// Returns the list of API keys for the authenticated user (no plaintext).
func (h *apiKeyHandler) List(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
		return
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT id, name, key_prefix, scopes, key_type, last_used_at, expires_at, created_at
		FROM api_keys
		WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type keyItem struct {
		ID         string     `json:"id"`
		Name       string     `json:"name"`
		Prefix     string     `json:"prefix"`
		Scopes     string     `json:"scopes"`
		KeyType    string     `json:"key_type"`
		LastUsedAt *time.Time `json:"last_used_at,omitempty"`
		ExpiresAt  *time.Time `json:"expires_at,omitempty"`
		CreatedAt  time.Time  `json:"created_at"`
	}

	var keys []keyItem
	for rows.Next() {
		var k keyItem
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &k.Scopes, &k.KeyType, &k.LastUsedAt, &k.ExpiresAt, &k.CreatedAt); err != nil {
			continue
		}
		keys = append(keys, k)
	}
	if keys == nil {
		keys = []keyItem{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"keys": keys})
}

// DELETE /auth/keys/{id}
func (h *apiKeyHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
		return
	}

	keyID := r.PathValue("id")
	if keyID == "" {
		http.Error(w, `{"error":"key id required"}`, http.StatusBadRequest)
		return
	}

	result, err := h.db.Exec(r.Context(), `
		UPDATE api_keys SET revoked_at = NOW()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL
	`, keyID, userID)
	if err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	if result.RowsAffected() == 0 {
		http.Error(w, `{"error":"key not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "revoked"})
}

// POST /auth/keys/{id}/rotate
// Creates a new key with the same name/scopes/key_type and a fresh 90-day expiry,
// then atomically revokes the old key.  Returns the new key (shown once) in the
// same format as Create.
func (h *apiKeyHandler) Rotate(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
		return
	}

	keyID := r.PathValue("id")
	if keyID == "" {
		http.Error(w, `{"error":"key id required"}`, http.StatusBadRequest)
		return
	}

	// Look up the old key — must belong to the authenticated user and be active.
	var oldName, oldScopes, oldKeyType string
	err := h.db.QueryRow(r.Context(), `
		SELECT name, scopes, key_type
		FROM api_keys
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL
	`, keyID, userID).Scan(&oldName, &oldScopes, &oldKeyType)
	if err != nil {
		http.Error(w, `{"error":"key not found"}`, http.StatusNotFound)
		return
	}

	// Generate a new key with the same type.
	plaintext, err := generateAPIKey(oldKeyType)
	if err != nil {
		slog.Error("rotate: generate api key", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	newHash := auth.HashAPIKey(plaintext)
	prefixLen := 11
	if oldKeyType == "cli" {
		prefixLen = 15
	}
	newPrefix := plaintext[:min(prefixLen, len(plaintext))]

	newExpiresAt := time.Now().UTC().AddDate(0, 0, 90)

	// Atomically insert the new key and revoke the old one.
	ctx := r.Context()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		slog.Error("rotate: begin transaction", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var newID string
	var newCreatedAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO api_keys (user_id, name, key_hash, key_prefix, scopes, key_type, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at
	`, userID, oldName, newHash, newPrefix, oldScopes, oldKeyType, newExpiresAt).Scan(&newID, &newCreatedAt)
	if err != nil {
		slog.Error("rotate: insert new key", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	_, err = tx.Exec(ctx, `
		UPDATE api_keys SET revoked_at = NOW()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL
	`, keyID, userID)
	if err != nil {
		slog.Error("rotate: revoke old key", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		slog.Error("rotate: commit transaction", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":         newID,
		"key":        plaintext, // shown ONCE — never retrievable again
		"name":       oldName,
		"prefix":     newPrefix,
		"scopes":     oldScopes,
		"key_type":   oldKeyType,
		"expires_at": newExpiresAt,
		"created_at": newCreatedAt,
		"note":       "Save this key now — it will not be shown again.",
	})
}
