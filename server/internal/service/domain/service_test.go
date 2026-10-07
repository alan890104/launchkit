// Package domain — comprehensive tests for the domain service.
//
// Test strategy:
//   - Pure function tests: no DB, no mocks
//   - Guard tests: nil registrar guard fires before IO
//   - Mock registrar: function-field mock implementing provider.DomainRegistrar
//   - SQL security: static analysis via os.ReadFile — verify team-scoping clauses
package domain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── mock registrar ───────────────────────────────────────────────────────────

type mockRegistrar struct {
	searchFn    func(context.Context, []string) ([]provider.DomainSearchResult, error)
	purchaseFn  func(context.Context, provider.DomainPurchaseOpts) (*provider.DomainPurchaseResult, error)
	lockFn      func(context.Context, string) error
	createDNSFn func(context.Context, string, provider.RegistrarDNSRecord) (*provider.RegistrarDNSRecord, error)
	listDNSFn   func(context.Context, string) ([]provider.RegistrarDNSRecord, error)
	deleteDNSFn func(context.Context, string, int) error
}

func (m *mockRegistrar) SearchDomains(ctx context.Context, domains []string) ([]provider.DomainSearchResult, error) {
	if m.searchFn != nil {
		return m.searchFn(ctx, domains)
	}
	return nil, nil
}

func (m *mockRegistrar) PurchaseDomain(ctx context.Context, opts provider.DomainPurchaseOpts) (*provider.DomainPurchaseResult, error) {
	if m.purchaseFn != nil {
		return m.purchaseFn(ctx, opts)
	}
	return &provider.DomainPurchaseResult{ExpiresAt: time.Now().Add(365 * 24 * time.Hour)}, nil
}

func (m *mockRegistrar) LockDomain(ctx context.Context, domain string) error {
	if m.lockFn != nil {
		return m.lockFn(ctx, domain)
	}
	return nil
}

func (m *mockRegistrar) CreateDNSRecord(ctx context.Context, domain string, rec provider.RegistrarDNSRecord) (*provider.RegistrarDNSRecord, error) {
	if m.createDNSFn != nil {
		return m.createDNSFn(ctx, domain, rec)
	}
	return &rec, nil
}

func (m *mockRegistrar) ListDNSRecords(ctx context.Context, domain string) ([]provider.RegistrarDNSRecord, error) {
	if m.listDNSFn != nil {
		return m.listDNSFn(ctx, domain)
	}
	return nil, nil
}

func (m *mockRegistrar) DeleteDNSRecord(ctx context.Context, domain string, id int) error {
	if m.deleteDNSFn != nil {
		return m.deleteDNSFn(ctx, domain, id)
	}
	return nil
}

func (m *mockRegistrar) RenewDomain(_ context.Context, _ string, _ int) (*provider.DomainPurchaseResult, error) {
	return nil, nil
}

func (m *mockRegistrar) UnlockDomain(_ context.Context, _ string) error { return nil }

func (m *mockRegistrar) ListDomains(_ context.Context) ([]provider.DomainInfo, error) {
	return nil, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func nilService() *Service {
	return NewService(nil, nil, nil, nil, nil)
}

func serviceWithMockRegistrar(r *mockRegistrar) *Service {
	return NewService(nil, nil, nil, nil, r)
}

var testCtx = context.Background()

// ─── Group 1: ExpandSearchQuery ───────────────────────────────────────────────

func TestExpandSearchQuery_SingleWithTLD(t *testing.T) {
	got := ExpandSearchQuery("myapp.com")
	require.Len(t, got, 1)
	assert.Equal(t, "myapp.com", got[0])
}

func TestExpandSearchQuery_BaseName(t *testing.T) {
	got := ExpandSearchQuery("myapp")
	require.Len(t, got, 8, "expected 8 TLD variants: %v", got)
	assert.Equal(t, "myapp.com", got[0])
	assert.Equal(t, "myapp.org", got[7])
}

func TestExpandSearchQuery_CommaSeparated(t *testing.T) {
	got := ExpandSearchQuery("foo.com, bar.io")
	require.Len(t, got, 2)
	assert.Equal(t, "foo.com", got[0])
	assert.Equal(t, "bar.io", got[1])
}

func TestExpandSearchQuery_Empty(t *testing.T) {
	assert.Nil(t, ExpandSearchQuery(""))
}

func TestExpandSearchQuery_Uppercase(t *testing.T) {
	got := ExpandSearchQuery("MyApp.COM")
	require.Len(t, got, 1)
	assert.Equal(t, "myapp.com", got[0])
}

func TestExpandSearchQuery_Whitespace(t *testing.T) {
	got := ExpandSearchQuery("  myapp.com  ")
	require.Len(t, got, 1)
	assert.Equal(t, "myapp.com", got[0])
}

// ─── Group 2: subdomainOf ─────────────────────────────────────────────────────

func TestSubdomainOf(t *testing.T) {
	tests := []struct {
		domain, root string
		want         string
	}{
		{"api.myapp.dev", "myapp.dev", "api"},
		{"www.api.myapp.dev", "myapp.dev", "www.api"},
		{"myapp.dev", "myapp.dev", ""},          // apex
		{"other.com", "myapp.dev", "other.com"}, // TrimSuffix has no effect
	}
	for _, tt := range tests {
		t.Run(tt.domain+"/"+tt.root, func(t *testing.T) {
			assert.Equal(t, tt.want, subdomainOf(tt.domain, tt.root))
		})
	}
}

// ─── Group 3: Error type helpers ─────────────────────────────────────────────

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
	assert.False(t, IsCode(errors.New("random error"), ErrNotFound))
}

func TestError_Message(t *testing.T) {
	err := newError(ErrInternal, "hello %s", "world")
	assert.Equal(t, "hello world", err.Error())
}

func TestError_AllCodes(t *testing.T) {
	codes := []ErrorCode{
		ErrInvalidArgument, ErrNotFound, ErrConflict,
		ErrFailedPrecondtion, ErrUnavailable, ErrInternal, ErrExternal,
	}
	for _, code := range codes {
		err := newError(code, "test")
		assert.True(t, IsCode(err, code), "IsCode should match %s", code)
	}
}

// ─── Group 4: Search ─────────────────────────────────────────────────────────

func TestSearch_NilRegistrar(t *testing.T) {
	_, err := nilService().Search(testCtx, "myapp.com")
	require.Error(t, err)
	assert.True(t, IsCode(err, ErrUnavailable))
}

func TestSearch_EmptyQuery(t *testing.T) {
	_, err := serviceWithMockRegistrar(&mockRegistrar{}).Search(testCtx, "")
	require.Error(t, err)
	assert.True(t, IsCode(err, ErrInvalidArgument))
}

func TestSearch_TooManyDomains(t *testing.T) {
	parts := make([]string, 51)
	for i := range parts {
		parts[i] = fmt.Sprintf("domain%d.com", i)
	}
	_, err := serviceWithMockRegistrar(&mockRegistrar{}).Search(testCtx, strings.Join(parts, ","))
	require.Error(t, err)
	assert.True(t, IsCode(err, ErrInvalidArgument))
}

func TestSearch_RegistrarError(t *testing.T) {
	mock := &mockRegistrar{
		searchFn: func(_ context.Context, _ []string) ([]provider.DomainSearchResult, error) {
			return nil, fmt.Errorf("upstream timeout")
		},
	}
	_, err := serviceWithMockRegistrar(mock).Search(testCtx, "myapp.com")
	require.Error(t, err)
}

func TestSearch_Success(t *testing.T) {
	mock := &mockRegistrar{
		searchFn: func(_ context.Context, _ []string) ([]provider.DomainSearchResult, error) {
			return []provider.DomainSearchResult{
				{Domain: "myapp.com", Available: true, PriceUSD: 12.99},
				{Domain: "myapp.io", Available: false, PriceUSD: 39.99},
			}, nil
		},
	}
	resp, err := serviceWithMockRegistrar(mock).Search(testCtx, "myapp.com,myapp.io")
	require.NoError(t, err)
	assert.Equal(t, 2, resp.Total)
	assert.Len(t, resp.Results, 2)
	assert.Equal(t, 1, resp.Available)
}

// ─── Group 5: Purchase nil-registrar guard ───────────────────────────────────

func TestPurchase_NilRegistrar(t *testing.T) {
	_, err := nilService().Purchase(testCtx, PurchaseInput{
		Domain: "myapp.com", ProjectID: "proj-1", TeamID: "team-1", Years: 1,
	})
	require.Error(t, err)
	assert.True(t, IsCode(err, ErrUnavailable))
}

// ─── Group 6: DNS nil-registrar guards ───────────────────────────────────────

func TestListDNS_NilRegistrar(t *testing.T) {
	_, err := nilService().ListDNS(testCtx, "myapp.com", []string{"team-1"})
	require.Error(t, err)
	assert.True(t, IsCode(err, ErrUnavailable))
}

func TestCreateDNS_NilRegistrar(t *testing.T) {
	_, err := nilService().CreateDNS(testCtx, "myapp.com", []string{"team-1"}, []DNSRecordInput{
		{Type: "A", Host: "", Value: "1.2.3.4"},
	})
	require.Error(t, err)
	assert.True(t, IsCode(err, ErrUnavailable))
}

func TestDeleteDNS_NilRegistrar(t *testing.T) {
	_, err := nilService().DeleteDNS(testCtx, "myapp.com", []string{"team-1"}, []DNSRecordInput{
		{ID: 42},
	})
	require.Error(t, err)
	assert.True(t, IsCode(err, ErrUnavailable))
}

// ─── Group 7: SQL security — static analysis ─────────────────────────────────

func readServiceSrc(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("service.go")
	require.NoError(t, err, "could not read service.go for SQL analysis")
	return string(data)
}

func TestSQL_ListPurchased_TeamScoped(t *testing.T) {
	src := readServiceSrc(t)
	assert.Contains(t, src, "team_id = ANY",
		"ListPurchased must scope results by team_id = ANY to prevent cross-tenant leakage")
}

func TestSQL_VerifyOwnership_TeamScoped(t *testing.T) {
	src := readServiceSrc(t)
	// verifyDomainOwnership and findOwnedPurchasedRoot both use team_id = ANY
	count := strings.Count(src, "team_id = ANY")
	assert.GreaterOrEqual(t, count, 2,
		"expected ≥2 occurrences of team_id = ANY (ownership + list queries)")
}

func TestSQL_Purchase_AtomicBalanceDeduction(t *testing.T) {
	src := readServiceSrc(t)
	assert.Contains(t, src, "FOR UPDATE",
		"Purchase balance deduction must use SELECT ... FOR UPDATE for atomicity")
}

func TestSQL_Purchase_DuplicateCheck(t *testing.T) {
	src := readServiceSrc(t)
	assert.Contains(t, src, "purchased_domains WHERE domain",
		"Purchase must check for existing domain registration before proceeding")
}

func TestSQL_BalanceLock_ByPrimaryKey(t *testing.T) {
	src := readServiceSrc(t)
	assert.Contains(t, src, "WHERE id = $1 FOR UPDATE",
		"balance row lock must be on team primary key (pre-verified), not arbitrary user input")
}
