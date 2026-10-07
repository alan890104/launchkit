package deploy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// validMinimalPlan returns a Plan that passes all validation rules.
// Tests mutate a copy to introduce exactly one defect at a time.
func validMinimalPlan() *Plan {
	return &Plan{
		ID:        "plan_abc123",
		ProjectID: "00000000-0000-0000-0000-000000000001",
		Cloud:     CloudGCP,
		Status:    PlanPending,
		Services: []ServicePlan{
			{
				Name:      "api",
				Type:      ServiceBackend,
				Target:    TargetCloudRun,
				SourceDir: "backend/",
				Port:      8080,
			},
		},
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name       string
		plan       *Plan
		wantErr    bool
		errSubstr  string
		errSubstrs []string // when multiple error substrings must all be present
	}{
		{
			name:    "valid minimal plan",
			plan:    validMinimalPlan(),
			wantErr: false,
		},
		{
			name: "missing plan ID",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.ID = ""
				return p
			}(),
			wantErr:   true,
			errSubstr: "plan ID is required",
		},
		{
			name: "missing project ID",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.ProjectID = ""
				return p
			}(),
			wantErr:   true,
			errSubstr: "project ID is required",
		},
		{
			name: "empty services",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services = nil
				return p
			}(),
			wantErr:   true,
			errSubstr: "at least one service",
		},
		{
			name: "duplicate service names",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services = append(p.Services, ServicePlan{
					Name:      "api",
					Type:      ServiceBackend,
					Target:    TargetCloudRun,
					SourceDir: "backend2/",
					Port:      8081,
				})
				return p
			}(),
			wantErr:   true,
			errSubstr: "duplicate",
		},
		{
			name: "missing service name",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].Name = ""
				return p
			}(),
			wantErr:   true,
			errSubstr: "name is required",
		},
		{
			name: "invalid service name with uppercase and space",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].Name = "My App"
				return p
			}(),
			wantErr:   true,
			errSubstr: "must match [a-z][a-z0-9-]*",
		},
		{
			name: "service name with shell injection chars",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].Name = "api;rm"
				return p
			}(),
			wantErr:   true,
			errSubstr: "must match [a-z][a-z0-9-]*",
		},
		{
			name: "frontend targeting cloud_run is rejected",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].Type = ServiceFrontend
				p.Services[0].Target = TargetCloudRun
				return p
			}(),
			wantErr:   true,
			errSubstr: "must target cloudflare_pages",
		},
		{
			name: "backend targeting cloudflare_pages is rejected",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].Type = ServiceBackend
				p.Services[0].Target = TargetCloudflarePages
				return p
			}(),
			wantErr:   true,
			errSubstr: "must target a compute platform",
		},
		{
			name: "compute service with port 0",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].Port = 0
				return p
			}(),
			wantErr:   true,
			errSubstr: "port must be",
		},
		{
			name: "compute service with port 99999",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].Port = 99999
				return p
			}(),
			wantErr:   true,
			errSubstr: "port must be",
		},
		{
			name: "source_dir with path traversal",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].SourceDir = "../etc"
				return p
			}(),
			wantErr:   true,
			errSubstr: "source_dir must be a relative path",
		},
		{
			name: "source_dir with absolute path",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].SourceDir = "/etc"
				return p
			}(),
			wantErr:   true,
			errSubstr: "source_dir must be a relative path",
		},
		{
			name: "valid env var key",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].EnvVars = []EnvVar{
					{Key: "VALID_KEY", Classification: EnvUserRequired},
				}
				return p
			}(),
			wantErr: false,
		},
		{
			name: "env var key with space is rejected",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].EnvVars = []EnvVar{
					{Key: "invalid key", Classification: EnvUserRequired},
				}
				return p
			}(),
			wantErr:   true,
			errSubstr: "must match",
		},
		{
			name: "env var key with semicolon is rejected",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].EnvVars = []EnvVar{
					{Key: "key;inject", Classification: EnvUserRequired},
				}
				return p
			}(),
			wantErr:   true,
			errSubstr: "must match",
		},
		{
			name: "build arg key starting with digit is rejected",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].BuildArgs = []EnvVar{
					{Key: "123INVALID", Classification: EnvBuildArg},
				}
				return p
			}(),
			wantErr:   true,
			errSubstr: "must match",
		},
		{
			name: "auto_inject env var without matching resource",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].EnvVars = []EnvVar{
					{
						Key:            "DATABASE_URL",
						Classification: EnvAutoInject,
						Source:         "provision_postgres",
					},
				}
				// No resources → no matching resource for provision_postgres
				return p
			}(),
			wantErr:   true,
			errSubstr: "no matching resource",
		},
		{
			name: "duplicate resource types",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Resources = []ResourcePlan{
					{Name: "db1", Type: ResourcePostgres, Provider: "neon", Region: "us-east4"},
					{Name: "db2", Type: ResourcePostgres, Provider: "neon", Region: "us-east4"},
				}
				return p
			}(),
			wantErr:   true,
			errSubstr: "duplicate resource type",
		},
		{
			name: "missing service type",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].Type = ""
				return p
			}(),
			wantErr:   true,
			errSubstr: "type is required",
		},
		{
			name: "missing service target",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].Target = ""
				return p
			}(),
			wantErr:   true,
			errSubstr: "target is required",
		},
		{
			name: "missing service source_dir",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].SourceDir = ""
				return p
			}(),
			wantErr:   true,
			errSubstr: "source_dir is required",
		},
		{
			name: "missing resource type",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Resources = []ResourcePlan{
					{Name: "db", Type: "", Provider: "neon", Region: "us-east4"},
				}
				return p
			}(),
			wantErr:   true,
			errSubstr: "type is required",
		},
		{
			name: "missing resource provider",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Resources = []ResourcePlan{
					{Name: "db", Type: ResourcePostgres, Provider: "", Region: "us-east4"},
				}
				return p
			}(),
			wantErr:   true,
			errSubstr: "provider is required",
		},
		{
			name: "multi-error accumulation",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.ID = ""
				p.ProjectID = ""
				return p
			}(),
			wantErr:    true,
			errSubstrs: []string{"plan ID", "project ID"},
		},
		{
			name: "worker targeting cloudflare_pages",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].Type = ServiceWorker
				p.Services[0].Target = TargetCloudflarePages
				return p
			}(),
			wantErr:   true,
			errSubstr: "must target a compute platform",
		},
		{
			name: "valid frontend on cloudflare_pages",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].Type = ServiceFrontend
				p.Services[0].Target = TargetCloudflarePages
				p.Services[0].SourceDir = "frontend/"
				p.Services[0].Port = 0
				return p
			}(),
			wantErr: false,
		},
		{
			name: "valid auto_inject with matching resource",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].EnvVars = []EnvVar{
					{
						Key:            "DATABASE_URL",
						Classification: EnvAutoInject,
						Source:         "provision_postgres",
					},
				}
				p.Resources = []ResourcePlan{
					{Name: "db", Type: ResourcePostgres, Provider: "neon", Region: "us-east4"},
				}
				return p
			}(),
			wantErr: false,
		},
		{
			name: "service name with hyphen is valid",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].Name = "my-api"
				return p
			}(),
			wantErr: false,
		},
		{
			name: "env key with uppercase and semicolon is rejected",
			plan: func() *Plan {
				p := validMinimalPlan()
				p.Services[0].EnvVars = []EnvVar{
					{Key: "INJECT;BAD", Classification: EnvUserRequired},
				}
				return p
			}(),
			wantErr:   true,
			errSubstr: "must match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.plan)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errSubstr != "" {
					assert.ErrorContains(t, err, tt.errSubstr)
				}
				for _, sub := range tt.errSubstrs {
					assert.ErrorContains(t, err, sub)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
