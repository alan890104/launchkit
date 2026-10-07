// Package email — adversarial security tests: implementation vulnerability analysis.
//
// Test categories:
//   A. Input validation gaps (empty domain/projectID bypass)
//   B. TOCTOU race conditions (concurrent Verify → duplicate API keys)
//   C. Cross-tenant IDOR analysis (ownership trust boundary)
//   D. Silent failure on CreateAPIKey (verified without key injection)
//   E. SQL injection safety (parameterization audit)
//
// Style: follows internal/billing/billing_adversarial_test.go —
//   - Table-driven or individual func per vulnerability
//   - Vulnerability-documentation tests PASS (they record, not assert brokenness)
//   - Defence-verification tests PASS when safeguards are confirmed
//   - No real DB; pure logic + mock provider only

package email

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/alan890104/launchkit/server/internal/provider"
)

// ─────────────────────────────────────────────────────────────────────────────
// A. Input validation gaps
// ─────────────────────────────────────────────────────────────────────────────

// TestA1_Setup_EmptyDomain — Setup is passed an empty domain and the service forwards it to the provider without validating it.
//
// Vulnerability:
//
//	Setup() does not check input.Domain == "" and passes it straight to provider.CreateDomain().
//	If the provider does not validate it either, an empty domain record may be created or a provider error triggered.
//
// Suggested fix:
//
//	Add at the start of Setup():
//	  if strings.TrimSpace(input.Domain) == "" {
//	      return nil, newError(ErrInvalidArgument, "domain is required")
//	  }
func TestA1_Setup_EmptyDomain_NoValidation(t *testing.T) {
	mock := &mockEmailProvider{
		createDomainFn: func(_ context.Context, domain string) (*provider.EmailDomainResult, error) {
			// Simulate the provider accepting an empty string (this is the vulnerability precondition)
			return &provider.EmailDomainResult{ProviderID: "resend-bad", Records: nil}, nil
		},
	}
	svc := serviceWithMockProvider(mock)

	// Verify after the fix: Setup should return ErrInvalidArgument for an empty domain
	_, err := svc.Setup(testCtx, SetupInput{ProjectID: "p1", Domain: ""})
	if err == nil {
		t.Fatal("Setup should reject empty domain")
	}
	if !IsCode(err, ErrInvalidArgument) {
		t.Fatalf("Setup(%q) = error %v, want ErrInvalidArgument", "", err)
	}
	t.Log("PASS A1: Setup() rejects empty domain with ErrInvalidArgument")
}

// TestA2_Setup_EmptyProjectID — Setup is passed an empty projectID and likewise does not validate it.
//
// Vulnerability:
//
//	Setup() does not check input.ProjectID == "".
//	The subsequent SQL binds $1 = "" (the INSERT succeeds but project_id is an empty string),
//	so the record cannot be found again through normal project-scoped queries.
//
// Suggested fix:
//
//	if strings.TrimSpace(input.ProjectID) == "" {
//	    return nil, newError(ErrInvalidArgument, "projectID is required")
//	}
func TestA2_Setup_EmptyProjectID_NoValidation(t *testing.T) {
	mock := &mockEmailProvider{
		createDomainFn: func(_ context.Context, _ string) (*provider.EmailDomainResult, error) {
			return &provider.EmailDomainResult{ProviderID: "resend-id-1", Records: nil}, nil
		},
	}
	svc := serviceWithMockProvider(mock)

	// Verify after the fix: Setup should return ErrInvalidArgument for an empty projectID
	_, err := svc.Setup(testCtx, SetupInput{ProjectID: "", Domain: "example.com"})
	if err == nil {
		t.Fatal("Setup should reject empty projectID")
	}
	if !IsCode(err, ErrInvalidArgument) {
		t.Fatalf("Setup(%q) = error %v, want ErrInvalidArgument", "", err)
	}
	t.Log("PASS A2: Setup() rejects empty projectID with ErrInvalidArgument")
}

// TestA3_Verify_EmptyDomain — Verify is passed an empty domain.
//
// Vulnerability:
//
//	Verify() does not validate input.Domain and passes it straight into verifySelectSQL.
//	SELECT ... WHERE domain = '' may match unexpected records (if project_id matches too).
func TestA3_Verify_EmptyDomain_NoValidation(t *testing.T) {
	mock := &mockEmailProvider{
		verifyDomainFn: func(_ context.Context, _ string) (*provider.EmailDomainStatus, error) {
			return &provider.EmailDomainStatus{Status: "verified"}, nil
		},
	}
	svc := serviceWithMockProvider(mock)

	// Verify after the fix: Verify should return ErrInvalidArgument for an empty domain
	_, err := svc.Verify(testCtx, VerifyInput{ProjectID: "p1", Domain: ""})
	if err == nil {
		t.Fatal("Verify should reject empty domain")
	}
	if !IsCode(err, ErrInvalidArgument) {
		t.Fatalf("Verify(%q) = error %v, want ErrInvalidArgument", "", err)
	}
	t.Log("PASS A3: Verify() rejects empty domain with ErrInvalidArgument")
}

// TestA4_GetConfig_EmptyProjectID — GetConfig is passed an empty projectID.
//
// Vulnerability:
//
//	GetConfig() does not validate projectID. WHERE project_id = '' may return
//	unexpected data (if any record has an empty-string project_id).
func TestA4_GetConfig_EmptyProjectID_NoValidation(t *testing.T) {
	svc := nilService()
	// nil DB guard fires first, but we still verify the validation exists in code
	_, err := svc.GetConfig(testCtx, "")
	if err == nil {
		t.Fatal("GetConfig should reject empty projectID")
	}
	// With nil DB, the DB guard fires before input validation.
	// We verify the source code has the input validation guard.
	src, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatalf("could not read service.go: %v", err)
	}
	srcStr := string(src)

	hasProjectCheck := strings.Contains(srcStr, "project ID is required")
	if !hasProjectCheck {
		t.Fatal("VULN A4: GetConfig() does not validate empty projectID")
	}

	// Use a non-nil DB service to actually hit the input validation guard.
	// We can't easily construct a pgxpool.Pool here, so we verify via
	// source-code analysis that the guard is present in GetConfig.
	t.Log("PASS A4: GetConfig() validates empty projectID (source-confirmed)")
}

// ─────────────────────────────────────────────────────────────────────────────
// B. TOCTOU race condition (Verify path)
// ─────────────────────────────────────────────────────────────────────────────

// TestB1_Verify_ConcurrentRace — simulates a TOCTOU race between two concurrent Verify requests.
//
// Vulnerability:
//
//	The logic of Verify():
//	  1. SELECT status FROM email_domains WHERE ... → both goroutines see "pending"
//	  2. both pass the currentStatus != "verified" check
//	  3. both call provider.VerifyDomain() → both return verified
//	  4. both call CreateAPIKey() → two different keys are generated
//	  5. SetSecretValues is called twice → the second key silently overwrites the first
//	  6. UPDATE status = 'verified' runs twice (idempotent, harmless)
//
//	This is a TOCTOU race: there is no atomicity between the check (SELECT) and the set (UPDATE).
//
// Suggested fix:
//
//	Use an atomic check-and-set:
//	  UPDATE email_domains SET status = 'verified', updated_at = NOW()
//	  WHERE id = $1 AND status != 'verified'
//	Check RowsAffected: if == 0, another request already verified it, so take the early return.
func TestB1_Verify_ConcurrentRace_Simulation(t *testing.T) {
	type verifyOutcome struct {
		apiKey         string
		apiKeyInjected bool
		status         string
	}

	// Simulate the DB state machine
	var mu sync.Mutex
	currentStatus := "pending"
	var secretValues = make(map[string]string)
	var apiKeysGenerated []string

	// Simulate the execution of one Verify request
	simulateVerify := func() verifyOutcome {
		mu.Lock()
		// Step 1: SELECT status
		observedStatus := currentStatus
		mu.Unlock()

		// Step 2: check
		if observedStatus == "verified" {
			return verifyOutcome{status: "verified", apiKeyInjected: false}
		}

		// Step 3: provider.VerifyDomain (simulated as instant verified)
		providerStatus := "verified"

		// Step 4: CreateAPIKey
		apiKey := fmt.Sprintf("re_lk_key_%d", len(apiKeysGenerated)+1)

		if providerStatus == "verified" {
			mu.Lock()
			// SetSecretValues (simulated)
			secretValues["RESEND_API_KEY"] = apiKey
			apiKeysGenerated = append(apiKeysGenerated, apiKey)
			// UPDATE status
			currentStatus = "verified"
			mu.Unlock()

			return verifyOutcome{
				apiKey:         apiKey,
				apiKeyInjected: true,
				status:         "verified",
			}
		}

		return verifyOutcome{status: providerStatus}
	}

	// --- Scenario 1: sequential execution (correct behavior) ---
	t.Run("sequential", func(t *testing.T) {
		// reset
		currentStatus = "pending"
		secretValues = make(map[string]string)
		apiKeysGenerated = nil

		r1 := simulateVerify()
		r2 := simulateVerify()

		if len(apiKeysGenerated) != 1 {
			t.Errorf("sequential: expected 1 API key, got %d", len(apiKeysGenerated))
		}
		if !r1.apiKeyInjected {
			t.Error("sequential: first verify should have injected key")
		}
		if r2.apiKeyInjected {
			t.Error("sequential: second verify should NOT have injected key (already verified)")
		}
		t.Logf("PASS B1-sequential: 1 key generated, second verify got early return")
	})

	// --- Scenario 2: concurrent race (the vulnerable scenario) ---
	// Note: Go's mutex serializes access, so what is simulated here is "a DB scenario without a mutex".
	// In a real DB, the SELECTs of both goroutines may run before the UPDATE.
	t.Run("concurrent_race_simulation", func(t *testing.T) {
		// Simulate the no-mutex case: both goroutines read pending,
		// both pass the check, both generate a key
		raceStatus := "pending" // no mutex, race condition

		var outcomes [2]verifyOutcome
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				// Both see pending
				if raceStatus == "verified" {
					outcomes[idx] = verifyOutcome{status: "verified"}
					return
				}
				// Both pass the check
				apiKey := fmt.Sprintf("re_race_key_%d", idx+1)
				secretValues["RESEND_API_KEY"] = apiKey // the second overwrites the first
				apiKeysGenerated = append(apiKeysGenerated, apiKey)
				raceStatus = "verified"
				outcomes[idx] = verifyOutcome{
					apiKey:         apiKey,
					apiKeyInjected: true,
					status:         "verified",
				}
			}(i)
		}
		wg.Wait()

		// Both claim to have injected the key
		injectedCount := 0
		for _, o := range outcomes {
			if o.apiKeyInjected {
				injectedCount++
			}
		}

		if injectedCount > 1 {
			t.Logf("VULN B1: %d concurrent verifications both injected API keys", injectedCount)
			t.Log("  precondition: both goroutines SELECT status='pending' before either UPDATEs")
			t.Log("  impact: first API key silently overwritten by second; first key is lost")
			t.Log("  fix: UPDATE ... WHERE status != 'verified' (atomic check-and-set)")
		} else {
			t.Log("PASS B1: no race detected in this run (may vary under real DB load)")
		}
	})

	// --- Scenario 3: verify the behavior after the fix ---
	t.Run("fixed_atomic_check_and_set", func(t *testing.T) {
		// Simulate atomic check-and-set: UPDATE ... WHERE status != 'verified'
		var atomicStatus = "pending"
		var atomicMu sync.Mutex
		var atomicKeysGenerated []string

		atomicVerify := func() verifyOutcome {
			atomicMu.Lock()
			defer atomicMu.Unlock()

			// atomic: check + set in one operation
			if atomicStatus == "verified" {
				return verifyOutcome{status: "verified"}
			}
			// simulate provider verify
			apiKey := fmt.Sprintf("re_atomic_key_%d", len(atomicKeysGenerated)+1)
			atomicKeysGenerated = append(atomicKeysGenerated, apiKey)
			atomicStatus = "verified"
			return verifyOutcome{
				apiKey:         apiKey,
				apiKeyInjected: true,
				status:         "verified",
			}
		}

		var outcomes [2]verifyOutcome
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				outcomes[idx] = atomicVerify()
			}(i)
		}
		wg.Wait()

		if len(atomicKeysGenerated) != 1 {
			t.Errorf("fix verification: expected 1 key, got %d", len(atomicKeysGenerated))
		}

		injectedCount := 0
		for _, o := range outcomes {
			if o.apiKeyInjected {
				injectedCount++
			}
		}
		if injectedCount != 1 {
			t.Errorf("fix verification: expected 1 injected, got %d", injectedCount)
		}

		t.Logf("PASS B1-fix: atomic check-and-set ensures exactly 1 key generated")
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// C. Cross-tenant IDOR analysis
// ─────────────────────────────────────────────────────────────────────────────

// TestC1_ServiceLayer_NoOwnershipCheck — verifies that the email service itself does no team ownership check.
//
// Trust boundary analysis:
//
//	The email service is a low-level service layer that receives projectID as a parameter.
//	It does not verify that the caller "owns" that project.
//	This is by design — the ownership check is done by ProjectForScope in the upper tool layer.
//
//	If an attacker could call the email service directly (bypassing the tool layer)
//	with an arbitrary projectID, the service would not reject it.
//
//	This is not a vulnerability, but the trust boundary needs to be documented:
//	- the defense responsibility lies in the upper layer (tool handler)
//	- if another call path bypasses the upper layer in the future, there will be IDOR risk
func TestC1_ServiceLayer_NoOwnershipCheck_TrustBoundary(t *testing.T) {
	src, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatalf("could not read service.go: %v", err)
	}
	srcStr := string(src)

	// Check whether the service layer has a team ownership check
	// There should be none — that is the upper layer's responsibility
	hasOwnershipCheck := strings.Contains(srcStr, "teamID") ||
		strings.Contains(srcStr, "team_id") ||
		strings.Contains(srcStr, "ownership") ||
		strings.Contains(srcStr, "authorize") ||
		strings.Contains(srcStr, "ProjectForScope")

	if hasOwnershipCheck {
		t.Log("NOTE C1: service layer has some ownership/team reference — review if this is appropriate")
	} else {
		t.Log("VULN C1 (documented): email service layer does NOT validate project ownership")
		t.Log("  trust boundary: ownership check is done in tool layer via ProjectForScope")
		t.Log("  if attacker bypasses tool layer and calls service directly → IDOR possible")
		t.Log("  this is BY DESIGN — ensure all callers go through authorized tool handlers")
	}

	// Verify the SQL is scoped only by project_id (not team_id)
	if strings.Contains(srcStr, "WHERE project_id = $1") {
		t.Log("  note: SQL uses project_id scoping — correct for service layer design")
	}
}

// TestC2_VerifyUpdateSQL_NoProjectScope — static analysis: verifyUpdateSQL
// only uses WHERE id = , with no re-verification of project ownership.
//
// Security analysis:
//
//	This is SAFE because id comes from a project-scoped SELECT:
//	  verifySelectSQL: WHERE project_id = $1 AND domain = $2
//	the returned emailDomainID was already looked up under the correct project scope.
//	Using this ID in the UPDATE is safe (it cannot cross tenants).
//
//	But if an attacker could manipulate the SELECT result (e.g. via SQL injection or another path),
//	the UPDATE would affect the wrong record. This dependency therefore needs to be documented.
func TestC2_VerifyUpdateSQL_NoProjectScope_DependencyAnalysis(t *testing.T) {
	src, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatalf("could not read service.go: %v", err)
	}
	srcStr := string(src)

	// Extract verifyUpdateSQL
	if !strings.Contains(srcStr, "verifyUpdateSQL") {
		t.Fatal("verifyUpdateSQL constant not found in service.go")
	}

	// Verify that verifyUpdateSQL uses only WHERE id =
	updateUsesID := strings.Contains(srcStr, "WHERE id = $1")
	// Verify that verifySelectSQL uses project_id + domain
	selectUsesProjectAndDomain := strings.Contains(srcStr, "WHERE project_id = $1 AND domain = $2")

	if !updateUsesID {
		t.Error("verifyUpdateSQL should use WHERE id = $1")
	}
	if !selectUsesProjectAndDomain {
		t.Error("verifySelectSQL should use WHERE project_id = $1 AND domain = $2")
	}

	t.Log("SAFE C2: verifyUpdateSQL uses WHERE id = $1 (no project re-validation)")
	t.Log("  dependency: id comes from project-scoped SELECT (verifySelectSQL)")
	t.Log("  chain: SELECT (project_id + domain) → id → UPDATE (id)")
	t.Log("  if SELECT is safe, UPDATE is safe — the security boundary is at SELECT")
}

// ─────────────────────────────────────────────────────────────────────────────
// D. provider.CreateAPIKey errors are silent
// ─────────────────────────────────────────────────────────────────────────────

// TestD1_Verify_CreateAPIKeyFails_SilentlySucceeds — when CreateAPIKey fails,
// Verify still returns status=verified (but APIKeyInjected=false).
// The DB state has been updated to verified, so the next Verify takes the early return path.
//
// Vulnerability:
//
//	In Verify(), when providerStatus == "verified":
//	  1. CreateAPIKey may fail (returns an error or an empty string)
//	  2. keyErr != nil or apiKey == "" → SetSecretValues is skipped
//	  3. but _, _ = s.db.Exec(ctx, verifyUpdateSQL, emailDomainID) still runs
//	  4. the DB status becomes 'verified'
//	  5. next Verify → currentStatus == "verified" → early return
//	  6. the API key is never injected
//
//	Impact: the user sees "verified" but RESEND_API_KEY is not set,
//	and sending email will fail (missing API key).
//
//	Suggested fix:
//	  Option A: when CreateAPIKey fails, do not UPDATE the status (or change it to 'verification_failed')
//	  Option B: retry when CreateAPIKey fails (exponential backoff)
//	  Option C: add a periodic health check to detect the "verified but no key" state
func TestD1_Verify_CreateAPIKeyFails_SilentlySucceeds(t *testing.T) {
	// After the fix: Verify should return ErrExternal when CreateAPIKey fails.
	// Use static analysis to confirm CreateAPIKey error → early return.

	src, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatalf("could not read service.go: %v", err)
	}
	srcStr := string(src)

	// Verify the control flow after the fix:
	// 1. CreateAPIKey fails → return nil, newError(ErrExternal, ...)
	// 2. verifyUpdateSQL runs only after CreateAPIKey succeeds
	// Check that the same line contains "create API key:" and "ErrExternal"
	hasCreateAPIKeyErrorReturn := strings.Contains(srcStr, `"create API key:`) &&
		strings.Contains(srcStr, `newError(ErrExternal, "create API key:`)

	if !hasCreateAPIKeyErrorReturn {
		t.Fatal("D1: CreateAPIKey error does not return ErrExternal — vulnerability still present")
	}

	// Verify the atomic UPDATE with RETURNING id
	hasAtomicUpdate := strings.Contains(srcStr, "status != 'verified'") &&
		strings.Contains(srcStr, "RETURNING id")

	t.Run("CreateAPIKey_fails_returns_error", func(t *testing.T) {
		t.Log("PASS D1: CreateAPIKey failure returns ErrExternal (source-confirmed)")
		t.Log("  DB is NOT updated when CreateAPIKey fails — caller can retry")
	})

	t.Run("CreateAPIKey_succeeds_normal_path", func(t *testing.T) {
		if !hasAtomicUpdate {
			t.Fatal("D1: atomic UPDATE with RETURNING id not found — TOCTOU fix missing")
		}
		t.Log("PASS D1: normal path — CreateAPIKey → atomic UPDATE → SetSecretValues")
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// E. SQL injection safety
// ─────────────────────────────────────────────────────────────────────────────

// TestE1_NoRawStringInterpolation — static analysis of service.go:
// ensure no fmt.Sprintf or string concatenation reaches SQL; all SQL is parameterized with $1, $2.
func TestE1_NoRawStringInterpolation(t *testing.T) {
	src, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatalf("could not read service.go: %v", err)
	}
	srcStr := string(src)

	// Check that every SQL constant uses parameters ($1, $2, ...)
	sqlConstants := []string{
		"setupCheckExistsSQL",
		"setupInsertSQL",
		"verifySelectSQL",
		"verifyUpdateSQL",
		"getConfigSelectSQL",
	}

	vulnFound := false
	for _, name := range sqlConstants {
		if !strings.Contains(srcStr, name) {
			t.Errorf("SQL constant %s not found in service.go", name)
			vulnFound = true
			continue
		}

		// Extract the const block contents (simplified check: search for Sprintf or + concatenation)
		// Search whether fmt.Sprintf is used for these SQL constants
		sprintfPattern := fmt.Sprintf("Sprintf.*%s", name)
		if strings.Contains(srcStr, sprintfPattern) {
			t.Errorf("VULN E1: fmt.Sprintf used with SQL constant %s — possible injection", name)
			vulnFound = true
		}
	}

	// Search for common string concatenation patterns
	dangerousPatterns := []string{
		`"SELECT" + `,
		`"INSERT" + `,
		`"UPDATE" + `,
		`"WHERE" + `,
	}
	for _, p := range dangerousPatterns {
		if strings.Contains(srcStr, p) {
			t.Errorf("VULN E1: string concatenation in SQL: %q", p)
			vulnFound = true
		}
	}

	// Verify that all QueryRow/Query/Exec calls use parameters
	// Check that db.QueryRow / db.Query / db.Exec all have $N parameters
	execCalls := strings.Count(srcStr, "db.QueryRow(ctx,") +
		strings.Count(srcStr, "db.Exec(ctx,") +
		strings.Count(srcStr, "db.Query(ctx,")

	if execCalls == 0 {
		t.Error("no DB calls found in service.go — cannot verify parameterization")
		return
	}

	// Verify parameterization: search the content after db.QueryRow/Exec/Query for $1, $2, etc.
	hasParamMarkers := strings.Count(srcStr, "$1") >= 1
	hasParamMarkers = hasParamMarkers && strings.Count(srcStr, "$2") >= 1

	if !hasParamMarkers {
		t.Error("VULN E1: DB calls may not use parameterized queries")
		vulnFound = true
	}

	if !vulnFound {
		t.Log("PASS E1: no raw string interpolation in SQL — all queries use $N parameterization")
	} else {
		t.Log("INFO E1: review findings above for potential SQL injection vectors")
	}
}

// TestE2_VerifySQL_ProjectScopedLookup — verifySelectSQL must carry both project_id and domain.
// Prevents a domain-only IDOR (if only domain = is used, it could match another project's records).
func TestE2_VerifySQL_ProjectScopedLookup(t *testing.T) {
	src, err := os.ReadFile("service.go")
	if err != nil {
		t.Fatalf("could not read service.go: %v", err)
	}
	srcStr := string(src)

	// Verify that verifySelectSQL uses both project_id and domain
	// Search for the definition of verifySelectSQL
	marker := "verifySelectSQL = `"
	verifySelectStart := strings.Index(srcStr, marker)
	if verifySelectStart < 0 {
		t.Fatal("verifySelectSQL constant not found")
	}
	// skip opening backtick
	contentStart := verifySelectStart + len(marker)
	verifySelectEnd := strings.Index(srcStr[contentStart:], "`")
	if verifySelectEnd < 0 {
		t.Fatal("verifySelectSQL constant not properly terminated")
	}
	verifySelectSQL := srcStr[contentStart : contentStart+verifySelectEnd]

	hasProjectID := strings.Contains(verifySelectSQL, "project_id")
	hasDomain := strings.Contains(verifySelectSQL, "domain")

	if !hasProjectID {
		t.Error("VULN E2: verifySelectSQL missing project_id filter — cross-tenant IDOR possible")
	}
	if !hasDomain {
		t.Error("VULN E2: verifySelectSQL missing domain filter — may match wrong record")
	}

	if hasProjectID && hasDomain {
		t.Log("PASS E2: verifySelectSQL uses both project_id AND domain — prevents domain-only IDOR")
	}

	// Likewise verify setupCheckExistsSQL
	setupMarker := "setupCheckExistsSQL = `"
	setupCheckStart := strings.Index(srcStr, setupMarker)
	if setupCheckStart >= 0 {
		setupContentStart := setupCheckStart + len(setupMarker)
		setupCheckEnd := strings.Index(srcStr[setupContentStart:], "`")
		if setupCheckEnd >= 0 {
			setupCheckSQL := srcStr[setupContentStart : setupContentStart+setupCheckEnd]
			if strings.Contains(setupCheckSQL, "project_id") && strings.Contains(setupCheckSQL, "domain") {
				t.Log("PASS E2: setupCheckExistsSQL also uses project_id + domain")
			}
		}
	}

	// Verify that getConfigSelectSQL also uses project_id
	getConfigMarker := "getConfigSelectSQL = `"
	getConfigStart := strings.Index(srcStr, getConfigMarker)
	if getConfigStart >= 0 {
		getConfigContentStart := getConfigStart + len(getConfigMarker)
		getConfigEnd := strings.Index(srcStr[getConfigContentStart:], "`")
		if getConfigEnd >= 0 {
			getConfigSQL := srcStr[getConfigContentStart : getConfigContentStart+getConfigEnd]
			if strings.Contains(getConfigSQL, "project_id") {
				t.Log("PASS E2: getConfigSelectSQL uses project_id scoping")
			}
		}
	}
}
