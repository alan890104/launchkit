package deploy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSelectTarget(t *testing.T) {
	tests := []struct {
		name        string
		serviceType string
		cloud       Cloud
		want        ServiceTarget
	}{
		{
			name:        "frontend on GCP targets cloudflare_pages",
			serviceType: "frontend",
			cloud:       CloudGCP,
			want:        TargetCloudflarePages,
		},
		{
			name:        "frontend on AWS targets cloudflare_pages",
			serviceType: "frontend",
			cloud:       CloudAWS,
			want:        TargetCloudflarePages,
		},
		{
			name:        "backend on GCP targets cloud_run",
			serviceType: "backend",
			cloud:       CloudGCP,
			want:        TargetCloudRun,
		},
		{
			name:        "backend on AWS targets ecs_fargate",
			serviceType: "backend",
			cloud:       CloudAWS,
			want:        TargetECSFargate,
		},
		{
			name:        "worker on GCP targets cloud_run",
			serviceType: "worker",
			cloud:       CloudGCP,
			want:        TargetCloudRun,
		},
		{
			name:        "worker on AWS targets ecs_fargate",
			serviceType: "worker",
			cloud:       CloudAWS,
			want:        TargetECSFargate,
		},
		{
			name:        "empty service type on GCP defaults to cloud_run",
			serviceType: "",
			cloud:       CloudGCP,
			want:        TargetCloudRun,
		},
		{
			name:        "case-sensitive Frontend on GCP defaults to cloud_run",
			serviceType: "Frontend",
			cloud:       CloudGCP,
			want:        TargetCloudRun,
		},
		{
			name:        "backend on unknown cloud defaults to cloud_run",
			serviceType: "backend",
			cloud:       Cloud("azure"),
			want:        TargetCloudRun,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectTarget(tt.serviceType, tt.cloud)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDefaultProvider(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		wantProvider string
		wantErr      bool
	}{
		{
			name:         "postgres defaults to neon",
			resourceType: "postgres",
			wantProvider: "neon",
			wantErr:      false,
		},
		{
			name:         "redis defaults to upstash",
			resourceType: "redis",
			wantProvider: "upstash",
			wantErr:      false,
		},
		{
			name:         "unknown resource type returns error",
			resourceType: "unknown",
			wantProvider: "",
			wantErr:      true,
		},
		{
			name:         "empty resource type returns error",
			resourceType: "",
			wantProvider: "",
			wantErr:      true,
		},
		{
			name:         "storage defaults to gcs",
			resourceType: "storage",
			wantProvider: "gcs",
			wantErr:      false,
		},
		{
			name:         "gcs defaults to gcs",
			resourceType: "gcs",
			wantProvider: "gcs",
			wantErr:      false,
		},
		{
			name:         "s3 defaults to s3",
			resourceType: "s3",
			wantProvider: "s3",
			wantErr:      false,
		},
		{
			name:         "case-sensitive Postgres returns error",
			resourceType: "Postgres",
			wantProvider: "",
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := defaultProvider(tt.resourceType)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Empty(t, provider)
				// Error message should contain the input string
				assert.Contains(t, err.Error(), tt.resourceType)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantProvider, provider)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestIsComputeTarget
// ---------------------------------------------------------------------------

func TestIsComputeTarget(t *testing.T) {
	tests := []struct {
		name   string
		target ServiceTarget
		want   bool
	}{
		{
			name:   "CloudRun is compute",
			target: TargetCloudRun,
			want:   true,
		},
		{
			name:   "ECSFargate is compute",
			target: TargetECSFargate,
			want:   true,
		},
		{
			name:   "CloudflarePages is not compute",
			target: TargetCloudflarePages,
			want:   false,
		},
		{
			name:   "unknown target is not compute",
			target: ServiceTarget("unknown"),
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsComputeTarget(tt.target))
		})
	}
}
