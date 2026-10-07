package server

import (
	"errors"
	"net/http"

	"github.com/alan890104/launchkit/server/internal/worker"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

func (h *apiProjectsHandler) redeploy(w http.ResponseWriter, r *http.Request) {
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

	var latest struct {
		ID        string
		PlanID    string
		ImageURI  string
		ServiceID string
	}
	if err := h.db.QueryRow(r.Context(), `
		SELECT d.id, d.plan_id, d.image_uri, d.service_id
		FROM deployments d
		JOIN services s ON s.id = d.service_id
		JOIN environments e ON e.id = s.environment_id
		WHERE e.project_id = $1 AND d.status IN ('live', 'failed')
		ORDER BY d.started_at DESC
		LIMIT 1
	`, projectID).Scan(&latest.ID, &latest.PlanID, &latest.ImageURI, &latest.ServiceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no completed deployment found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	deploymentID := uuid.NewString()
	if err := h.db.QueryRow(r.Context(), `
		INSERT INTO deployments (id, service_id, plan_id, status, trigger, image_uri)
		VALUES ($1, $2, $3, 'pending', 'dashboard', $4)
		RETURNING id
	`, deploymentID, latest.ServiceID, latest.PlanID, latest.ImageURI).Scan(&deploymentID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create deployment failed"})
		return
	}

	if h.riverClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "deploy workers not available"})
		return
	}

	if _, err := h.riverClient.Insert(r.Context(), &worker.DeployJobArgs{
		DeploymentID: deploymentID,
		PlanID:       latest.PlanID,
		IsRecovery:   false,
	}, &river.InsertOpts{
		UniqueOpts: river.UniqueOpts{ByArgs: true},
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "enqueue deployment failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"deployment_id": deploymentID})
}
