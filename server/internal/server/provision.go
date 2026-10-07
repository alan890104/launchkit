package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

// provisionMiddlewareFor returns an HTTP middleware that auto-provisions
// user + personal team on first authenticated request.
// No-ops for unauthenticated requests (auth middleware handles those).
func provisionMiddlewareFor(db *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if userID := auth.UserIDFromContext(r.Context()); userID != "" {
				// Extract email and name before spawning the goroutine — r.Context()
				// is cancelled once ServeHTTP returns, so we capture the values now
				// and pass context.Background() for the actual DB work.
				email := auth.EmailFromContext(r.Context())
				name := auth.NameFromContext(r.Context())

				// Best-effort: provision in background, never block the request.
				// provisionUser is idempotent (ON CONFLICT DO NOTHING).
				go func() {
					if err := provisionUser(context.Background(), db, userID, email, name); err != nil {
						slog.Debug("provision user", "user_id", userID, "error", err)
					}
				}()
			}
			next.ServeHTTP(w, r)
		})
	}
}

// provisionUser upserts a user row and creates a default personal team if the user is new.
// Called after successful auth (JWT or API key) on first contact.
func provisionUser(ctx context.Context, db *pgxpool.Pool, userID, email, name string) error {
	// Upsert user
	_, err := db.Exec(ctx, `
		INSERT INTO users (id, email, name)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET
			email = EXCLUDED.email,
			name  = CASE WHEN EXCLUDED.name <> '' THEN EXCLUDED.name ELSE users.name END,
			updated_at = NOW()
	`, userID, email, name)
	if err != nil {
		return fmt.Errorf("upsert user: %w", err)
	}

	// Create personal team if it doesn't exist yet.
	// One personal team per user — named after the user.
	//
	// Security: We use INSERT ... ON CONFLICT (owner_id) DO NOTHING rather than
	// SELECT EXISTS + INSERT to prevent a TOCTOU race where two concurrent requests
	// both read teamExists=false and each insert a team row, granting the user two
	// $10 signup bonuses. The UNIQUE(owner_id) constraint on teams serializes this
	// at the DB level so at most one row is ever inserted per owner.
	teamName := name
	if teamName == "" {
		teamName = email
	}

	var newTeamID string
	err = db.QueryRow(ctx, `
		INSERT INTO teams (name, owner_id, balance)
		VALUES ($1, $2, 5.00)
		ON CONFLICT (owner_id) DO NOTHING
		RETURNING id
	`, teamName+"'s Team", userID).Scan(&newTeamID)

	if err != nil && err.Error() != "no rows in result set" {
		// Real DB error (not the "no rows" from ON CONFLICT DO NOTHING)
		return fmt.Errorf("create team: %w", err)
	}

	if newTeamID != "" {
		// This is a new team — add owner membership and starter subscription.
		// Add as owner member
		_, err = db.Exec(ctx, `
			INSERT INTO team_members (team_id, user_id, role)
			VALUES ($1, $2, 'owner')
			ON CONFLICT DO NOTHING
		`, newTeamID, userID)
		if err != nil {
			return fmt.Errorf("add team member: %w", err)
		}

		// Create starter subscription with $5 credit + $5 signup bonus = $10 usable first month.
		// monthly_credit=$5 means $5/mo going forward; credit_remaining=$10 for first month only.
		_, err = db.Exec(ctx, `
			INSERT INTO subscriptions (team_id, plan, monthly_credit, credit_remaining)
			VALUES ($1, 'starter', 5.00, 10.00)
			ON CONFLICT (team_id) DO NOTHING
		`, newTeamID)
		if err != nil {
			return fmt.Errorf("create subscription: %w", err)
		}

		slog.Info("provisioned new user", "user_id", userID, "email", email)
	}

	return nil
}
