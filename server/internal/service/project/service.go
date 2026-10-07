package project

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/alan890104/launchkit/server/internal/config"
	"github.com/alan890104/launchkit/server/internal/deploy"
	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ErrorCode string

const (
	ErrNotFound     ErrorCode = "not_found"
	ErrUnauthorized ErrorCode = "unauthorized"
	ErrUnavailable  ErrorCode = "unavailable"
	ErrInvalidArg   ErrorCode = "invalid_argument"
	ErrInternal     ErrorCode = "internal"
	ErrExternal     ErrorCode = "external"
)

type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string { return e.Message }

func IsCode(err error, code ErrorCode) bool {
	var svcErr *Error
	return errors.As(err, &svcErr) && svcErr.Code == code
}

func newError(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

type Service struct {
	db      *pgxpool.Pool
	compute provider.Compute
	cfg     *config.Config
	logger  *slog.Logger
}

func New(db *pgxpool.Pool, compute provider.Compute, cfg *config.Config) *Service {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &Service{
		db:      db,
		compute: compute,
		cfg:     cfg,
		logger:  slog.Default(),
	}
}

type ScaleInput struct {
	ProjectID    string
	ServiceName  string
	MinInstances int
	MaxInstances int
	CPU          string
	Memory       string
}

type ScaleResult struct {
	ServiceName string
	Applied     ScaleInput
}

type RestartInput struct {
	ProjectID   string
	ServiceName string
}

type RestartResult struct {
	ServiceName string
}

type DestroyInput struct {
	ProjectID string
	Confirm   bool
}

type DestroyResult struct {
	Services  int
	Databases int
	Caches    int
	Storage   int
	Domains   int
}

type RollbackInput struct {
	ProjectID    string
	ServiceName  string
	DeploymentID string
}

type RollbackResult struct {
	RolledBackTo string
	ImageURI     string
}

type CancelInput struct {
	ProjectID    string
	DeploymentID string
}

type CancelResult struct {
	DeploymentID string
}

type UpdateEnvInput struct {
	ProjectID   string
	ServiceName string
	EnvVars     map[string]string
}

type UpdateEnvResult struct {
	Updated int
}

const (
	scaleLookupServiceSQL = `
		SELECT s.target, s.environment_id
		FROM services s
		JOIN environments e ON s.environment_id = e.id
		WHERE e.project_id = $1 AND s.name = $2
	`
	restartLookupServiceSQL = `
		SELECT s.target, COALESCE(d.image_uri, ''), s.environment_id
		FROM services s
		JOIN environments e ON s.environment_id = e.id
		LEFT JOIN deployments d ON s.id = d.service_id AND d.status = 'live'
		WHERE e.project_id = $1 AND s.name = $2
		ORDER BY d.created_at DESC LIMIT 1
	`
	destroyCountServicesSQL = `
		SELECT COUNT(*) FROM services s
		JOIN environments e ON s.environment_id = e.id
		WHERE e.project_id = $1
	`
	destroyCountResourcesSQL = `
		SELECT
			COUNT(*) FILTER (WHERE type = 'postgres') as databases,
			COUNT(*) FILTER (WHERE type = 'redis') as caches,
			COUNT(*) FILTER (WHERE type = 'storage') as storage
		FROM resources r
		JOIN environments e ON r.environment_id = e.id
		WHERE e.project_id = $1
	`
	destroyCountDomainsSQL = `
		SELECT COUNT(*)
		FROM domains d
		JOIN services s ON d.service_id = s.id
		JOIN environments e ON s.environment_id = e.id
		WHERE e.project_id = $1
	`
	destroyListServicesSQL = `
		SELECT s.name, s.target, e.id as env_id
		FROM services s
		JOIN environments e ON s.environment_id = e.id
		WHERE e.project_id = $1
	`
	destroyDeleteProjectSQL      = `DELETE FROM projects WHERE id = $1`
	rollbackLookupDeploymentsSQL = `
		SELECT d.id, d.image_uri, d.created_at, s.environment_id
		FROM deployments d
		JOIN services s ON d.service_id = s.id
		JOIN environments e ON s.environment_id = e.id
		WHERE e.project_id = $1 AND s.name = $2 AND d.status = 'live'
	`
	rollbackUpdateLiveStatusSQL = `
		UPDATE deployments SET status = 'live', updated_at = NOW() WHERE id = $1
	`
	cancelAtomicSQL = `
		UPDATE deployments
		SET status = 'cancelled', finished_at = NOW(), updated_at = NOW()
		FROM services s
		JOIN environments e ON s.environment_id = e.id
		WHERE deployments.id = $1
		  AND deployments.service_id = s.id
		  AND e.project_id = $2
		  AND deployments.status IN ('pending', 'building', 'deploying')
		RETURNING deployments.id
	`
	updateEnvLookupServiceSQL = `
		SELECT s.id, s.target, COALESCE(d.image_uri, ''), s.environment_id
		FROM services s
		JOIN environments e ON s.environment_id = e.id
		LEFT JOIN deployments d ON s.id = d.service_id AND d.status = 'live'
		WHERE e.project_id = $1 AND s.name = $2
	`
	updateEnvUpsertBindingSQL = `
		INSERT INTO env_bindings (id, service_id, key, value, source)
		VALUES (gen_random_uuid()::TEXT, $1, $2, $3, 'user_input')
		ON CONFLICT (service_id, key) DO UPDATE SET
			value = EXCLUDED.value,
			secret_id = NULL,
			source = 'user_input'
	`
	updateEnvListBindingsSQL = `
		SELECT eb.key, COALESCE(eb.value, '') as val, COALESCE(s.name, '') as secret_name
		FROM env_bindings eb
		LEFT JOIN secrets s ON eb.secret_id = s.id
		WHERE eb.service_id = $1
	`
)

type rollbackCandidate struct {
	ID        string
	ImageURI  string
	CreatedAt time.Time
	EnvID     string
}

func (s *Service) Scale(ctx context.Context, input ScaleInput) (*ScaleResult, error) {
	if s.compute == nil {
		return nil, newError(ErrUnavailable, "cloud provider not configured")
	}
	if s.db == nil {
		return nil, newError(ErrInternal, "database not configured")
	}

	var target, envID string
	err := s.db.QueryRow(ctx, scaleLookupServiceSQL, input.ProjectID, input.ServiceName).Scan(&target, &envID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, newError(ErrNotFound, "service not found")
		}
		return nil, newError(ErrInternal, "lookup service: %v", err)
	}
	if !deploy.IsComputeTarget(deploy.ServiceTarget(target)) {
		return nil, newError(ErrInvalidArg, "scaling only available for compute services")
	}

	_, err = s.compute.Deploy(ctx, provider.DeployOpts{
		ServiceName: s.cloudServiceName(input.ProjectID, envID, input.ServiceName),
		Region:      s.cfg.Region(),
		MinScale:    input.MinInstances,
		MaxScale:    input.MaxInstances,
		CPU:         input.CPU,
		Memory:      input.Memory,
	})
	if err != nil {
		return nil, newError(ErrExternal, "scale service: %v", err)
	}

	return &ScaleResult{
		ServiceName: input.ServiceName,
		Applied:     input,
	}, nil
}

func (s *Service) Restart(ctx context.Context, input RestartInput) (*RestartResult, error) {
	if s.compute == nil {
		return nil, newError(ErrUnavailable, "cloud provider not configured")
	}
	if s.db == nil {
		return nil, newError(ErrInternal, "database not configured")
	}

	var target, imageURI, envID string
	err := s.db.QueryRow(ctx, restartLookupServiceSQL, input.ProjectID, input.ServiceName).Scan(&target, &imageURI, &envID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, newError(ErrNotFound, "service not found")
		}
		return nil, newError(ErrInternal, "lookup service: %v", err)
	}
	if !deploy.IsComputeTarget(deploy.ServiceTarget(target)) {
		return nil, newError(ErrInvalidArg, "restart only available for compute services")
	}

	_, err = s.compute.Deploy(ctx, provider.DeployOpts{
		ServiceName: s.cloudServiceName(input.ProjectID, envID, input.ServiceName),
		Region:      s.cfg.Region(),
		ImageURI:    imageURI,
	})
	if err != nil {
		return nil, newError(ErrExternal, "restart service: %v", err)
	}

	return &RestartResult{ServiceName: input.ServiceName}, nil
}

func (s *Service) Destroy(ctx context.Context, input DestroyInput) (*DestroyResult, error) {
	if !input.Confirm {
		return nil, newError(ErrInvalidArg, "confirm must be true")
	}
	if s.compute == nil {
		return nil, newError(ErrUnavailable, "cloud provider not configured")
	}
	if s.db == nil {
		return nil, newError(ErrInternal, "database not configured")
	}

	result := &DestroyResult{}
	if err := s.db.QueryRow(ctx, destroyCountServicesSQL, input.ProjectID).Scan(&result.Services); err != nil {
		return nil, newError(ErrInternal, "count services: %v", err)
	}
	if err := s.db.QueryRow(ctx, destroyCountResourcesSQL, input.ProjectID).Scan(&result.Databases, &result.Caches, &result.Storage); err != nil {
		return nil, newError(ErrInternal, "count resources: %v", err)
	}
	if err := s.db.QueryRow(ctx, destroyCountDomainsSQL, input.ProjectID).Scan(&result.Domains); err != nil {
		return nil, newError(ErrInternal, "count domains: %v", err)
	}

	rows, err := s.db.Query(ctx, destroyListServicesSQL, input.ProjectID)
	if err != nil {
		return nil, newError(ErrInternal, "list services: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var serviceName, target, envID string
		if err := rows.Scan(&serviceName, &target, &envID); err != nil {
			return nil, newError(ErrInternal, "scan service: %v", err)
		}
		if !deploy.IsComputeTarget(deploy.ServiceTarget(target)) {
			continue
		}
		if err := s.compute.DeleteService(ctx, s.cloudServiceName(input.ProjectID, envID, serviceName), s.cfg.Region()); err != nil {
			s.logger.Warn("failed to delete compute service during destroy", "service", serviceName, "error", err)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, newError(ErrInternal, "iterate services: %v", err)
	}

	if _, err := s.db.Exec(ctx, destroyDeleteProjectSQL, input.ProjectID); err != nil {
		return nil, newError(ErrInternal, "delete project: %v", err)
	}

	return result, nil
}

func (s *Service) Rollback(ctx context.Context, input RollbackInput) (*RollbackResult, error) {
	if s.compute == nil {
		return nil, newError(ErrUnavailable, "cloud provider not configured")
	}
	if s.db == nil {
		return nil, newError(ErrInternal, "database not configured")
	}

	query := rollbackLookupDeploymentsSQL
	args := []any{input.ProjectID, input.ServiceName}
	if input.DeploymentID != "" {
		query += " AND d.id = $3"
		args = append(args, input.DeploymentID)
	} else {
		query += " ORDER BY d.created_at DESC LIMIT 2"
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, newError(ErrInternal, "query deployments: %v", err)
	}
	defer rows.Close()

	var candidates []rollbackCandidate
	for rows.Next() {
		var candidate rollbackCandidate
		if err := rows.Scan(&candidate.ID, &candidate.ImageURI, &candidate.CreatedAt, &candidate.EnvID); err != nil {
			return nil, newError(ErrInternal, "scan deployment: %v", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, newError(ErrInternal, "iterate deployments: %v", err)
	}

	target, err := pickRollbackCandidate(candidates, input.DeploymentID)
	if err != nil {
		return nil, err
	}

	_, err = s.compute.Deploy(ctx, provider.DeployOpts{
		ServiceName: s.cloudServiceName(input.ProjectID, target.EnvID, input.ServiceName),
		Region:      s.cfg.Region(),
		ImageURI:    target.ImageURI,
	})
	if err != nil {
		return nil, newError(ErrExternal, "rollback deploy failed: %v", err)
	}

	if _, err := s.db.Exec(ctx, rollbackUpdateLiveStatusSQL, target.ID); err != nil {
		return nil, newError(ErrInternal, "update deployment status: %v", err)
	}

	return &RollbackResult{
		RolledBackTo: target.ID,
		ImageURI:     target.ImageURI,
	}, nil
}

func (s *Service) Cancel(ctx context.Context, input CancelInput) (*CancelResult, error) {
	if s.db == nil {
		return nil, newError(ErrInternal, "database not configured")
	}

	var cancelledID string
	err := s.db.QueryRow(ctx, cancelAtomicSQL, input.DeploymentID, input.ProjectID).Scan(&cancelledID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Either not found (wrong project) or not in a cancellable state.
			// Distinguish by checking existence separately to give a useful error.
			var exists bool
			_ = s.db.QueryRow(ctx,
				`SELECT EXISTS(
					SELECT 1 FROM deployments d
					JOIN services s ON d.service_id = s.id
					JOIN environments e ON s.environment_id = e.id
					WHERE d.id = $1 AND e.project_id = $2
				)`, input.DeploymentID, input.ProjectID).Scan(&exists)
			if !exists {
				return nil, newError(ErrNotFound, "deployment not found")
			}
			return nil, newError(ErrInvalidArg, "deployment is not in a cancellable state")
		}
		return nil, newError(ErrInternal, "cancel deployment: %v", err)
	}

	return &CancelResult{DeploymentID: cancelledID}, nil
}

func (s *Service) UpdateEnv(ctx context.Context, input UpdateEnvInput) (*UpdateEnvResult, error) {
	if s.db == nil {
		return nil, newError(ErrInternal, "database not configured")
	}

	var serviceID, target, imageURI, envID string
	err := s.db.QueryRow(ctx, updateEnvLookupServiceSQL, input.ProjectID, input.ServiceName).Scan(&serviceID, &target, &imageURI, &envID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, newError(ErrNotFound, "service not found")
		}
		return nil, newError(ErrInternal, "lookup service: %v", err)
	}

	updated := 0
	for key, value := range input.EnvVars {
		if err := deploy.ValidateEnvKeyForUpdate(key); err != nil {
			return nil, newError(ErrInvalidArg, "invalid env key: %v", err)
		}
		if _, err := s.db.Exec(ctx, updateEnvUpsertBindingSQL, serviceID, key, value); err != nil {
			return nil, newError(ErrInternal, "store env binding: %v", err)
		}
		updated++
	}

	if updated > 0 && deploy.IsComputeTarget(deploy.ServiceTarget(target)) {
		if s.compute == nil {
			return nil, newError(ErrUnavailable, "cloud provider not configured")
		}

		rows, err := s.db.Query(ctx, updateEnvListBindingsSQL, serviceID)
		if err != nil {
			return nil, newError(ErrInternal, "query env bindings: %v", err)
		}
		defer rows.Close()

		envVars := make(map[string]string)
		for rows.Next() {
			var key, value, secretName string
			if err := rows.Scan(&key, &value, &secretName); err != nil {
				return nil, newError(ErrInternal, "scan env binding: %v", err)
			}
			if secretName != "" {
				continue
			}
			envVars[key] = value
		}
		if err := rows.Err(); err != nil {
			return nil, newError(ErrInternal, "iterate env bindings: %v", err)
		}

		_, err = s.compute.Deploy(ctx, provider.DeployOpts{
			ServiceName: s.cloudServiceName(input.ProjectID, envID, input.ServiceName),
			Region:      s.cfg.Region(),
			ImageURI:    imageURI,
			EnvVars:     envVars,
		})
		if err != nil {
			return nil, newError(ErrExternal, "restart service after env update: %v", err)
		}
	}

	return &UpdateEnvResult{Updated: updated}, nil
}

func (s *Service) lookupServiceRef(ctx context.Context, projectID, serviceName string) (target, envID, imageURI string, err error) {
	if s.db == nil {
		return "", "", "", newError(ErrInternal, "database not configured")
	}

	err = s.db.QueryRow(ctx, restartLookupServiceSQL, projectID, serviceName).Scan(&target, &imageURI, &envID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", "", newError(ErrNotFound, "service not found")
		}
		return "", "", "", newError(ErrInternal, "lookup service: %v", err)
	}
	return target, envID, imageURI, nil
}

func (s *Service) cloudServiceName(projectID, envID, serviceName string) string {
	return deploy.ScopeServiceNameWithEnv(projectID, envID, serviceName)
}

func pickRollbackCandidate(candidates []rollbackCandidate, deploymentID string) (rollbackCandidate, error) {
	if len(candidates) == 0 {
		return rollbackCandidate{}, newError(ErrNotFound, "no previous deployment")
	}
	if deploymentID == "" {
		if len(candidates) < 2 {
			return rollbackCandidate{}, newError(ErrNotFound, "no previous deployment")
		}
		if strings.TrimSpace(candidates[1].ImageURI) == "" {
			return rollbackCandidate{}, newError(ErrInvalidArg, "target deployment has no image")
		}
		return candidates[1], nil
	}
	if strings.TrimSpace(candidates[0].ImageURI) == "" {
		return rollbackCandidate{}, newError(ErrInvalidArg, "target deployment has no image")
	}
	return candidates[0], nil
}
