package tools

import (
	"testing"

	"github.com/alan890104/launchkit/server/internal/deploy"
)

func TestCloudServiceName_UsesEnvironmentScope(t *testing.T) {
	projectID := "project-123"
	name := "api"
	envID1 := "env-prod"
	envID2 := "env-staging"

	got1 := cloudServiceName(projectID, envID1, name)
	got2 := cloudServiceName(projectID, envID2, name)

	if got1 == got2 {
		t.Fatalf("cloudServiceName(%q, %q, %q) = %q, cloudServiceName(%q, %q, %q) = %q; want distinct names for distinct envs",
			projectID, envID1, name, got1,
			projectID, envID2, name, got2,
		)
	}
}

func TestCloudServiceName_MatchesDeployScoping(t *testing.T) {
	projectID := "project-123"
	name := "api"
	envID := "env-prod"

	if got, want := cloudServiceName(projectID, "", name), deploy.ScopeServiceNameWithEnv(projectID, "", name); got != want {
		t.Fatalf("cloudServiceName legacy path = %q, want %q", got, want)
	}

	if got, want := cloudServiceName(projectID, envID, name), deploy.ScopeServiceNameWithEnv(projectID, envID, name); got != want {
		t.Fatalf("cloudServiceName env-aware path = %q, want %q", got, want)
	}
}
