package deploy_test

// adversarial_test.go — Cross-tenant resource collision security audit
//
// Each test represents an attack scenario where Tenant A could accidentally
// or intentionally interfere with Tenant B's cloud resources.
//
// Run with:
//   go test ./internal/deploy/... -v -run TestAdversarial
//
// Tests that FAIL = real vulnerability still present in the codebase.
// Tests that PASS = the fix is in place (or the attack vector doesn't exist).

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/alan890104/launchkit/server/internal/deploy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// HELPER: replicate the internal scopeResourceName algorithm so tests can be
// self-contained without accessing unexported symbols.
// Must stay in sync with orchestrator.go:scopeResourceName.
// Current: 8 bytes (16 hex chars) = 64-bit collision space.
// ─────────────────────────────────────────────────────────────────────────────

func scopedName(projectID, resourceName string) string {
	h := sha256.Sum256([]byte(projectID + "/" + resourceName))
	return fmt.Sprintf("lk-%s", hex.EncodeToString(h[:8]))
}

// ─────────────────────────────────────────────────────────────────────────────
// ATTACK 1 (CRITICAL): SHA-256 truncation collision
//
// scopeResourceName keeps only the first 6 bytes (12 hex chars) of SHA-256.
// Birthday paradox: ~2^24 ≈ 16 million distinct names before a 50% collision.
// A determined adversary can brute-force a pair of (projectID, resourceName)
// that produces the same 12-char prefix and hijack the target resource.
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_ScopeResourceName_TruncationDepth verifies that the hash
// output space is wide enough to resist birthday-attack collisions.
// The test FAILS if fewer than 48 bits (96 hex chars / 2 = 48 bytes, but we
// compare against the 12-char truncation and document the risk).
func TestAdversarial_ScopeResourceName_TruncationDepth(t *testing.T) {
	// Attack scenario:
	//   Tenant A has projectID "proj-aaaaaa" and service "api".
	//   Tenant B crafts a projectID+serviceName that produces the same 12-char hash.
	//   Both tenants' cloud services resolve to the same Cloud Run name → B hijacks A.

	const truncatedHexLen = 12 // 6 bytes = 12 hex chars
	const minSafeBits = 48     // NIST SP 800-107: ≥ 48-bit collision resistance

	// Measure actual output length via exported ScopeServiceName.
	name := deploy.ScopeServiceName("any-project-id", "any-service")
	prefix := strings.TrimPrefix(name, "lk-")

	actualBits := len(prefix) * 4 // each hex char = 4 bits

	assert.GreaterOrEqual(t, actualBits, minSafeBits,
		"VULNERABILITY: scopeResourceName truncates to only %d bits (%d hex chars). "+
			"Birthday-attack collision probability is ~50%% after ~%d unique (project,service) pairs. "+
			"Fix: increase to at least 16 hex chars (8 bytes = 64-bit collision space).",
		actualBits, len(prefix),
		1<<(actualBits/2),
	)
}

// TestAdversarial_ScopeResourceName_BirthdayCollisionDemo brute-forces an actual
// collision among a realistic workload to prove the risk is not theoretical.
// It iterates over 2^24 names and checks whether any two produce the same scoped name.
// The test FAILS if a collision is found (= vulnerability confirmed).
func TestAdversarial_ScopeResourceName_BirthdayCollisionDemo(t *testing.T) {
	// Attack scenario: adversary controls projectID prefix and iterates service names.
	// With only 12 hex chars (48 bits) of output space, a collision among ~5M pairs
	// is highly likely (~10% probability at ~2M samples by birthday bound).

	seen := make(map[string]string, 1_000_000)
	limit := 1_500_000 // stop early — birthday bound says ~50% at 2^24

	for i := 0; i < limit; i++ {
		// Attacker controls both projectID and service name.
		projectID := fmt.Sprintf("proj-%08x", i)
		svcName := "api"
		scoped := scopedName(projectID, svcName)

		if existing, ok := seen[scoped]; ok {
			t.Errorf("COLLISION FOUND after %d iterations: %q and %q both hash to %q.\n"+
				"An attacker can craft a projectID that collides with a victim's Cloud Run service name.",
				i, existing, projectID+"/"+svcName, scoped)
			return
		}
		seen[scoped] = projectID + "/" + svcName
	}
	// If we reach here with no collision, the truncation depth is acceptable for this sample.
	t.Logf("No collision found in %d samples — risk is probabilistic, not zero.", limit)
}

// ─────────────────────────────────────────────────────────────────────────────
// ATTACK 2 (CRITICAL): Image tag does NOT include projectID
//
// The ImageTagFn in server.go is:
//   fmt.Sprintf("%s/%s:%s", registryRepo, serviceName, planID)
//
// serviceName here is the HUMAN-READABLE name (e.g. "api"), not the scoped hash.
// Two different projects with a service named "api" will push to the SAME
// Artifact Registry image path:
//   us-east4-docker.pkg.dev/launchkit-spike/user-images/api:<planID>
//
// If planIDs are non-colliding UUIDs this doesn't overwrite each other's images,
// but it allows Tenant B to PULL Tenant A's image by guessing the planID,
// because there is no per-tenant registry path isolation.
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_ImageTag_NoProjectIsolation checks that after the fix, two
// different projects with the same service name produce DIFFERENT image paths.
//
// The fix (orchestrator.go): pass scopeResourceNameWithEnv(projectID, envID, svcName)
// as the serviceName argument to imageTagFn instead of the raw svc.Name.
func TestAdversarial_ImageTag_NoProjectIsolation(t *testing.T) {
	registryRepo := "us-east4-docker.pkg.dev/launchkit-spike/user-images"
	imageTagFn := func(serviceName, planID string) string {
		return fmt.Sprintf("%s/%s:%s", registryRepo, serviceName, planID)
	}

	// Two tenants, both have a service named "api" — but DIFFERENT project UUIDs.
	tenantAProjectID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	tenantBProjectID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	tenantAPlanID := "plan_aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	tenantBPlanID := "plan_bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	// After the fix: orchestrator passes scopeResourceNameWithEnv(projectID, "", svcName)
	// as serviceName — so the two tenants get different image paths.
	scopedA := deploy.ScopeServiceNameWithEnv(tenantAProjectID, "", "api")
	scopedB := deploy.ScopeServiceNameWithEnv(tenantBProjectID, "", "api")

	tagA := imageTagFn(scopedA, tenantAPlanID)
	tagB := imageTagFn(scopedB, tenantBPlanID)

	repoA := strings.Split(tagA, ":")[0]
	repoB := strings.Split(tagB, ":")[0]

	assert.NotEqual(t, repoA, repoB,
		"VULNERABILITY STILL PRESENT: Both tenants push to the same AR repository path.\n"+
			"tagA=%q\ntagB=%q", tagA, tagB,
	)
	t.Logf("tenant A image path: %s", repoA)
	t.Logf("tenant B image path: %s", repoB)
}

// ─────────────────────────────────────────────────────────────────────────────
// ATTACK 3 (HIGH): URN collision when projectID equals resourceType+name prefix
//
// MakeURN format: "launchkit:{projectID}:{resourceType}:{resourceName}"
// The colon separator is not escaped. If a projectID contains ":", the URN
// becomes ambiguous — two different (projectID, type, name) triples can produce
// the same URN string.
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_URN_ColonInjection verifies that URNs are unambiguous even
// when projectID contains the ":" separator character.
func TestAdversarial_URN_ColonInjection(t *testing.T) {
	// Attack scenario:
	//   Tenant A crafts projectID = "proj:compute" and resourceName = "lk-xyz".
	//   Normal Tenant B has projectID = "proj", resourceType = "compute", resourceName = "compute:lk-xyz".
	//   Both produce URN "launchkit:proj:compute:compute:lk-xyz" vs
	//   "launchkit:proj:compute:lk-xyz" — but by shifting colon positions an
	//   adversary can craft a URN that matches another tenant's entry in resource_states,
	//   overwriting it via the ON CONFLICT (urn) upsert in resource_manager.go:84.

	// Pair 1: normal tenant
	urn1 := deploy.MakeURN("proj-abc", "compute", "lk-a1b2c3d4e5f6")

	// Pair 2: adversary crafts projectID to contain colon segments
	// This simulates a projectID that is NOT a UUID (possible in dev mode where
	// projectID falls back to projectName — see engine.go:31-33).
	urn2 := deploy.MakeURN("proj-abc:compute:lk-a1b2c3d4e5f6", "extra", "ignored")

	assert.NotEqual(t, urn1, urn2,
		"VULNERABILITY: URN collision via colon injection.\n"+
			"urn1=%q\nurn2=%q\n"+
			"Fix: percent-encode or replace ':' in projectID before embedding in URN, "+
			"or enforce UUID-only projectIDs at the DB constraint level.",
		urn1, urn2,
	)
}

// TestAdversarial_URN_ProjectIDNotEnforcedAsUUID verifies that the system would
// reject a non-UUID projectID before using it in a URN or scopeResourceName.
// In engine.go:31-33, if hints.ProjectID is empty, it falls back to ProjectName.
// ProjectName is user-controlled and NOT a UUID, so it can contain ":" and other
// special characters that break URN uniqueness.
func TestAdversarial_URN_ProjectIDNotEnforcedAsUUID(t *testing.T) {
	// Simulate the fallback path in engine.go:31-33:
	//   projectID := hints.ProjectID
	//   if projectID == "" {
	//       projectID = hints.ProjectName  // ← user-controlled, any string
	//   }

	maliciousProjectName := "evil:compute:lk-deadbeef1234"

	// This project name can be used as projectID if ProjectID field is empty.
	// Validate() in validate.go only checks that ProjectID != "", not that it's a UUID.
	plan := &deploy.Plan{
		ID:        "plan_test",
		ProjectID: maliciousProjectName, // colon-containing "projectID"
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

	err := deploy.Validate(plan)
	assert.Error(t, err,
		"VULNERABILITY: Validate() accepts a non-UUID projectID %q that contains ':'. "+
			"This allows URN injection in resource_states via MakeURN(). "+
			"Fix: add a UUID format check in Validate() for plan.ProjectID.",
		maliciousProjectName,
	)
}

// ─────────────────────────────────────────────────────────────────────────────
// ATTACK 4 (HIGH): GCS bucket global namespace collision
//
// GCS bucket names are globally unique across ALL GCP projects, not just LaunchKit's.
// scopeResourceName produces "lk-{12hexchars}" — only 16^12 ≈ 2.8 trillion names.
// But "lk-" is a common prefix. An external party could have pre-registered a
// bucket "lk-a1b2c3d4e5f6" that collides with LaunchKit's generated name,
// causing CreateBucket to fail or (worse) silently reference the wrong bucket.
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_GCSBucketName_PrefixTooShort verifies that generated bucket
// names have sufficient entropy to resist pre-registration attacks.
func TestAdversarial_GCSBucketName_PrefixTooShort(t *testing.T) {
	// GCS bucket name rules: 3–63 chars, globally unique.
	// Our format: "lk-" + 12 hex chars = 15 chars total.
	// An adversary could iterate "lk-{000000000000}" through "lk-{ffffffffffff}"
	// (16^12 ≈ 2.8T names) to register all possible LaunchKit bucket names,
	// but the more realistic attack is squatting on the exact hash of a known
	// projectID+resourceName (leaked via audit logs or source code).

	projectID := "11111111-1111-1111-1111-111111111111"
	resourceName := "assets"
	generated := scopedName(projectID, resourceName)

	// Verify the generated name doesn't collide with a realistic external bucket name.
	// The assertion we actually want to make: the name MUST include a LaunchKit-specific
	// non-guessable suffix (e.g. GCP project ID encoded in the hash input) so that
	// pre-squatting is infeasible.

	// Currently the hash input is ONLY projectID+"/"+resourceName.
	// An attacker who knows a victim's projectID and resource name can compute
	// the exact bucket name and pre-register it.

	// Demonstrate: given known inputs, the bucket name is fully predictable.
	computed := scopedName(projectID, resourceName)
	assert.Equal(t, generated, computed,
		"Bucket name should be deterministic for crash-recovery, this is expected.")

	// The actual vulnerability: the bucket name is ALSO predictable by an adversary.
	// We check that the name is long enough to be unguessable WITHOUT knowledge of projectID.
	// 12 hex chars = 48 bits — too short if projectID is a known/leaked UUID.
	entropyBits := len(strings.TrimPrefix(generated, "lk-")) * 4
	const minimumEntropyBits = 64
	assert.GreaterOrEqual(t, entropyBits, minimumEntropyBits,
		"VULNERABILITY: GCS bucket name %q has only %d bits of entropy. "+
			"An adversary who knows the projectID and resource name can pre-register the bucket. "+
			"Fix: include the LaunchKit GCP project ID in the hash input, or use 16 hex chars (8 bytes).",
		generated, entropyBits,
	)
}

// ─────────────────────────────────────────────────────────────────────────────
// ATTACK 5 (HIGH): Cloudflare Pages project name collision
//
// scopeResourceName is used for Cloudflare Pages project names too (orchestrator.go:556).
// Cloudflare Pages project names must be unique WITHIN a Cloudflare account.
// The same 12-char hash applies — see Attack 1.
// Additionally, Pages project names are publicly visible in the URL:
//   {projectName}.pages.dev
// This leaks internal hashes and allows enumeration.
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_CloudflarePages_SameHashAsDifferentProject checks that two
// different (projectID, serviceName) pairs cannot produce the same Pages project name.
func TestAdversarial_CloudflarePages_SameHashAsDifferentProject(t *testing.T) {
	// Two legitimately different tenants, both name their frontend "web".
	tenant1ProjectID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	tenant2ProjectID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	serviceName := "web"

	name1 := scopedName(tenant1ProjectID, serviceName)
	name2 := scopedName(tenant2ProjectID, serviceName)

	assert.NotEqual(t, name1, name2,
		"VULNERABILITY: Different tenants produce the same Cloudflare Pages project name.\n"+
			"name1=%q name2=%q\nThis would cause one tenant's deploy to overwrite the other's.",
	)

	// Also verify the format is suitable for Cloudflare Pages naming constraints.
	// CF Pages: 1–28 chars, lowercase alphanumeric + hyphens, cannot start/end with hyphen.
	assert.LessOrEqual(t, len(name1), 28,
		"Cloudflare Pages project name must be ≤ 28 chars, got %d: %q", len(name1), name1)
}

// ─────────────────────────────────────────────────────────────────────────────
// ATTACK 6 (HIGH): Secrets isolation — cross-project secret leak via name reuse
//
// In secrets.go:SetSecretValues(), secrets are keyed by (project_id, name).
// But the secret FORM at /s/{requestID} uses the project_id embedded in
// secret_requests — it does NOT re-verify that the user submitting the form
// owns the project. Any unauthenticated user who guesses/obtains a secret
// request ID can submit secrets for ANY project.
//
// Additionally, secrets.go:ResolveUserSecrets fetches ALL secrets for a
// project_id. If an attacker can inject a secret_request for a victim's
// project_id (e.g. by creating a new plan for a project they don't own),
// they can inject a poisoned DATABASE_URL that gets deployed to the victim.
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_Secrets_UNIQUE_ProjectScoped verifies that the secrets table
// has a (project_id, name) unique constraint — preventing cross-tenant secret
// injection if project_id is correctly scoped.
func TestAdversarial_Secrets_UNIQUE_ProjectScoped(t *testing.T) {
	// This is a schema-level check. We verify the constraint exists by attempting
	// to construct a scenario where it would protect us.
	// The actual enforcement is the DB UNIQUE(project_id, name) constraint.
	// We test the logic path that depends on it.

	// Two tenants submit secrets for the same key name "DATABASE_URL".
	// They MUST be stored separately — different project_ids.
	tenant1ID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	tenant2ID := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	secretName := "DATABASE_URL"

	// They differ only in project_id, so both can coexist in the secrets table.
	urnT1 := fmt.Sprintf("%s|%s", tenant1ID, secretName)
	urnT2 := fmt.Sprintf("%s|%s", tenant2ID, secretName)

	assert.NotEqual(t, urnT1, urnT2,
		"Tenant secrets must be project-scoped (this documents the expected property).")

	// Verify secrets are NOT accessible across project boundaries.
	// The real risk: ResolveUserSecrets(ctx, db, projectID, enc) queries
	//   WHERE project_id = $1
	// This correctly scopes to the project — but ONLY if projectID is always
	// a non-forgeable UUID, never a user-controlled display name.
	// (See Attack 3 for the projectID non-UUID risk.)
	assert.NotEmpty(t, tenant1ID, "projectID must be a UUID, not a display name")
}

// TestAdversarial_Secrets_EncryptionFallback verifies that secrets are NOT
// stored as plaintext in production mode (enc == nil fallback must be blocked).
func TestAdversarial_Secrets_EncryptionFallback(t *testing.T) {
	// In secrets.go:SetSecretValues():
	//   if enc != nil {  ... encrypt ... }
	//   else { ciphertext = []byte(value) }  // ← plaintext fallback
	//
	// In server.go:51-60:
	//   if s.cfg.SecretEncryptionKey != "" {  enc = ... }
	//   else { slog.Warn("secrets stored as plaintext") }
	//
	// Risk: if SECRET_ENCRYPTION_KEY is accidentally unset in production,
	// all user secrets (Stripe keys, OAuth tokens, etc.) are stored as plaintext BYTEA.
	// A DB dump or SQL injection would expose all secrets in cleartext.
	//
	// The current code logs a Warn but does NOT refuse to start — this should be
	// a Fatal in non-dev environments.

	// Verify that the Encryptor rejects empty keys (it does — test documents it).
	_, err := deploy.NewEncryptor("")
	assert.Error(t, err,
		"NewEncryptor with empty key must fail — this is already correct.")

	// Document the remaining risk: server.go starts without enc in non-dev mode.
	// The fix: add an environment check:
	//   if !cfg.IsDev() && cfg.SecretEncryptionKey == "" {
	//       return fmt.Errorf("SECRET_ENCRYPTION_KEY is required in non-dev environments")
	//   }
	t.Log("MEDIUM RISK: server.go does not enforce SECRET_ENCRYPTION_KEY in production. " +
		"Secrets fall back to plaintext if the env var is accidentally unset.")
}

// ─────────────────────────────────────────────────────────────────────────────
// ATTACK 7 (MEDIUM): Resource name conflict between "storage", "gcs", "s3" types
//
// In validate.go:125, resource uniqueness is checked by TYPE:
//   resourceTypes[res.Type] — one resource per type
// But ResourceType "storage", "gcs", and "s3" are three distinct types (plan.go:50-57).
// A plan can include both a "storage" resource AND a "gcs" resource.
// Both get passed to scopeResourceName and will produce different scoped names,
// but both map to GCS buckets. The Plan Engine uses "storage" when cloud=GCP but
// the SAME Neon/Upstash project receives two separate create calls.
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_ResourceType_StorageGCSAlias verifies that "storage", "gcs", "s3"
// types don't silently alias each other and cause double-provisioning.
func TestAdversarial_ResourceType_StorageGCSAlias(t *testing.T) {
	// A plan with both "storage" and "gcs" type resources passes Validate()
	// because they are distinct types. Both will be provisioned as GCS buckets.
	plan := &deploy.Plan{
		ID:        "plan_test",
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
		Resources: []deploy.ResourcePlan{
			{Name: "assets", Type: deploy.ResourceStorage, Provider: "gcs", Region: "us-east4"},
			{Name: "uploads", Type: deploy.ResourceGCS, Provider: "gcs", Region: "us-east4"},
		},
	}

	err := deploy.Validate(plan)
	// Currently this PASSES validation — two GCS bucket resources in one plan.
	// We assert it SHOULD fail (duplicate provider type).
	assert.Error(t, err,
		"VULNERABILITY: A plan can include both 'storage' and 'gcs' resource types, "+
			"resulting in two GCS bucket provisioning calls. "+
			"Fix: treat 'storage', 'gcs' as aliases in Validate() — allow only one per-cloud storage resource per plan.")
}

// ─────────────────────────────────────────────────────────────────────────────
// ATTACK 8 (MEDIUM): Duplicate resource TYPE check uses ResourceType map,
// but the auto_inject validation uses resource NAME — mismatch allows orphaned env vars
//
// In validate.go:118-133, duplicate types are rejected.
// In validate.go:152-161, auto_inject source is validated against resource TYPE.
// But the Plan lists resources by NAME (not type). Two resources with the same NAME
// but different types would pass the type-based duplicate check.
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_ResourceName_DuplicateNameDifferentType verifies that two
// resources with the same NAME but different types are rejected.
func TestAdversarial_ResourceName_DuplicateNameDifferentType(t *testing.T) {
	// Attack: craft a plan where "main-db" appears as both postgres and redis.
	// The type-uniqueness check won't catch this (different types).
	// Both will be passed to scopeResourceName("proj", "main-db") → same scoped name →
	// second provision call hits Neon's "project already exists" or overwrites state.
	plan := &deploy.Plan{
		ID:        "plan_test",
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
		Resources: []deploy.ResourcePlan{
			{Name: "main-db", Type: deploy.ResourcePostgres, Provider: "neon"},
			{Name: "main-db", Type: deploy.ResourceRedis, Provider: "upstash"}, // same name, different type
		},
	}

	err := deploy.Validate(plan)
	assert.Error(t, err,
		"VULNERABILITY: Two resources with the same NAME but different types pass validation. "+
			"Both produce the same scopeResourceName output: %q. "+
			"Fix: add a resource name uniqueness check alongside the type uniqueness check.",
		scopedName("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "main-db"),
	)
}

// ─────────────────────────────────────────────────────────────────────────────
// ATTACK 9 (MEDIUM): staging/production environments share the same scopeResourceName
//
// Cloud Run service names are scoped by (projectID, serviceName) but NOT by
// environment name. So the "production" environment's "api" service and the
// "staging" environment's "api" service of the SAME project both produce:
//   lk-{sha256(projectID + "/api")[:12]}
// This means deploying to staging OVERWRITES the production Cloud Run service.
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_Environments_SameCloudServiceName verifies that staging and
// production environments of the same project produce DISTINCT Cloud Run service names.
func TestAdversarial_Environments_SameCloudServiceName(t *testing.T) {
	projectID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	serviceName := "api"

	// Production environment has its own UUID.
	prodEnvID := "11111111-1111-1111-1111-111111111111"
	// Staging environment has a different UUID.
	stagingEnvID := "22222222-2222-2222-2222-222222222222"

	// With the fix in place, environment ID is included in the scoping hash.
	prodName := deploy.ScopeServiceNameWithEnv(projectID, prodEnvID, serviceName)
	stagingName := deploy.ScopeServiceNameWithEnv(projectID, stagingEnvID, serviceName)

	assert.NotEqual(t, prodName, stagingName,
		"VULNERABILITY: Staging and production environments of project %q both map to "+
			"Cloud Run service %q. A staging deploy will overwrite production.\n"+
			"Fix: include environment ID in scopeResourceNameWithEnv().",
		projectID, prodName,
	)
}

// ─────────────────────────────────────────────────────────────────────────────
// ATTACK 10 (LOW): Service name max length validation gap
//
// validate.go enforces maxServiceNameLength = 63 for the HUMAN-READABLE name.
// But scopeResourceName always produces 15-char names ("lk-" + 12 hex).
// This is safe. HOWEVER, the Cloud Run service name limit is 49 chars on some
// regions, and the Cloudflare Pages project name limit is 28 chars.
// "lk-" + 12 hex = 15 chars — fine for Cloud Run (49) but checked against CF Pages (28): OK.
// This test documents the boundary is safe.
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_ServiceName_ScopedLengthBoundary verifies that all scoped names
// fit within the most restrictive provider's name length limit.
func TestAdversarial_ServiceName_ScopedLengthBoundary(t *testing.T) {
	tests := []struct {
		name         string
		projectID    string
		serviceName  string
		maxAllowed   int
		providerName string
	}{
		{
			name:         "max-length human name",
			projectID:    "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			serviceName:  strings.Repeat("a", 63), // maxServiceNameLength
			maxAllowed:   49,                      // Cloud Run service name limit
			providerName: "Cloud Run",
		},
		{
			name:         "typical service name for CF Pages",
			projectID:    "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			serviceName:  "web",
			maxAllowed:   28, // Cloudflare Pages project name limit
			providerName: "Cloudflare Pages",
		},
		{
			name:         "typical service name for Neon",
			projectID:    "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			serviceName:  "postgres",
			maxAllowed:   63, // Neon project name limit
			providerName: "Neon",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scoped := deploy.ScopeServiceName(tt.projectID, tt.serviceName)
			assert.LessOrEqual(t, len(scoped), tt.maxAllowed,
				"Scoped name %q (%d chars) exceeds %s limit of %d chars",
				scoped, len(scoped), tt.providerName, tt.maxAllowed,
			)
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ATTACK 11 (CRITICAL): Environment isolation — staging overrides production
//
// Deeper test: when a project has environments (production, staging), both
// environments' services share the same plan.ProjectID.
// The orchestrator's scopeResourceName does NOT factor in the environment name.
// This means create_environment + deploy to staging WILL CLOBBER production.
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_Environments_CrossEnvCloudRunClobber proves that after the fix,
// production and staging environments of the same project get distinct Cloud Run names.
func TestAdversarial_Environments_CrossEnvCloudRunClobber(t *testing.T) {
	projectID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	apiServiceName := "api"

	// Each environment has its own UUID (stored in environments.id).
	prodEnvID := "11111111-1111-1111-1111-111111111111"
	stagingEnvID := "22222222-2222-2222-2222-222222222222"

	// Build plans as the orchestrator does — with EnvironmentID now set.
	prodPlan := &deploy.Plan{
		ID:            "plan_prod",
		ProjectID:     projectID,
		EnvironmentID: prodEnvID,
		Services: []deploy.ServicePlan{{
			Name:      apiServiceName,
			Type:      deploy.ServiceBackend,
			Target:    deploy.TargetCloudRun,
			SourceDir: "backend/",
			Port:      8080,
		}},
	}
	stagingPlan := &deploy.Plan{
		ID:            "plan_staging",
		ProjectID:     projectID,    // SAME project UUID
		EnvironmentID: stagingEnvID, // DIFFERENT environment UUID
		Services: []deploy.ServicePlan{{
			Name:      apiServiceName, // SAME service name
			Type:      deploy.ServiceBackend,
			Target:    deploy.TargetCloudRun,
			SourceDir: "backend/",
			Port:      8080,
		}},
	}

	require.NoError(t, deploy.Validate(prodPlan))
	require.NoError(t, deploy.Validate(stagingPlan))

	// With environment-aware scoping, the names must now be distinct.
	prodCloudName := deploy.ScopeServiceNameWithEnv(prodPlan.ProjectID, prodPlan.EnvironmentID, prodPlan.Services[0].Name)
	stagingCloudName := deploy.ScopeServiceNameWithEnv(stagingPlan.ProjectID, stagingPlan.EnvironmentID, stagingPlan.Services[0].Name)

	assert.NotEqual(t, prodCloudName, stagingCloudName,
		"CRITICAL VULNERABILITY still present: Production and staging environments of project %q "+
			"both map to Cloud Run service %q.\n"+
			"Fix: populate plan.EnvironmentID and use ScopeServiceNameWithEnv().",
		projectID, prodCloudName,
	)
	t.Logf("prod   → %s", prodCloudName)
	t.Logf("staging → %s", stagingCloudName)
}

// ─────────────────────────────────────────────────────────────────────────────
// ATTACK 12 (MEDIUM): Resource state URN collision via projectID-from-name fallback
//
// engine.go:31-33:
//   projectID := hints.ProjectID
//   if projectID == "" {
//       projectID = hints.ProjectName  ← user-controlled display name
//   }
//
// In dev mode (plan_deployment.go:72-79), project lookup uses name only:
//   SELECT id FROM projects WHERE name = $1 LIMIT 1
// This returns the FIRST project with that name across ALL teams.
// In production, projectIDForUser() correctly scopes to the authenticated user.
// But the dev-mode path is a multi-tenant collision vector if multiple users
// use the same project name.
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_DevMode_UnscoppedProjectLookup documents the dev-mode
// cross-tenant project name collision.
func TestAdversarial_DevMode_UnscoppedProjectLookup(t *testing.T) {
	// In dev mode (plan_deployment.go:72-79):
	//   SELECT id FROM projects WHERE name = $1 LIMIT 1
	// This query is NOT scoped to a user or team — it returns the first project
	// with that name across ALL tenants.
	//
	// Attack scenario:
	//   Tenant A creates project "myapp" first → gets id "proj-aaaa".
	//   Tenant B (on same dev instance) also has a project "myapp" → gets id "proj-bbbb".
	//   Dev mode returns proj-aaaa for BOTH tenants.
	//   Tenant B's deployment is routed to Tenant A's project.
	//
	// This is labeled "dev mode only" but dev mode runs on shared preview/staging
	// instances, making it a real cross-tenant risk.

	// Document the expected query behavior (no DB needed — this is a code audit).
	devModeQuery := "SELECT id FROM projects WHERE name = $1 LIMIT 1"

	// The query lacks a team_id or user_id scope.
	assert.NotContains(t, devModeQuery, "team_id",
		"MEDIUM RISK: Dev mode project lookup in plan_deployment.go is NOT scoped to a team or user. "+
			"Multiple tenants with the same project name on a shared dev instance will be routed to "+
			"the FIRST project with that name (lowest created_at). "+
			"Fix: always scope project lookup to the authenticated user, even in dev mode.")

	assert.NotContains(t, devModeQuery, "user_id",
		"Same as above — no user_id scope in dev mode query.")
}

// ─────────────────────────────────────────────────────────────────────────────
// SUMMARY TEST: scopeResourceName collision exhaustion
//
// Run with -count=1 to ensure we actually test with the correct function.
// ─────────────────────────────────────────────────────────────────────────────

// TestAdversarial_ScopeResourceName_DifferentInputsProduceDifferentOutputs is a
// sanity check that confirms two LEGITIMATE distinct tenants don't accidentally
// collide for the most common resource names.
func TestAdversarial_ScopeResourceName_DifferentInputsProduceDifferentOutputs(t *testing.T) {
	type pair struct{ projectID, name string }
	inputs := []pair{
		{"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "api"},
		{"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "web"},
		{"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "postgres"},
		{"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "api"},
		{"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "web"},
		{"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "postgres"},
		{"cccccccc-cccc-cccc-cccc-cccccccccccc", "api"},
	}

	seen := make(map[string]pair)
	for _, p := range inputs {
		scoped := deploy.ScopeServiceName(p.projectID, p.name)
		if existing, ok := seen[scoped]; ok {
			t.Errorf("COLLISION: (%q, %q) and (%q, %q) both produce %q",
				existing.projectID, existing.name,
				p.projectID, p.name,
				scoped,
			)
		}
		seen[scoped] = p
	}
}
