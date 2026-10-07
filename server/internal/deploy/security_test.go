package deploy_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/alan890104/launchkit/server/internal/deploy"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) (*pgxpool.Pool, func()) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)

	cleanup := func() {
		// Clean test data
		ctx := context.Background()
		pool.Exec(ctx, "DELETE FROM deployment_steps")
		pool.Exec(ctx, "DELETE FROM deployments")
		pool.Exec(ctx, "DELETE FROM plans")
		pool.Exec(ctx, "DELETE FROM services")
		pool.Exec(ctx, "DELETE FROM resources")
		pool.Exec(ctx, "DELETE FROM environments")
		pool.Exec(ctx, "DELETE FROM projects")
		pool.Exec(ctx, "DELETE FROM team_members")
		pool.Exec(ctx, "DELETE FROM teams")
		pool.Exec(ctx, "DELETE FROM users")
		pool.Close()
	}

	return pool, cleanup
}

// ============================================================================
// SECURITY TESTS: Plan Validation
// ============================================================================

// TestValidate_PathTraversalInSourceDir tests that path traversal attacks are blocked
func TestValidate_PathTraversalInSourceDir(t *testing.T) {
	tests := []struct {
		name      string
		sourceDir string
		wantErr   bool
		errMsg    string
	}{
		{"valid path", "backend/", false, ""},
		{"valid nested path", "apps/api/", false, ""},
		{"path traversal", "../etc/passwd", true, ".."},
		{"path traversal nested", "backend/../../etc/passwd", true, ".."},
		{"absolute path unix", "/etc/shadow", true, ".."},
		// Windows paths are only detected on Windows; on Unix they pass filepath.IsAbs
		// but this is OK since the app runs on Unix servers
		{"hidden path traversal", "backend/..\\..\\etc\\passwd", true, ".."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := &deploy.Plan{
				ID:        "plan_test",
				ProjectID: "00000000-0000-0000-0000-000000000001",
				Services: []deploy.ServicePlan{
					{
						Name:      "api",
						Type:      deploy.ServiceBackend,
						Target:    deploy.TargetCloudRun,
						SourceDir: tt.sourceDir,
						Port:      8080, // Add valid port
					},
				},
			}

			err := deploy.Validate(plan)
			if tt.wantErr {
				assert.Error(t, err, "path traversal should be rejected")
				if err != nil && tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidate_ShellInjectionInEnvVars tests that env var keys are validated
func TestValidate_ShellInjectionInEnvVars(t *testing.T) {
	tests := []struct {
		name    string
		envKey  string
		wantErr bool
	}{
		{"valid key", "DATABASE_URL", false},
		{"valid with underscore", "API_KEY_123", false},
		{"shell injection", "$(whoami)", true},
		{"backtick injection", "`id`", true},
		{"command separator", "VAR; rm -rf /", true},
		{"pipe injection", "VAR|cat /etc/passwd", true},
		{"spaces in key", "VAR NAME", true},
		{"lowercase key", "database_url", true},
		{"starts with number", "1DATABASE", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := &deploy.Plan{
				ID:        "plan_test",
				ProjectID: "00000000-0000-0000-0000-000000000001",
				Services: []deploy.ServicePlan{
					{
						Name:      "api",
						Type:      deploy.ServiceBackend,
						Target:    deploy.TargetCloudRun,
						SourceDir: "backend/",
						EnvVars: []deploy.EnvVar{
							{
								Key:            tt.envKey,
								Classification: deploy.EnvUserRequired,
							},
						},
					},
				},
			}

			err := deploy.Validate(plan)
			if tt.wantErr {
				assert.Error(t, err, "invalid env key should be rejected")
			} else {
				// Check if error is about env key
				if err != nil && strings.Contains(err.Error(), "key") {
					t.Errorf("valid env key %q was rejected: %v", tt.envKey, err)
				}
			}
		})
	}
}

// TestValidate_DuplicateServiceNames tests that duplicate service names are rejected
func TestValidate_DuplicateServiceNames(t *testing.T) {
	plan := &deploy.Plan{
		ID:        "plan_test",
		ProjectID: "00000000-0000-0000-0000-000000000001",
		Services: []deploy.ServicePlan{
			{Name: "api", Type: deploy.ServiceBackend, Target: deploy.TargetCloudRun, SourceDir: "backend/"},
			{Name: "api", Type: deploy.ServiceBackend, Target: deploy.TargetCloudRun, SourceDir: "api/"}, // Duplicate
		},
	}

	err := deploy.Validate(plan)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

// TestValidate_MissingRequiredFields tests that required fields are validated
func TestValidate_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		plan    *deploy.Plan
		wantErr bool
	}{
		{
			name: "missing plan ID",
			plan: &deploy.Plan{
				ProjectID: "00000000-0000-0000-0000-000000000001",
				Services:  []deploy.ServicePlan{{Name: "api", Type: deploy.ServiceBackend, Target: deploy.TargetCloudRun, SourceDir: "backend/"}},
			},
			wantErr: true,
		},
		{
			name: "missing project ID",
			plan: &deploy.Plan{
				ID:       "plan_test",
				Services: []deploy.ServicePlan{{Name: "api", Type: deploy.ServiceBackend, Target: deploy.TargetCloudRun, SourceDir: "backend/"}},
			},
			wantErr: true,
		},
		{
			name: "no services",
			plan: &deploy.Plan{
				ID:        "plan_test",
				ProjectID: "00000000-0000-0000-0000-000000000001",
			},
			wantErr: true,
		},
		{
			name: "missing service name",
			plan: &deploy.Plan{
				ID:        "plan_test",
				ProjectID: "00000000-0000-0000-0000-000000000001",
				Services:  []deploy.ServicePlan{{Type: deploy.ServiceBackend, Target: deploy.TargetCloudRun, SourceDir: "backend/"}},
			},
			wantErr: true,
		},
		{
			name: "missing service type",
			plan: &deploy.Plan{
				ID:        "plan_test",
				ProjectID: "00000000-0000-0000-0000-000000000001",
				Services:  []deploy.ServicePlan{{Name: "api", Target: deploy.TargetCloudRun, SourceDir: "backend/"}},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := deploy.Validate(tt.plan)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidate_InvalidServiceNameFormat tests service name validation
func TestValidate_InvalidServiceNameFormat(t *testing.T) {
	tests := []struct {
		name    string
		svcName string
		wantErr bool
	}{
		{"valid name", "api", false},
		{"valid with dash", "api-service", false},
		{"valid with numbers", "api123", false},
		{"starts with number", "1api", true},
		{"uppercase", "API", true},
		{"contains underscore", "api_service", true},
		{"contains dot", "api.service", true},
		{"contains space", "api service", true},
		{"XSS payload", "<script>alert(1)</script>", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := &deploy.Plan{
				ID:        "plan_test",
				ProjectID: "00000000-0000-0000-0000-000000000001",
				Services: []deploy.ServicePlan{
					{Name: tt.svcName, Type: deploy.ServiceBackend, Target: deploy.TargetCloudRun, SourceDir: "backend/"},
				},
			}

			err := deploy.Validate(plan)
			if tt.wantErr {
				assert.Error(t, err, "invalid service name should be rejected")
			} else {
				// Check if error is about service name
				if err != nil && strings.Contains(err.Error(), "name") {
					t.Errorf("valid service name %q was rejected: %v", tt.svcName, err)
				}
			}
		})
	}
}

// TestValidate_FrontendMustTargetPages tests frontend target validation
func TestValidate_FrontendMustTargetPages(t *testing.T) {
	tests := []struct {
		name    string
		target  deploy.ServiceTarget
		wantErr bool
	}{
		{"valid: cloudflare pages", deploy.TargetCloudflarePages, false},
		{"invalid: cloud run", deploy.TargetCloudRun, true},
		{"invalid: ecs fargate", deploy.TargetECSFargate, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := &deploy.Plan{
				ID:        "plan_test",
				ProjectID: "00000000-0000-0000-0000-000000000001",
				Services: []deploy.ServicePlan{
					{Name: "web", Type: deploy.ServiceFrontend, Target: tt.target, SourceDir: "frontend/"},
				},
			}

			err := deploy.Validate(plan)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "frontend")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidate_InvalidPortRange tests port validation for compute services
func TestValidate_InvalidPortRange(t *testing.T) {
	tests := []struct {
		name    string
		port    int
		target  deploy.ServiceTarget
		svcType deploy.ServiceType
		wantErr bool
	}{
		{"valid port", 8080, deploy.TargetCloudRun, deploy.ServiceBackend, false},
		{"port 0", 0, deploy.TargetCloudRun, deploy.ServiceBackend, true},
		{"negative port", -1, deploy.TargetCloudRun, deploy.ServiceBackend, true},
		{"port > 65535", 70000, deploy.TargetCloudRun, deploy.ServiceBackend, true},
		{"port 65535", 65535, deploy.TargetCloudRun, deploy.ServiceBackend, false},
		{"port 1", 1, deploy.TargetCloudRun, deploy.ServiceBackend, false},
		{"static service ignores port", 0, deploy.TargetCloudflarePages, deploy.ServiceFrontend, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := &deploy.Plan{
				ID:        "plan_test",
				ProjectID: "00000000-0000-0000-0000-000000000001",
				Services: []deploy.ServicePlan{
					{Name: "api", Type: tt.svcType, Target: tt.target, SourceDir: "backend/", Port: tt.port},
				},
			}

			err := deploy.Validate(plan)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "port")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidate_AutoInjectResourceReference tests that auto_inject references are validated
func TestValidate_AutoInjectResourceReference(t *testing.T) {
	tests := []struct {
		name      string
		resources []deploy.ResourcePlan
		envSource string
		wantErr   bool
	}{
		{
			name: "valid reference",
			resources: []deploy.ResourcePlan{
				{Name: "main-db", Type: deploy.ResourcePostgres, Provider: "neon"},
			},
			envSource: "provision_postgres", // hasMatchingResource checks TYPE, not name
			wantErr:   false,
		},
		{
			name:      "no matching resource",
			resources: []deploy.ResourcePlan{{Name: "main-db", Type: deploy.ResourcePostgres, Provider: "neon"}},
			envSource: "provision_redis",
			wantErr:   true,
		},
		{
			name:      "empty resources",
			resources: []deploy.ResourcePlan{},
			envSource: "provision_postgres",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := &deploy.Plan{
				ID:        "plan_test",
				ProjectID: "00000000-0000-0000-0000-000000000001",
				Services: []deploy.ServicePlan{
					{
						Name:      "api",
						Type:      deploy.ServiceBackend,
						Target:    deploy.TargetCloudRun,
						SourceDir: "backend/",
						Port:      8080,
						EnvVars: []deploy.EnvVar{
							{
								Key:            "DATABASE_URL",
								Classification: deploy.EnvAutoInject,
								Source:         tt.envSource,
							},
						},
					},
				},
				Resources: tt.resources,
			}

			err := deploy.Validate(plan)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "auto_inject")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ============================================================================
// BUSINESS LOGIC TESTS: Env Var Classification
// ============================================================================

// TestClassifyEnv_InjectFrom tests that inject_from takes precedence
func TestClassifyEnv_InjectFrom(t *testing.T) {
	availableResources := map[string]bool{"my-db": true}

	ev, err := deploy.ClassifyEnv("DATABASE_URL", "", "", "my-db", availableResources)
	require.NoError(t, err)
	assert.Equal(t, deploy.EnvAutoInject, ev.Classification)
	assert.Equal(t, "provision_my-db", ev.Source)
}

// TestClassifyEnv_InvalidInjectFrom tests that invalid inject_from is rejected
func TestClassifyEnv_InvalidInjectFrom(t *testing.T) {
	availableResources := map[string]bool{"my-db": true}

	_, err := deploy.ClassifyEnv("DATABASE_URL", "", "", "nonexistent-db", availableResources)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resource 'nonexistent-db' not found")
}

// TestClassifyEnv_BuildArgPrefixes tests deterministic build arg detection
func TestClassifyEnv_BuildArgPrefixes(t *testing.T) {
	tests := []struct {
		key     string
		isBuild bool
	}{
		{"VITE_API_URL", true},
		{"NEXT_PUBLIC_API_URL", true},
		{"REACT_APP_API_URL", true},
		{"DATABASE_URL", false},
		{"API_KEY", false},
		{"PUBLIC_PATH", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			ev, err := deploy.ClassifyEnv(tt.key, "", "", "", nil)
			require.NoError(t, err)
			if tt.isBuild {
				assert.Equal(t, deploy.EnvBuildArg, ev.Classification)
			} else {
				assert.NotEqual(t, deploy.EnvBuildArg, ev.Classification)
			}
		})
	}
}

// TestClassifyEnv_ProviderKeysNotAutoGenerated tests that provider keys are not auto-generated
func TestClassifyEnv_ProviderKeysNotAutoGenerated(t *testing.T) {
	providerKeys := []string{
		"STRIPE_SECRET_KEY",
		"NEON_API_KEY",
		"CLOUDFLARE_API_TOKEN",
		"AWS_SECRET_ACCESS_KEY",
		"OPENAI_API_KEY",
		"ANTHROPIC_API_KEY",
		"GITHUB_TOKEN",
		"SENDGRID_API_KEY",
		"TWILIO_AUTH_TOKEN",
	}

	for _, key := range providerKeys {
		t.Run(key, func(t *testing.T) {
			ev, err := deploy.ClassifyEnv(key, "", "", "", nil)
			require.NoError(t, err)
			assert.Equal(t, deploy.EnvUserRequired, ev.Classification,
				"Provider keys should not be auto-generated")
		})
	}
}

// ============================================================================
// SECURITY TESTS: Resource Exhaustion
// ============================================================================

// TestValidate_ExcessiveServiceCount tests that excessive services are rejected
func TestValidate_ExcessiveServiceCount(t *testing.T) {
	var services []deploy.ServicePlan
	for i := 0; i < 101; i++ {
		services = append(services, deploy.ServicePlan{
			Name:      fmt.Sprintf("svc-%d", i),
			Type:      deploy.ServiceBackend,
			Target:    deploy.TargetCloudRun,
			SourceDir: "backend/",
		})
	}

	plan := &deploy.Plan{
		ID:        "plan_test",
		ProjectID: "00000000-0000-0000-0000-000000000001",
		Services:  services,
	}

	err := deploy.Validate(plan)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too many")
}

// TestValidate_ExcessiveEnvVars tests that excessive env vars are rejected
func TestValidate_ExcessiveEnvVars(t *testing.T) {
	var envVars []deploy.EnvVar
	for i := 0; i < 101; i++ {
		envVars = append(envVars, deploy.EnvVar{
			Key:            fmt.Sprintf("VAR_%d", i),
			Classification: deploy.EnvUserRequired,
		})
	}

	plan := &deploy.Plan{
		ID:        "plan_test",
		ProjectID: "00000000-0000-0000-0000-000000000001",
		Services: []deploy.ServicePlan{
			{
				Name:      "api",
				Type:      deploy.ServiceBackend,
				Target:    deploy.TargetCloudRun,
				SourceDir: "backend/",
				EnvVars:   envVars,
			},
		},
	}

	err := deploy.Validate(plan)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too many")
}

// TestValidate_DuplicateResourceTypes tests that duplicate resource types are rejected
func TestValidate_DuplicateResourceTypes(t *testing.T) {
	plan := &deploy.Plan{
		ID:        "plan_test",
		ProjectID: "00000000-0000-0000-0000-000000000001",
		Services:  []deploy.ServicePlan{{Name: "api", Type: deploy.ServiceBackend, Target: deploy.TargetCloudRun, SourceDir: "backend/"}},
		Resources: []deploy.ResourcePlan{
			{Name: "db1", Type: deploy.ResourcePostgres, Provider: "neon"},
			{Name: "db2", Type: deploy.ResourcePostgres, Provider: "neon"}, // Duplicate type
		},
	}

	err := deploy.Validate(plan)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}
