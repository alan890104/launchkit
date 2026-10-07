package server

import (
	"context"
	"net/http"
	"regexp"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func isUniqueViolation(err error) bool {
	pgErr, ok := err.(*pgconn.PgError)
	return ok && pgErr.Code == "23505"
}

var envVarNameRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func requireUserID(r *http.Request) (string, bool) {
	userID := auth.UserIDFromContext(r.Context())
	return userID, userID != ""
}

func isValidEnvVarName(s string) bool {
	return envVarNameRe.MatchString(s)
}

func projectTeamForUser(ctx context.Context, db *pgxpool.Pool, projectID, userID string) (string, error) {
	var teamID string
	err := db.QueryRow(ctx, `
		SELECT p.team_id
		FROM projects p
		JOIN teams t ON t.id = p.team_id
		JOIN team_members tm ON tm.team_id = t.id
		WHERE p.id = $1 AND tm.user_id = $2
	`, projectID, userID).Scan(&teamID)
	return teamID, err
}

func deploymentForUser(ctx context.Context, db *pgxpool.Pool, deploymentID, userID string) (string, error) {
	var projectID string
	err := db.QueryRow(ctx, `
		SELECT p.id
		FROM deployments d
		JOIN services s ON s.id = d.service_id
		JOIN environments e ON e.id = s.environment_id
		JOIN projects p ON p.id = e.project_id
		JOIN teams t ON t.id = p.team_id
		JOIN team_members tm ON tm.team_id = t.id
		WHERE d.id = $1 AND tm.user_id = $2
	`, deploymentID, userID).Scan(&projectID)
	return projectID, err
}
