package billing

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/alan890104/launchkit/server/internal/provider"
)

type mockCompute struct {
	deployErrs  map[string]error
	deployCalls []provider.DeployOpts
}

func (m *mockCompute) CreateService(context.Context, provider.DeployOpts) (string, error) {
	return "", errors.New("not implemented")
}

func (m *mockCompute) Deploy(_ context.Context, opts provider.DeployOpts) (string, error) {
	m.deployCalls = append(m.deployCalls, opts)
	if err := m.deployErrs[opts.ServiceName]; err != nil {
		return "", err
	}
	return "https://" + opts.ServiceName, nil
}

func (m *mockCompute) GetStatus(context.Context, string, string) (*provider.ServiceStatus, error) {
	return nil, errors.New("not implemented")
}

func (m *mockCompute) GetLogs(context.Context, string, string, int) ([]provider.LogEntry, error) {
	return nil, errors.New("not implemented")
}

func (m *mockCompute) DeleteService(context.Context, string, string) error {
	return errors.New("not implemented")
}

func (m *mockCompute) GetMetrics(context.Context, provider.MetricsQueryParams) ([]provider.MetricsResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *mockCompute) GetUptime(context.Context, string, string, string) (provider.UptimeInfo, error) {
	return provider.UptimeInfo{}, errors.New("not implemented")
}

type mockAlertStore struct {
	query func(ctx context.Context, sql string, args ...any) (alertRows, error)
	exec  func(ctx context.Context, sql string, args ...any) error
}

func (s mockAlertStore) Query(ctx context.Context, sql string, args ...any) (alertRows, error) {
	if s.query == nil {
		return &sliceAlertRows{}, nil
	}
	return s.query(ctx, sql, args...)
}

func (s mockAlertStore) Exec(ctx context.Context, sql string, args ...any) error {
	if s.exec == nil {
		return nil
	}
	return s.exec(ctx, sql, args...)
}

type sliceAlertRows struct {
	rows      [][]any
	nextIdx   int
	current   []any
	scanErrAt map[int]error
}

func (r *sliceAlertRows) Next() bool {
	if r.nextIdx >= len(r.rows) {
		r.current = nil
		return false
	}
	r.current = r.rows[r.nextIdx]
	r.nextIdx++
	return true
}

func (r *sliceAlertRows) Scan(dest ...any) error {
	if r.current == nil {
		return errors.New("Scan called without Next")
	}
	if err := r.scanErrAt[r.nextIdx-1]; err != nil {
		return err
	}
	if len(dest) != len(r.current) {
		return errors.New("unexpected scan destination count")
	}
	for i := range dest {
		if err := assignScanValue(dest[i], r.current[i]); err != nil {
			return err
		}
	}
	return nil
}

func (r *sliceAlertRows) Close() {}

func TestSuspendTeamCompute(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	tests := []struct {
		name          string
		queryErr      error
		rows          [][]any
		deployErrs    map[string]error
		want          bool
		wantDeploys   int
		wantMinScales []int
	}{
		{
			name:        "returns false when db query fails",
			queryErr:    errors.New("db down"),
			want:        false,
			wantDeploys: 0,
		},
		{
			name: "returns false when any deploy fails",
			rows: [][]any{
				{`{"service_name":"svc-a","region":"us-east1"}`},
				{`{"service_name":"svc-b","region":"us-west1"}`},
			},
			deployErrs: map[string]error{
				"svc-b": errors.New("provider error"),
			},
			want:          false,
			wantDeploys:   2,
			wantMinScales: []int{0, 0},
		},
		{
			name: "returns true only when all deploys succeed",
			rows: [][]any{
				{`{"service_name":"svc-a","region":"us-east1"}`},
				{`{"service_name":"svc-b","region":"us-west1"}`},
			},
			want:          true,
			wantDeploys:   2,
			wantMinScales: []int{0, 0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			compute := &mockCompute{deployErrs: tc.deployErrs}
			store := mockAlertStore{
				query: func(context.Context, string, ...any) (alertRows, error) {
					if tc.queryErr != nil {
						return nil, tc.queryErr
					}
					return &sliceAlertRows{rows: tc.rows}, nil
				},
			}

			got := suspendTeamComputeWithStore(ctx, store, compute, "team-1", logger)
			if got != tc.want {
				t.Fatalf("suspendTeamComputeWithStore() = %v, want %v", got, tc.want)
			}
			if len(compute.deployCalls) != tc.wantDeploys {
				t.Fatalf("deploy call count = %d, want %d", len(compute.deployCalls), tc.wantDeploys)
			}
			for i, call := range compute.deployCalls {
				if call.MinScale != tc.wantMinScales[i] {
					t.Fatalf("deploy call %d min_scale = %d, want %d", i, call.MinScale, tc.wantMinScales[i])
				}
			}
		})
	}
}

func TestCheckZeroBalance_SetsSuspendedAtOnlyOnFullSuspension(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	tests := []struct {
		name             string
		suspendOK        bool
		wantSuspendedAt  bool
		wantFailureAudit bool
		wantSuccessAudit bool
	}{
		{
			name:             "does not set suspended_at when suspension fails",
			suspendOK:        false,
			wantSuspendedAt:  false,
			wantFailureAudit: true,
			wantSuccessAudit: false,
		},
		{
			name:             "sets suspended_at when suspension succeeds",
			suspendOK:        true,
			wantSuspendedAt:  true,
			wantFailureAudit: false,
			wantSuccessAudit: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var execSQLs []string
			store := mockAlertStore{
				query: func(context.Context, string, ...any) (alertRows, error) {
					return &sliceAlertRows{
						rows: [][]any{
							{"team-1", "Acme", float64(0)},
						},
					}, nil
				},
				exec: func(_ context.Context, sql string, _ ...any) error {
					execSQLs = append(execSQLs, sql)
					return nil
				},
			}

			checkZeroBalanceWithStore(ctx, store, &mockCompute{}, logger, func(context.Context, balanceAlertStore, provider.Compute, string, *slog.Logger) bool {
				return tc.suspendOK
			})

			gotSuspendedAt := anySQLContains(execSQLs, "SET suspended_at = NOW(), suspension_reason = 'zero_balance'")
			if gotSuspendedAt != tc.wantSuspendedAt {
				t.Fatalf("suspended_at update present = %v, want %v; exec SQLs = %v", gotSuspendedAt, tc.wantSuspendedAt, execSQLs)
			}

			gotFailureAudit := anySQLContains(execSQLs, "'suspension_failed'")
			if gotFailureAudit != tc.wantFailureAudit {
				t.Fatalf("failure audit present = %v, want %v; exec SQLs = %v", gotFailureAudit, tc.wantFailureAudit, execSQLs)
			}

			gotSuccessAudit := anySQLContains(execSQLs, "'team_suspended'")
			if gotSuccessAudit != tc.wantSuccessAudit {
				t.Fatalf("success audit present = %v, want %v; exec SQLs = %v", gotSuccessAudit, tc.wantSuccessAudit, execSQLs)
			}
		})
	}
}

func anySQLContains(sqls []string, needle string) bool {
	for _, sql := range sqls {
		if strings.Contains(sql, needle) {
			return true
		}
	}
	return false
}
