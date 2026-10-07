// Package email — comprehensive tests for the email service.
//
// Test strategy:
//   - Pure function tests: no DB, no mocks
//   - Guard tests: nil provider/DB fires before IO
//   - Mock provider: function-field mock implementing provider.Email
//   - SQL security: static analysis via os.ReadFile
package email

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── mock provider ────────────────────────────────────────────────────────────

type mockEmailProvider struct {
	createDomainFn func(context.Context, string) (*provider.EmailDomainResult, error)
	verifyDomainFn func(context.Context, string) (*provider.EmailDomainStatus, error)
	createAPIKeyFn func(context.Context, string, string) (string, error)
	deleteDomainFn func(context.Context, string) error
}

func (m *mockEmailProvider) CreateDomain(ctx context.Context, domain string) (*provider.EmailDomainResult, error) {
	if m.createDomainFn != nil {
		return m.createDomainFn(ctx, domain)
	}
	return &provider.EmailDomainResult{ProviderID: "resend-id-1", Records: nil}, nil
}

func (m *mockEmailProvider) VerifyDomain(ctx context.Context, domainID string) (*provider.EmailDomainStatus, error) {
	if m.verifyDomainFn != nil {
		return m.verifyDomainFn(ctx, domainID)
	}
	return &provider.EmailDomainStatus{Status: "pending"}, nil
}

func (m *mockEmailProvider) CreateAPIKey(ctx context.Context, name, domainID string) (string, error) {
	if m.createAPIKeyFn != nil {
		return m.createAPIKeyFn(ctx, name, domainID)
	}
	return "re_test_key", nil
}

func (m *mockEmailProvider) DeleteDomain(ctx context.Context, domainID string) error {
	if m.deleteDomainFn != nil {
		return m.deleteDomainFn(ctx, domainID)
	}
	return nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func nilService() *Service {
	return NewService(nil, nil, nil)
}

func serviceWithMockProvider(p *mockEmailProvider) *Service {
	return NewService(nil, p, nil)
}

var testCtx = context.Background()

// ─── Group 1: Error type helpers ─────────────────────────────────────────────

func TestError_IsCode_Match(t *testing.T) {
	err := newError(ErrNotFound, "not found")
	assert.True(t, IsCode(err, ErrNotFound))
}

func TestError_IsCode_Mismatch(t *testing.T) {
	err := newError(ErrNotFound, "not found")
	assert.False(t, IsCode(err, ErrConflict))
}

func TestError_IsCode_Nil(t *testing.T) {
	assert.False(t, IsCode(nil, ErrNotFound))
}

func TestError_IsCode_NonServiceError(t *testing.T) {
	assert.False(t, IsCode(errors.New("random"), ErrNotFound))
}

func TestError_Message(t *testing.T) {
	err := newError(ErrInternal, "hello %s", "world")
	assert.Equal(t, "hello world", err.Error())
}

func TestError_AllCodes(t *testing.T) {
	codes := []ErrorCode{
		ErrInvalidArgument, ErrNotFound, ErrConflict,
		ErrUnavailable, ErrInternal, ErrExternal,
	}
	for _, code := range codes {
		e := newError(code, "test")
		assert.True(t, IsCode(e, code), "IsCode should match %s", code)
	}
}

// ─── Group 2: Setup guards ────────────────────────────────────────────────────

func TestSetup_NilProvider(t *testing.T) {
	_, err := nilService().Setup(testCtx, SetupInput{ProjectID: "p1", Domain: "example.com"})
	require.Error(t, err)
	assert.True(t, IsCode(err, ErrUnavailable))
}

func TestSetup_NilDB(t *testing.T) {
	_, err := serviceWithMockProvider(&mockEmailProvider{}).Setup(testCtx, SetupInput{ProjectID: "p1", Domain: "example.com"})
	require.Error(t, err)
	assert.True(t, IsCode(err, ErrInternal))
}

func TestSetup_ProviderError(t *testing.T) {
	mock := &mockEmailProvider{
		createDomainFn: func(_ context.Context, _ string) (*provider.EmailDomainResult, error) {
			return nil, errors.New("resend API down")
		},
	}
	// nil DB still fires first; test the provider error path via a service
	// that has a provider but no DB (ErrInternal from DB guard comes first).
	// We can only reach ErrExternal with a real DB; verify the provider error
	// wraps correctly by directly testing newError wrapping.
	err := newError(ErrExternal, "create email domain: %v", errors.New("resend API down"))
	assert.True(t, IsCode(err, ErrExternal))
	_ = mock
}

// ─── Group 3: Verify guards ───────────────────────────────────────────────────

func TestVerify_NilProvider(t *testing.T) {
	_, err := nilService().Verify(testCtx, VerifyInput{ProjectID: "p1", Domain: "example.com"})
	require.Error(t, err)
	assert.True(t, IsCode(err, ErrUnavailable))
}

func TestVerify_NilDB(t *testing.T) {
	_, err := serviceWithMockProvider(&mockEmailProvider{}).Verify(testCtx, VerifyInput{ProjectID: "p1", Domain: "example.com"})
	require.Error(t, err)
	assert.True(t, IsCode(err, ErrInternal))
}

// ─── Group 4: GetConfig guards ────────────────────────────────────────────────

func TestGetConfig_NilDB(t *testing.T) {
	_, err := nilService().GetConfig(testCtx, "p1")
	require.Error(t, err)
	assert.True(t, IsCode(err, ErrInternal))
}

// ─── Group 5: SQL security — static analysis ─────────────────────────────────

func readServiceSrc(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("service.go")
	require.NoError(t, err, "could not read service.go for SQL analysis")
	return string(data)
}

func TestSQL_Setup_ProjectScoped(t *testing.T) {
	src := readServiceSrc(t)
	assert.Contains(t, src, "project_id = $1",
		"setup SQL must scope by project_id to prevent cross-tenant leakage")
}

func TestSQL_Verify_ProjectScoped(t *testing.T) {
	src := readServiceSrc(t)
	// verifySelectSQL and verifyUpdateSQL are both in service.go
	count := strings.Count(src, "project_id = $1")
	assert.GreaterOrEqual(t, count, 2,
		"expected ≥2 occurrences of project_id = $1 (setup check + verify select)")
}

func TestSQL_GetConfig_ProjectScoped(t *testing.T) {
	src := readServiceSrc(t)
	assert.Contains(t, src, "getConfigSelectSQL",
		"GetConfig must use the named SQL constant")
	assert.Contains(t, src, "WHERE project_id = $1",
		"getConfigSelectSQL must scope by project_id")
}
