package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

// TokenCache is an optional distributed cache for API key validation.
// Implemented by provider.APICache; defined here as an interface to avoid
// an import cycle between the auth and provider packages.
type TokenCache interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
}

// Context key for the authenticated user ID.
type ctxKey string

const (
	UserIDKey ctxKey = "user_id"
	UserEmail ctxKey = "user_email"
	UserName  ctxKey = "user_name"
)

// FirebaseKeys manages auto-refreshing Google public keys used to verify Firebase ID tokens.
type FirebaseKeys struct {
	mu     sync.RWMutex
	set    jwk.Set
	loaded bool
}

const firebaseKeysURL = "https://www.googleapis.com/robot/v1/metadata/jwk/securetoken@system.gserviceaccount.com"

// NewFirebaseKeys fetches Google's public keys for Firebase token verification.
func NewFirebaseKeys(ctx context.Context) (*FirebaseKeys, error) {
	set, err := jwk.Fetch(ctx, firebaseKeysURL)
	if err != nil {
		return nil, fmt.Errorf("fetch Firebase public keys: %w", err)
	}

	fk := &FirebaseKeys{set: set, loaded: true}

	// Background refresh every 15 minutes (Google rotates keys ~daily)
	go func() {
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refreshed, err := jwk.Fetch(ctx, firebaseKeysURL)
				if err != nil {
					slog.Error("Firebase keys refresh failed", "error", err)
					continue
				}
				fk.mu.Lock()
				fk.set = refreshed
				fk.mu.Unlock()
			}
		}
	}()

	slog.Info("Firebase public keys initialized")
	return fk, nil
}

func (fk *FirebaseKeys) KeySet() jwk.Set {
	fk.mu.RLock()
	defer fk.mu.RUnlock()
	return fk.set
}

// AuthMiddleware verifies either:
//   - Firebase ID token (for web dashboard)
//   - LaunchKit API key with prefix "lk_" (for Claude Desktop / Claude Code MCP)
//   - LAUNCHKIT_DEV_KEY env var (dev shortcut)
type AuthMiddleware struct {
	FirebaseKeys      *FirebaseKeys // nil if Firebase not configured
	DB                *pgxpool.Pool // for API key lookup
	FirebaseProjectID string        // GCP project ID for Firebase (issuer check)
	SkipPrefixes      []string      // paths that skip auth (e.g. /healthz, /readyz, /s/)
	IsDev             bool
	DevKey            string     // LAUNCHKIT_DEV_KEY — bypass auth in dev (exact match)
	Cache             TokenCache // optional distributed cache for API key validation (nil = DB only)
}

func (m *AuthMiddleware) ShouldSkip(path string) bool {
	for _, prefix := range m.SkipPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func (m *AuthMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.ShouldSkip(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			// Dev mode: accept X-Dev-User-ID header
			if m.IsDev {
				devUserID := r.Header.Get("X-Dev-User-ID")
				if devUserID != "" {
					ctx := context.WithValue(r.Context(), UserIDKey, devUserID)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				slog.Warn("dev mode: no auth header, proceeding without auth", "path", r.URL.Path)
				next.ServeHTTP(w, r)
				return
			}

			http.Error(w, `{"error":"missing bearer token"}`, http.StatusUnauthorized)
			return
		}

		tokenStr := strings.TrimPrefix(header, "Bearer ")

		// Dev key shortcut — exact match, skips DB entirely.
		if m.DevKey != "" && tokenStr == m.DevKey {
			ctx := context.WithValue(r.Context(), UserIDKey, "dev-user")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// LaunchKit API key — prefix "lk_"
		if strings.HasPrefix(tokenStr, "lk_") {
			userID, err := m.lookupAPIKey(r.Context(), tokenStr)
			if err != nil {
				slog.Warn("api key auth failed", "error", err, "path", r.URL.Path)
				http.Error(w, `{"error":"invalid or revoked api key"}`, http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), UserIDKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Firebase ID token
		if m.FirebaseKeys == nil {
			http.Error(w, `{"error":"firebase auth not configured"}`, http.StatusUnauthorized)
			return
		}

		issuer := fmt.Sprintf("https://securetoken.google.com/%s", m.FirebaseProjectID)
		token, err := jwt.Parse(
			[]byte(tokenStr),
			jwt.WithKeySet(m.FirebaseKeys.KeySet()),
			jwt.WithAudience(m.FirebaseProjectID),
			jwt.WithIssuer(issuer),
			jwt.WithValidate(true),
		)
		if err != nil {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}

		subject := token.Subject()
		if subject == "" {
			http.Error(w, `{"error":"missing subject in token"}`, http.StatusUnauthorized)
			return
		}

		// Extract email and name from Firebase token claims
		ctx := context.WithValue(r.Context(), UserIDKey, subject)
		if email, ok := token.PrivateClaims()["email"].(string); ok {
			ctx = context.WithValue(ctx, UserEmail, email)
		}
		if name, ok := token.PrivateClaims()["name"].(string); ok {
			ctx = context.WithValue(ctx, UserName, name)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// lookupAPIKey resolves a plaintext API key to a user ID.
// Checks the distributed cache first (if configured); falls through to DB on miss.
// Updates last_used_at on a DB hit.
//
// Note: revoked keys may remain valid in the cache for up to the 5-minute TTL.
// This is an acceptable trade-off for Phase 1 — full invalidation requires
// passing the cache reference into the revocation handler (future work).
func (m *AuthMiddleware) lookupAPIKey(ctx context.Context, plaintext string) (string, error) {
	if m.DB == nil {
		return "", fmt.Errorf("no database configured for api key lookup")
	}

	hash := HashAPIKey(plaintext)
	cacheKey := "apikey:" + hash

	// Cache read — skip DB if we have a cached user ID.
	if m.Cache != nil {
		if cachedUserID, ok, _ := m.Cache.Get(ctx, cacheKey); ok && cachedUserID != "" {
			return cachedUserID, nil
		}
	}

	// DB lookup (authoritative).
	var userID string
	err := m.DB.QueryRow(ctx, `
		UPDATE api_keys
		SET last_used_at = NOW()
		WHERE key_hash = $1
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > NOW())
		RETURNING user_id
	`, hash).Scan(&userID)
	if err != nil {
		return "", fmt.Errorf("api key not found or revoked")
	}

	// Populate cache for subsequent requests.
	if m.Cache != nil {
		_ = m.Cache.Set(ctx, cacheKey, userID, 5*time.Minute)
	}

	return userID, nil
}

// HashAPIKey returns the SHA-256 hex digest of a plaintext API key.
func HashAPIKey(plaintext string) string {
	h := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(h[:])
}

// UserIDFromContext extracts the authenticated user ID from the request context.
func UserIDFromContext(ctx context.Context) string {
	if userID, ok := ctx.Value(UserIDKey).(string); ok {
		return userID
	}
	return ""
}

// EmailFromContext extracts the user email from context (Firebase tokens only).
func EmailFromContext(ctx context.Context) string {
	if email, ok := ctx.Value(UserEmail).(string); ok {
		return email
	}
	return ""
}

// NameFromContext extracts the user name from context (Firebase tokens only).
func NameFromContext(ctx context.Context) string {
	if name, ok := ctx.Value(UserName).(string); ok {
		return name
	}
	return ""
}

// ── Firebase session exchange endpoint ───────────────────────────

// ExchangeHandler handles POST /auth/session — exchanges a Firebase ID token for a LaunchKit API key.
// This is used by the web dashboard after Firebase client-side login.
func ExchangeHandler(db *pgxpool.Pool, firebaseKeys *FirebaseKeys, firebaseProject string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			IDToken string `json:"id_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IDToken == "" {
			http.Error(w, `{"error":"missing id_token"}`, http.StatusBadRequest)
			return
		}

		if firebaseKeys == nil {
			http.Error(w, `{"error":"firebase not configured"}`, http.StatusServiceUnavailable)
			return
		}

		issuer := fmt.Sprintf("https://securetoken.google.com/%s", firebaseProject)
		token, err := jwt.Parse(
			[]byte(req.IDToken),
			jwt.WithKeySet(firebaseKeys.KeySet()),
			jwt.WithAudience(firebaseProject),
			jwt.WithIssuer(issuer),
			jwt.WithValidate(true),
		)
		if err != nil {
			http.Error(w, `{"error":"invalid firebase token"}`, http.StatusUnauthorized)
			return
		}

		uid := token.Subject()
		email, _ := token.PrivateClaims()["email"].(string)
		name, _ := token.PrivateClaims()["name"].(string)
		if uid == "" {
			http.Error(w, `{"error":"missing uid in token"}`, http.StatusUnauthorized)
			return
		}

		// Upsert user
		_, err = db.Exec(r.Context(), `
			INSERT INTO users (id, email, name) VALUES ($1, $2, $3)
			ON CONFLICT (id) DO UPDATE SET email = $2, name = COALESCE(NULLIF($3, ''), users.name), updated_at = NOW()
		`, uid, email, name)
		if err != nil {
			slog.Error("upsert user on session exchange", "error", err)
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]string{
			"user_id": uid,
			"email":   email,
			"name":    name,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// ── Token verification helper for non-HTTP contexts ─────────────

// VerifyFirebaseToken is a standalone helper to verify a Firebase ID token.
// Returns (uid, email, error).
func VerifyFirebaseToken(ctx context.Context, tokenStr string, keys *FirebaseKeys, projectID string) (string, string, error) {
	if keys == nil {
		return "", "", fmt.Errorf("firebase keys not initialized")
	}

	issuer := fmt.Sprintf("https://securetoken.google.com/%s", projectID)
	token, err := jwt.Parse(
		[]byte(tokenStr),
		jwt.WithKeySet(keys.KeySet()),
		jwt.WithAudience(projectID),
		jwt.WithIssuer(issuer),
		jwt.WithValidate(true),
	)
	if err != nil {
		return "", "", fmt.Errorf("invalid firebase token: %w", err)
	}

	uid := token.Subject()
	email, _ := token.PrivateClaims()["email"].(string)

	return uid, email, nil
}
