package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/alan890104/launchkit/server/internal/auth"
	projectsvc "github.com/alan890104/launchkit/server/internal/service/project"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

type apiProjectsHandler struct {
	db          *pgxpool.Pool
	riverClient *river.Client[pgx.Tx]
	projectSvc  *projectsvc.Service
}

// ProjectRow is returned by GET /api/projects
type ProjectRow struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Region     string          `json:"region"`
	UpdatedAt  time.Time       `json:"updated_at"`
	LastDeploy *DeploymentMeta `json:"last_deploy,omitempty"`
}

// DeploymentMeta is a lightweight deployment summary embedded in project rows.
type DeploymentMeta struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Trigger     string     `json:"trigger"`
	ServiceName string     `json:"service_name"`
	ServiceURL  string     `json:"service_url"`
	EnvName     string     `json:"env_name"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

// DeploymentDetail is returned by GET /api/deployments/{id}
type DeploymentDetail struct {
	ID          string           `json:"id"`
	Status      string           `json:"status"`
	Trigger     string           `json:"trigger"`
	Error       string           `json:"error,omitempty"`
	BuildLog    string           `json:"build_log,omitempty"`
	ImageURI    string           `json:"image_uri,omitempty"`
	ServiceName string           `json:"service_name"`
	ServiceURL  string           `json:"service_url"`
	ServiceType string           `json:"service_type"`
	EnvName     string           `json:"env_name"`
	ProjectName string           `json:"project_name"`
	StartedAt   time.Time        `json:"started_at"`
	FinishedAt  *time.Time       `json:"finished_at,omitempty"`
	Steps       []DeploymentStep `json:"steps"`
}

// DeploymentStep is a single step in a deployment.
type DeploymentStep struct {
	ID         string     `json:"id"`
	Step       string     `json:"step"`
	Status     string     `json:"status"`
	Error      string     `json:"error,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// DeploymentListItem is returned by GET /api/projects/{id}/deployments
type DeploymentListItem struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Trigger     string     `json:"trigger"`
	Error       string     `json:"error,omitempty"`
	ServiceName string     `json:"service_name"`
	ServiceURL  string     `json:"service_url"`
	EnvName     string     `json:"env_name"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

// createProject handles POST /api/projects
func (h *apiProjectsHandler) createProject(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var req struct {
		Name   string `json:"name"`
		Region string `json:"region"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if req.Region == "" {
		req.Region = "us-east4"
	}

	// Resolve user's team
	var teamID string
	err := h.db.QueryRow(r.Context(), `
		SELECT team_id FROM team_members WHERE user_id = $1 LIMIT 1
	`, userID).Scan(&teamID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not resolve team"})
		return
	}

	var p ProjectRow
	err = h.db.QueryRow(r.Context(), `
		INSERT INTO projects (team_id, name, region)
		VALUES ($1, $2, $3)
		RETURNING id, name, region, updated_at
	`, teamID, req.Name, req.Region).Scan(&p.ID, &p.Name, &p.Region, &p.UpdatedAt)
	if err != nil {
		slog.Error("createProject insert", "error", err)
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "project name already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"project": p})
}

// listProjects handles GET /api/projects
// Returns all projects the authenticated user has access to, with last deployment info.
func (h *apiProjectsHandler) listProjects(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT p.id, p.name, p.region, p.updated_at
		FROM projects p
		JOIN teams t ON t.id = p.team_id
		JOIN team_members tm ON tm.team_id = t.id
		WHERE tm.user_id = $1
		ORDER BY p.updated_at DESC
	`, userID)
	if err != nil {
		slog.Error("listProjects query", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	projects := []ProjectRow{}
	projectIDs := []string{}
	projectMap := map[string]*ProjectRow{}

	for rows.Next() {
		var p ProjectRow
		if err := rows.Scan(&p.ID, &p.Name, &p.Region, &p.UpdatedAt); err != nil {
			slog.Error("listProjects scan", "error", err)
			continue
		}
		projects = append(projects, p)
		projectIDs = append(projectIDs, p.ID)
		projectMap[p.ID] = &projects[len(projects)-1]
	}

	if len(projectIDs) > 0 {
		// Fetch last deployment per project in one query using DISTINCT ON
		depRows, err := h.db.Query(r.Context(), `
			SELECT DISTINCT ON (p.id)
			  p.id as project_id,
			  d.id, d.status, d.trigger, d.started_at, d.finished_at,
			  s.name as service_name, s.url as service_url,
			  e.name as env_name
			FROM projects p
			JOIN environments e ON e.project_id = p.id
			JOIN services s ON s.environment_id = e.id
			JOIN deployments d ON d.service_id = s.id
			WHERE p.id = ANY($1)
			ORDER BY p.id, d.created_at DESC
		`, projectIDs)
		if err == nil {
			defer depRows.Close()
			for depRows.Next() {
				var projectID string
				var dm DeploymentMeta
				if err := depRows.Scan(
					&projectID,
					&dm.ID, &dm.Status, &dm.Trigger, &dm.StartedAt, &dm.FinishedAt,
					&dm.ServiceName, &dm.ServiceURL, &dm.EnvName,
				); err != nil {
					continue
				}
				if p, ok := projectMap[projectID]; ok {
					p.LastDeploy = &dm
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

// listDeployments handles GET /api/projects/{id}/deployments
func (h *apiProjectsHandler) listDeployments(w http.ResponseWriter, r *http.Request) {
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

	rows, err := h.db.Query(r.Context(), `
		SELECT d.id, d.status, d.trigger, d.error, d.started_at, d.finished_at,
		       s.name as service_name, s.url as service_url, e.name as env_name
		FROM deployments d
		JOIN services s ON s.id = d.service_id
		JOIN environments e ON e.id = s.environment_id
		JOIN projects p ON p.id = e.project_id
		JOIN teams t ON t.id = p.team_id
		JOIN team_members tm ON tm.team_id = t.id
		WHERE p.id = $1 AND tm.user_id = $2
		ORDER BY d.created_at DESC
		LIMIT 30
	`, projectID, userID)
	if err != nil {
		slog.Error("listDeployments query", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	deployments := []DeploymentListItem{}
	for rows.Next() {
		var d DeploymentListItem
		if err := rows.Scan(
			&d.ID, &d.Status, &d.Trigger, &d.Error, &d.StartedAt, &d.FinishedAt,
			&d.ServiceName, &d.ServiceURL, &d.EnvName,
		); err != nil {
			continue
		}
		deployments = append(deployments, d)
	}

	writeJSON(w, http.StatusOK, map[string]any{"deployments": deployments})
}

// getDeployment handles GET /api/deployments/{id}
func (h *apiProjectsHandler) getDeployment(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	deploymentID := r.PathValue("id")
	if deploymentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing deployment id"})
		return
	}

	var d DeploymentDetail
	err := h.db.QueryRow(r.Context(), `
		SELECT d.id, d.status, d.trigger, d.error, d.build_log, d.image_uri,
		       d.started_at, d.finished_at,
		       s.name, s.url, s.type, e.name, p.name
		FROM deployments d
		JOIN services s ON s.id = d.service_id
		JOIN environments e ON e.id = s.environment_id
		JOIN projects p ON p.id = e.project_id
		JOIN teams t ON t.id = p.team_id
		JOIN team_members tm ON tm.team_id = t.id
		WHERE d.id = $1 AND tm.user_id = $2
	`, deploymentID, userID).Scan(
		&d.ID, &d.Status, &d.Trigger, &d.Error, &d.BuildLog, &d.ImageURI,
		&d.StartedAt, &d.FinishedAt,
		&d.ServiceName, &d.ServiceURL, &d.ServiceType, &d.EnvName, &d.ProjectName,
	)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "deployment not found"})
		return
	}

	// Fetch deployment steps
	stepRows, err := h.db.Query(r.Context(), `
		SELECT id, step, status, error, started_at, finished_at
		FROM deployment_steps
		WHERE deployment_id = $1
		ORDER BY COALESCE(started_at, NOW()) ASC
	`, deploymentID)
	if err == nil {
		defer stepRows.Close()
		for stepRows.Next() {
			var s DeploymentStep
			if err := stepRows.Scan(&s.ID, &s.Step, &s.Status, &s.Error, &s.StartedAt, &s.FinishedAt); err != nil {
				continue
			}
			d.Steps = append(d.Steps, s)
		}
	}
	if d.Steps == nil {
		d.Steps = []DeploymentStep{}
	}

	writeJSON(w, http.StatusOK, d)
}

// deleteProject handles DELETE /api/projects/{id}
func (h *apiProjectsHandler) deleteProject(w http.ResponseWriter, r *http.Request) {
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

	if _, err := h.db.Exec(r.Context(), `DELETE FROM projects WHERE id = $1`, projectID); err != nil {
		slog.Error("deleteProject exec", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *apiProjectsHandler) scaleService(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireProjectAccess(w, r); !ok {
		return
	}

	projectID := r.PathValue("id")
	var req struct {
		Service      string `json:"service"`
		MinInstances int    `json:"min_instances"`
		MaxInstances int    `json:"max_instances"`
		CPU          string `json:"cpu"`
		Memory       string `json:"memory"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}
	if req.Service == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing service"})
		return
	}

	result, err := h.projectSvc.Scale(r.Context(), projectsvc.ScaleInput{
		ProjectID:    projectID,
		ServiceName:  req.Service,
		MinInstances: req.MinInstances,
		MaxInstances: req.MaxInstances,
		CPU:          req.CPU,
		Memory:       req.Memory,
	})
	if err != nil {
		writeProjectError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "scaled",
		"service": result.ServiceName,
		"config": map[string]any{
			"min_instances": result.Applied.MinInstances,
			"max_instances": result.Applied.MaxInstances,
			"cpu":           result.Applied.CPU,
			"memory":        result.Applied.Memory,
		},
	})
}

func (h *apiProjectsHandler) restartService(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireProjectAccess(w, r); !ok {
		return
	}

	projectID := r.PathValue("id")
	var req struct {
		Service string `json:"service"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}
	if req.Service == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing service"})
		return
	}

	result, err := h.projectSvc.Restart(r.Context(), projectsvc.RestartInput{
		ProjectID:   projectID,
		ServiceName: req.Service,
	})
	if err != nil {
		writeProjectError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "restarting",
		"service": result.ServiceName,
	})
}

func (h *apiProjectsHandler) rollbackService(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireProjectAccess(w, r); !ok {
		return
	}

	projectID := r.PathValue("id")
	var req struct {
		Service      string `json:"service"`
		DeploymentID string `json:"deployment_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}
	if req.Service == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing service"})
		return
	}

	result, err := h.projectSvc.Rollback(r.Context(), projectsvc.RollbackInput{
		ProjectID:    projectID,
		ServiceName:  req.Service,
		DeploymentID: req.DeploymentID,
	})
	if err != nil {
		writeProjectError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "rolled_back",
		"service":        req.Service,
		"rolled_back_to": result.RolledBackTo,
		"image":          result.ImageURI,
	})
}

func (h *apiProjectsHandler) requireProjectAccess(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := requireUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return "", false
	}

	projectID := r.PathValue("id")
	if projectID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing project id"})
		return "", false
	}

	if _, err := projectTeamForUser(r.Context(), h.db, projectID, userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
			return "", false
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return "", false
	}

	return projectID, true
}

func writeProjectError(w http.ResponseWriter, err error) {
	switch {
	case projectsvc.IsCode(err, projectsvc.ErrInvalidArg):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case projectsvc.IsCode(err, projectsvc.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case projectsvc.IsCode(err, projectsvc.ErrUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	case projectsvc.IsCode(err, projectsvc.ErrExternal):
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON encode", "error", err)
	}
}
