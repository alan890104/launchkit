package auth

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuthScope contains the pre-resolved authorization context for an
// authenticated user. It is populated once per request by the global
// auth middleware and propagated via context so that handlers never
// need to query team_members themselves.
type AuthScope struct {
	UserID  string   // authenticated user ID
	TeamIDs []string // all teams the user belongs to
}

type scopeCtxKey struct{}

// ScopeFromContext extracts the AuthScope injected by the auth middleware.
func ScopeFromContext(ctx context.Context) (AuthScope, bool) {
	scope, ok := ctx.Value(scopeCtxKey{}).(AuthScope)
	return scope, ok
}

// ContextWithScope returns a new context carrying the given AuthScope.
func ContextWithScope(ctx context.Context, scope AuthScope) context.Context {
	return context.WithValue(ctx, scopeCtxKey{}, scope)
}

// ResolveAuthScope queries team_members once to build the full AuthScope
// for a given userID. This is called by the global middleware so that
// individual handlers never need to JOIN team_members.
func ResolveAuthScope(ctx context.Context, db *pgxpool.Pool, userID string) (AuthScope, error) {
	rows, err := db.Query(ctx,
		"SELECT team_id FROM team_members WHERE user_id = $1", userID)
	if err != nil {
		return AuthScope{}, fmt.Errorf("resolve auth scope: %w", err)
	}
	defer rows.Close()

	var teamIDs []string
	for rows.Next() {
		var tid string
		if err := rows.Scan(&tid); err != nil {
			return AuthScope{}, fmt.Errorf("scan team_id: %w", err)
		}
		teamIDs = append(teamIDs, tid)
	}
	if err := rows.Err(); err != nil {
		return AuthScope{}, fmt.Errorf("iterate team_members: %w", err)
	}

	return AuthScope{
		UserID:  userID,
		TeamIDs: teamIDs,
	}, nil
}
