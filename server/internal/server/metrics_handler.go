package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/alan890104/launchkit/server/internal/auth"
	"github.com/alan890104/launchkit/server/internal/deploy"
	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/jackc/pgx/v5/pgxpool"
)

type metricsHandler struct {
	db      *pgxpool.Pool
	compute provider.Compute // nil = Cloud Monitoring not available
	region  string
}

// healthResponse is the response shape for GET /api/projects/{id}/health.
type healthResponse struct {
	Status         string          `json:"status"`
	ErrorRate1h    *float64        `json:"error_rate_1h"`
	P95Ms          *float64        `json:"p95_ms"`
	RequestsPerMin *float64        `json:"requests_per_min"`
	ServiceCount   int             `json:"service_count"`
	LastDeploy     *deploymentSnap `json:"last_deploy,omitempty"`
}

type deploymentSnap struct {
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	Trigger    string     `json:"trigger"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// health handles GET /api/projects/{id}/health
func (h *metricsHandler) health(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	projectID := r.PathValue("id")
	if projectID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing project id"})
		return
	}

	// Verify project ownership.
	var exists bool
	err := h.db.QueryRow(r.Context(),
		`SELECT EXISTS(
			SELECT 1 FROM projects p
			JOIN teams t ON t.id = p.team_id
			JOIN team_members tm ON tm.team_id = t.id
			WHERE p.id = $1 AND tm.user_id = $2
		)`,
		projectID, userID,
	).Scan(&exists)
	if err != nil || !exists {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}

	// Fetch distinct service names for this project.
	svcRows, err := h.db.Query(r.Context(),
		`SELECT DISTINCT s.name
		 FROM services s
		 JOIN environments e ON s.environment_id = e.id
		 WHERE e.project_id = $1`,
		projectID,
	)
	if err != nil {
		slog.Error("health: query services", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer svcRows.Close()

	var serviceNames []string
	for svcRows.Next() {
		var name string
		if err := svcRows.Scan(&name); err != nil {
			continue
		}
		serviceNames = append(serviceNames, name)
	}

	// Aggregate metrics across services when compute is available.
	var (
		maxErrorRate  float64
		maxP95        float64
		totalRequests float64
		hasMetrics    bool
	)

	if h.compute != nil {
		for _, svcName := range serviceNames {
			cloudSvcName := deploy.ScopeServiceName(projectID, svcName)
			metricsResp, err := h.compute.GetMetrics(r.Context(), provider.MetricsQueryParams{
				ServiceName: cloudSvcName,
				Region:      h.region,
				Period:      "1h",
			})
			if err != nil {
				slog.Warn("health: get metrics", "service", cloudSvcName, "error", err)
				continue
			}
			for _, mr := range metricsResp {
				hasMetrics = true
				if m, ok := mr.Data["error_rate"]; ok && m.Current > maxErrorRate {
					maxErrorRate = m.Current
				}
				if m, ok := mr.Data["latency_p95"]; ok && m.Current > maxP95 {
					maxP95 = m.Current
				}
				if m, ok := mr.Data["requests"]; ok {
					totalRequests += m.Current
				}
			}
		}
	}

	// Fetch last deployment via services → environments → projects.
	var lastDeploy *deploymentSnap
	var snap deploymentSnap
	err = h.db.QueryRow(r.Context(),
		`SELECT d.id, d.status, d.trigger, d.started_at, d.finished_at
		 FROM deployments d
		 JOIN services s ON s.id = d.service_id
		 JOIN environments e ON e.id = s.environment_id
		 WHERE e.project_id = $1
		 ORDER BY d.started_at DESC
		 LIMIT 1`,
		projectID,
	).Scan(&snap.ID, &snap.Status, &snap.Trigger, &snap.StartedAt, &snap.FinishedAt)
	if err == nil {
		lastDeploy = &snap
	}

	// Determine health status.
	status := "healthy"
	if hasMetrics {
		switch {
		case maxErrorRate > 5:
			status = "down"
		case maxErrorRate >= 1:
			status = "degraded"
		default:
			status = "healthy"
		}
	} else if lastDeploy != nil {
		// Fallback: derive from last deploy status.
		switch lastDeploy.Status {
		case "failed", "error":
			status = "down"
		case "deploying", "provisioning", "building":
			status = "degraded"
		default:
			status = "healthy"
		}
	}

	resp := healthResponse{
		Status:       status,
		ServiceCount: len(serviceNames),
		LastDeploy:   lastDeploy,
	}
	if hasMetrics {
		resp.ErrorRate1h = &maxErrorRate
		resp.P95Ms = &maxP95
		resp.RequestsPerMin = &totalRequests
	}

	writeJSON(w, http.StatusOK, resp)
}

// metricsAPIResponse is the response shape for GET /api/projects/{id}/metrics.
type metricsAPIResponse struct {
	Period    string            `json:"period"`
	ErrorRate []provider.Metric `json:"error_rate"`
	P95Ms     []provider.Metric `json:"p95_ms"`
	Requests  []provider.Metric `json:"requests"`
}

// metrics handles GET /api/projects/{id}/metrics?period=1h|6h|24h
func (h *metricsHandler) metrics(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	projectID := r.PathValue("id")
	if projectID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing project id"})
		return
	}

	period := r.URL.Query().Get("period")
	if period == "" {
		period = "1h"
	}
	switch period {
	case "1h", "6h", "24h":
		// valid
	default:
		period = "1h"
	}

	// Verify project ownership.
	var existsM bool
	err := h.db.QueryRow(r.Context(),
		`SELECT EXISTS(
			SELECT 1 FROM projects p
			JOIN teams t ON t.id = p.team_id
			JOIN team_members tm ON tm.team_id = t.id
			WHERE p.id = $1 AND tm.user_id = $2
		)`,
		projectID, userID,
	).Scan(&existsM)
	if err != nil || !existsM {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}

	resp := metricsAPIResponse{
		Period:    period,
		ErrorRate: []provider.Metric{},
		P95Ms:     []provider.Metric{},
		Requests:  []provider.Metric{},
	}

	if h.compute == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Fetch service names.
	svcRows, err := h.db.Query(r.Context(),
		`SELECT DISTINCT s.name
		 FROM services s
		 JOIN environments e ON s.environment_id = e.id
		 WHERE e.project_id = $1`,
		projectID,
	)
	if err != nil {
		slog.Error("metrics: query services", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer svcRows.Close()

	var serviceNames []string
	for svcRows.Next() {
		var name string
		if err := svcRows.Scan(&name); err != nil {
			continue
		}
		serviceNames = append(serviceNames, name)
	}

	for _, svcName := range serviceNames {
		cloudSvcName := deploy.ScopeServiceName(projectID, svcName)
		metricsResp, err := h.compute.GetMetrics(r.Context(), provider.MetricsQueryParams{
			ServiceName: cloudSvcName,
			Region:      h.region,
			Period:      period,
		})
		if err != nil {
			slog.Warn("metrics: get metrics", "service", cloudSvcName, "error", err)
			continue
		}
		for _, mr := range metricsResp {
			if m, ok := mr.Data["error_rate"]; ok {
				resp.ErrorRate = append(resp.ErrorRate, m)
			}
			if m, ok := mr.Data["latency_p95"]; ok {
				resp.P95Ms = append(resp.P95Ms, m)
			}
			if m, ok := mr.Data["requests"]; ok {
				resp.Requests = append(resp.Requests, m)
			}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// recentErrorItem is a single error entry in the recent errors response.
type recentErrorItem struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	Message      string    `json:"message"`
	OccurredAt   time.Time `json:"occurred_at"`
	DeploymentID string    `json:"deployment_id"`
}

// recentErrorsResponse is the response shape for GET /api/projects/{id}/errors/recent.
type recentErrorsResponse struct {
	Errors []recentErrorItem `json:"errors"`
	Note   string            `json:"note"`
}

// recentErrors handles GET /api/projects/{id}/errors/recent
func (h *metricsHandler) recentErrors(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	projectID := r.PathValue("id")
	if projectID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing project id"})
		return
	}

	// Verify project ownership.
	var exists bool
	err := h.db.QueryRow(r.Context(),
		`SELECT EXISTS(
			SELECT 1
			FROM projects p
			JOIN teams t ON t.id = p.team_id
			JOIN team_members tm ON tm.team_id = t.id
			WHERE p.id = $1 AND tm.user_id = $2
		)`,
		projectID, userID,
	).Scan(&exists)
	if err != nil || !exists {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}

	// Query failed deployments via services → environments → projects.
	rows, err := h.db.Query(r.Context(),
		`SELECT d.id, d.status, d.error, d.started_at
		 FROM deployments d
		 JOIN services s ON s.id = d.service_id
		 JOIN environments e ON e.id = s.environment_id
		 WHERE e.project_id = $1
		   AND d.status IN ('failed', 'error')
		 ORDER BY d.started_at DESC
		 LIMIT 10`,
		projectID,
	)
	if err != nil {
		slog.Error("recentErrors: query", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	errors := []recentErrorItem{}
	for rows.Next() {
		var (
			id        string
			status    string
			errMsg    string
			startedAt time.Time
		)
		if err := rows.Scan(&id, &status, &errMsg, &startedAt); err != nil {
			continue
		}
		errors = append(errors, recentErrorItem{
			ID:           id,
			Type:         "deploy_failed",
			Message:      errMsg,
			OccurredAt:   startedAt,
			DeploymentID: id,
		})
	}

	writeJSON(w, http.StatusOK, recentErrorsResponse{
		Errors: errors,
		Note:   "Showing deployment failures. Runtime error integration via Cloud Logging coming soon.",
	})
}
