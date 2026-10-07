package deploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SecretKey describes a user_required secret for the web form.
type SecretKey struct {
	Name string `json:"name"`
	Hint string `json:"hint"`
}

// SecretRequestView is the data needed to render the secret form.
type SecretRequestView struct {
	ID          string
	ProjectID   string
	ProjectName string
	Keys        []SecretKeyStatus
	Expired     bool
	CSRFToken   string
}

// SecretKeyStatus tracks whether a single key has been filled.
type SecretKeyStatus struct {
	Name  string
	Hint  string
	IsSet bool
}

// collectUserRequiredVars scans all services in a plan and returns
// deduplicated user_required env var keys with their descriptions.
func collectUserRequiredVars(plan *Plan) []SecretKey {
	seen := make(map[string]bool)
	var keys []SecretKey
	for _, svc := range plan.Services {
		for _, ev := range svc.EnvVars {
			if ev.Classification == EnvUserRequired && !seen[ev.Key] {
				seen[ev.Key] = true
				keys = append(keys, SecretKey{
					Name: ev.Key,
					Hint: ev.Description,
				})
			}
		}
	}
	return keys
}

// createSecretRequest inserts a secret_requests row and upserts secrets rows.
// Returns the request ID. Existing secrets with status='set' are preserved.
func createSecretRequest(ctx context.Context, db *pgxpool.Pool, projectID, deploymentID string, keys []SecretKey) (string, error) {
	keysJSON, err := json.Marshal(keys)
	if err != nil {
		return "", fmt.Errorf("marshal secret keys: %w", err)
	}

	expiresAt := time.Now().Add(1 * time.Hour)

	var requestID string
	err = db.QueryRow(ctx,
		`INSERT INTO secret_requests (project_id, deployment_id, keys, expires_at)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id`,
		projectID, deploymentID, keysJSON, expiresAt,
	).Scan(&requestID)
	if err != nil {
		return "", fmt.Errorf("insert secret_request: %w", err)
	}

	// Upsert secrets rows — DO NOTHING preserves secrets from prior deploys.
	for _, k := range keys {
		_, err = db.Exec(ctx,
			`INSERT INTO secrets (project_id, name)
			 VALUES ($1, $2)
			 ON CONFLICT (project_id, name) DO NOTHING`,
			projectID, k.Name,
		)
		if err != nil {
			return "", fmt.Errorf("upsert user_secret %s: %w", k.Name, err)
		}
	}

	return requestID, nil
}

// allSecretsSet returns true if every named secret has status='set' for the project.
func allSecretsSet(ctx context.Context, db *pgxpool.Pool, projectID string, keyNames []string) bool {
	var count int
	err := db.QueryRow(ctx,
		`SELECT COUNT(*) FROM secrets
		 WHERE project_id = $1 AND name = ANY($2) AND status = 'set'`,
		projectID, keyNames,
	).Scan(&count)
	return err == nil && count >= len(keyNames)
}

// waitForSecrets polls secrets until all keys are set or timeout/context cancellation.
func waitForSecrets(ctx context.Context, db *pgxpool.Pool, projectID string, keyNames []string, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		if allSecretsSet(ctx, db, projectID, keyNames) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("timeout waiting for secrets after %s", timeout)
		case <-ticker.C:
			// next poll
		}
	}
}

// ResolveUserSecrets reads all set secrets for a project. Returns {name: plaintext_value}.
// Pass a non-nil enc to decrypt AES-256-GCM ciphertext; nil enc falls back to plaintext (dev mode).
func ResolveUserSecrets(ctx context.Context, db *pgxpool.Pool, projectID string, enc *Encryptor) (map[string]string, error) {
	rows, err := db.Query(ctx,
		`SELECT name, value_enc, nonce FROM secrets
		 WHERE project_id = $1 AND status = 'set'`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("query secrets: %w", err)
	}
	defer rows.Close()

	secrets := make(map[string]string)
	for rows.Next() {
		var name string
		var value, nonce []byte
		if err := rows.Scan(&name, &value, &nonce); err != nil {
			return nil, fmt.Errorf("scan user_secret: %w", err)
		}
		if enc != nil && len(nonce) > 0 {
			plaintext, err := enc.Decrypt(value, nonce)
			if err != nil {
				return nil, fmt.Errorf("decrypt secret %s: %w", name, err)
			}
			secrets[name] = plaintext
		} else {
			secrets[name] = string(value) // plaintext fallback (dev mode or unencrypted legacy)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate secrets: %w", err)
	}
	return secrets, nil
}

// getSecretRequestForForm loads a secret request with project info and per-key fill status.
func GetSecretRequestForForm(ctx context.Context, db *pgxpool.Pool, requestID string) (*SecretRequestView, error) {
	var (
		projectID   string
		projectName string
		keysJSON    []byte
		expiresAt   time.Time
		csrfToken   string
		status      string
	)

	err := db.QueryRow(ctx,
		`SELECT sr.project_id, p.name, sr.keys, sr.expires_at, sr.csrf_token, sr.status
		 FROM secret_requests sr
		 JOIN projects p ON sr.project_id = p.id
		 WHERE sr.id = $1`,
		requestID,
	).Scan(&projectID, &projectName, &keysJSON, &expiresAt, &csrfToken, &status)
	if err != nil {
		return nil, fmt.Errorf("get secret_request: %w", err)
	}

	var keys []SecretKey
	if err := json.Unmarshal(keysJSON, &keys); err != nil {
		return nil, fmt.Errorf("unmarshal keys: %w", err)
	}

	view := &SecretRequestView{
		ID:          requestID,
		ProjectID:   projectID,
		ProjectName: projectName,
		Expired:     status == "completed" || time.Now().After(expiresAt),
		CSRFToken:   csrfToken,
	}

	// Check each key's status in secrets
	for _, k := range keys {
		var secretStatus string
		err := db.QueryRow(ctx,
			`SELECT status FROM secrets WHERE project_id = $1 AND name = $2`,
			projectID, k.Name,
		).Scan(&secretStatus)

		ks := SecretKeyStatus{
			Name:  k.Name,
			Hint:  k.Hint,
			IsSet: err == nil && secretStatus == "set",
		}
		view.Keys = append(view.Keys, ks)
	}

	return view, nil
}

// SetSecretValues stores values for the given keys with optional AES-256-GCM encryption.
// Pass a non-nil enc to encrypt; nil enc stores plaintext (dev mode).
// Uses UPSERT so it works for both new secrets and updates.
func SetSecretValues(ctx context.Context, db *pgxpool.Pool, projectID string, values map[string]string, enc *Encryptor) error {
	for name, value := range values {
		var ciphertext, nonce []byte
		if enc != nil {
			var err error
			ciphertext, nonce, err = enc.Encrypt(value)
			if err != nil {
				return fmt.Errorf("encrypt secret %s: %w", name, err)
			}
		} else {
			ciphertext = []byte(value) // dev mode: store plaintext
		}
		_, err := db.Exec(ctx,
			`INSERT INTO secrets (project_id, name, value_enc, nonce, status)
			 VALUES ($1, $2, $3, $4, 'set')
			 ON CONFLICT (project_id, name) DO UPDATE SET
			     value_enc = EXCLUDED.value_enc,
			     nonce = EXCLUDED.nonce,
			     status = 'set',
			     updated_at = NOW()`,
			projectID, name, ciphertext, nonce,
		)
		if err != nil {
			return fmt.Errorf("set secret %s: %w", name, err)
		}
	}
	return nil
}

// markSecretRequestCompleted sets a secret request's status to 'completed'.
func MarkSecretRequestCompleted(ctx context.Context, db *pgxpool.Pool, requestID string) error {
	_, err := db.Exec(ctx,
		`UPDATE secret_requests SET status = 'completed' WHERE id = $1`,
		requestID,
	)
	return err
}

// ensureCSRFToken generates and stores a CSRF token if one doesn't exist yet.
// Returns the token. Uses UPDATE ... WHERE csrf_token=” to avoid overwriting.
func EnsureCSRFToken(ctx context.Context, db *pgxpool.Pool, requestID string) (string, error) {
	// Check if already set
	var existing string
	err := db.QueryRow(ctx,
		`SELECT csrf_token FROM secret_requests WHERE id = $1`,
		requestID,
	).Scan(&existing)
	if err != nil {
		return "", err
	}
	if existing != "" {
		return existing, nil
	}

	// Generate and store
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate csrf token: %w", err)
	}
	token := hex.EncodeToString(b)

	_, err = db.Exec(ctx,
		`UPDATE secret_requests SET csrf_token = $1 WHERE id = $2 AND csrf_token = ''`,
		token, requestID,
	)
	if err != nil {
		return "", fmt.Errorf("store csrf token: %w", err)
	}

	return token, nil
}
