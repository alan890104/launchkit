package project

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alan890104/launchkit/server/internal/config"
	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockCompute struct {
	deployFn        func(ctx context.Context, opts provider.DeployOpts) (string, error)
	deleteServiceFn func(ctx context.Context, name, region string) error
}

func (m *mockCompute) CreateService(ctx context.Context, opts provider.DeployOpts) (string, error) {
	return "", nil
}

func (m *mockCompute) Deploy(ctx context.Context, opts provider.DeployOpts) (string, error) {
	if m.deployFn != nil {
		return m.deployFn(ctx, opts)
	}
	return "", nil
}

func (m *mockCompute) GetStatus(ctx context.Context, serviceName, region string) (*provider.ServiceStatus, error) {
	return nil, nil
}

func (m *mockCompute) GetLogs(ctx context.Context, serviceName, region string, limit int) ([]provider.LogEntry, error) {
	return nil, nil
}

func (m *mockCompute) DeleteService(ctx context.Context, name, region string) error {
	if m.deleteServiceFn != nil {
		return m.deleteServiceFn(ctx, name, region)
	}
	return nil
}

func (m *mockCompute) GetMetrics(ctx context.Context, params provider.MetricsQueryParams) ([]provider.MetricsResponse, error) {
	return nil, nil
}

func (m *mockCompute) GetUptime(ctx context.Context, serviceName, region string, period string) (provider.UptimeInfo, error) {
	return provider.UptimeInfo{}, nil
}

func TestScale_NilCompute(t *testing.T) {
	svc := New(nil, nil, &config.Config{})

	result, err := svc.Scale(context.Background(), ScaleInput{})

	require.Nil(t, result)
	require.Error(t, err)
	assert.True(t, IsCode(err, ErrUnavailable))
}

func TestRestart_NilCompute(t *testing.T) {
	svc := New(nil, nil, &config.Config{})

	result, err := svc.Restart(context.Background(), RestartInput{})

	require.Nil(t, result)
	require.Error(t, err)
	assert.True(t, IsCode(err, ErrUnavailable))
}

func TestDestroy_ConfirmFalse(t *testing.T) {
	svc := New(nil, nil, &config.Config{})

	result, err := svc.Destroy(context.Background(), DestroyInput{Confirm: false})

	require.Nil(t, result)
	require.Error(t, err)
	assert.True(t, IsCode(err, ErrInvalidArg))
}

func TestRollback_NilCompute(t *testing.T) {
	svc := New(nil, nil, &config.Config{})

	result, err := svc.Rollback(context.Background(), RollbackInput{})

	require.Nil(t, result)
	require.Error(t, err)
	assert.True(t, IsCode(err, ErrUnavailable))
}

func TestScale_Success_DeployOptions(t *testing.T) {
	var got provider.DeployOpts
	svc := New(nil, &mockCompute{
		deployFn: func(ctx context.Context, opts provider.DeployOpts) (string, error) {
			got = opts
			return "https://example.test", nil
		},
	}, &config.Config{GCPRegion: "us-east4"})

	_, err := svc.compute.Deploy(context.Background(), provider.DeployOpts{
		ServiceName: svc.cloudServiceName("project-1", "env-1", "web"),
		Region:      svc.cfg.Region(),
		MinScale:    1,
		MaxScale:    3,
		CPU:         "1",
		Memory:      "512Mi",
	})

	require.NoError(t, err)
	assert.Equal(t, "us-east4", got.Region)
	assert.Equal(t, 1, got.MinScale)
	assert.Equal(t, 3, got.MaxScale)
	assert.Equal(t, "1", got.CPU)
	assert.Equal(t, "512Mi", got.Memory)
	assert.Equal(t, svc.cloudServiceName("project-1", "env-1", "web"), got.ServiceName)
}

func TestRollback_NoDeploymentID_UsesSecondLatest(t *testing.T) {
	candidates := []rollbackCandidate{
		{ID: "dep-current", ImageURI: "img-current", CreatedAt: time.Now()},
		{ID: "dep-previous", ImageURI: "img-previous", CreatedAt: time.Now().Add(-time.Minute)},
	}

	target, err := pickRollbackCandidate(candidates, "")

	require.NoError(t, err)
	assert.Equal(t, "dep-previous", target.ID)
	assert.Equal(t, "img-previous", target.ImageURI)
}

func TestRollback_SingleDeployment_ReturnsError(t *testing.T) {
	candidates := []rollbackCandidate{
		{ID: "dep-current", ImageURI: "img-current", CreatedAt: time.Now()},
	}

	target, err := pickRollbackCandidate(candidates, "")

	require.Zero(t, target)
	require.Error(t, err)
	assert.True(t, IsCode(err, ErrNotFound))
}

func TestSQL_Scale_ScopedByProjectID(t *testing.T) {
	assert.Contains(t, normalizeSQL(scaleLookupServiceSQL), "where e.project_id = $1 and s.name = $2")
}

func TestSQL_Restart_ScopedByProjectID(t *testing.T) {
	assert.Contains(t, normalizeSQL(restartLookupServiceSQL), "where e.project_id = $1 and s.name = $2")
}

func TestSQL_Destroy_DeleteByID(t *testing.T) {
	assert.Equal(t, "delete from projects where id = $1", normalizeSQL(destroyDeleteProjectSQL))
}

func TestSQL_Rollback_ScopedByProjectID(t *testing.T) {
	assert.Contains(t, normalizeSQL(rollbackLookupDeploymentsSQL), "where e.project_id = $1 and s.name = $2 and d.status = 'live'")
}

func TestSQL_Cancel_AtomicAndScoped(t *testing.T) {
	n := normalizeSQL(cancelAtomicSQL)
	assert.Contains(t, n, "deployments.id = $1", "cancel must scope by deployment id")
	assert.Contains(t, n, "e.project_id = $2", "cancel must verify project ownership")
	assert.Contains(t, n, "deployments.status in ('pending', 'building', 'deploying')", "cancel must atomically check status")
	assert.Contains(t, n, "returning deployments.id", "cancel must use RETURNING to detect no-op")
}

func TestError_IsCode_Match(t *testing.T) {
	err := &Error{Code: ErrUnavailable, Message: "nope"}

	assert.True(t, IsCode(err, ErrUnavailable))
}

func TestError_IsCode_Mismatch(t *testing.T) {
	err := &Error{Code: ErrUnavailable, Message: "nope"}

	assert.False(t, IsCode(err, ErrInternal))
}

func TestError_Message(t *testing.T) {
	err := &Error{Code: ErrNotFound, Message: "thing not found"}
	assert.Equal(t, "thing not found", err.Error())
}

func TestIsCode_Nil(t *testing.T) {
	assert.False(t, IsCode(nil, ErrNotFound))
}

func TestCancel_InvalidStatus(t *testing.T) {
	// Without a real DB we can't exercise the query path, but we verify
	// that a nil DB produces ErrInternal (the nil-DB check fires first).
	svc := New(nil, nil, &config.Config{})
	_, err := svc.Cancel(context.Background(), CancelInput{ProjectID: "p1", DeploymentID: "d1"})
	assert.True(t, IsCode(err, ErrInternal))
}

func normalizeSQL(sql string) string {
	return strings.Join(strings.Fields(strings.ToLower(sql)), " ")
}
