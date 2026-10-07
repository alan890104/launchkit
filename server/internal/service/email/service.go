// Package email — business logic for transactional email setup via Resend.
//
// Responsibilities:
//   - Create and verify email domains with the Resend provider
//   - Persist domain records to the email_domains table
//   - Inject scoped Resend API keys as project secrets on successful verification
//   - List email domain configuration per project
package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/alan890104/launchkit/server/internal/deploy"
	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Error types ─────────────────────────────────────────────────────────────

type ErrorCode string

const (
	ErrInvalidArgument ErrorCode = "invalid_argument"
	ErrNotFound        ErrorCode = "not_found"
	ErrConflict        ErrorCode = "conflict"
	ErrUnavailable     ErrorCode = "unavailable"
	ErrInternal        ErrorCode = "internal"
	ErrExternal        ErrorCode = "external"
)

type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string { return e.Message }

// IsCode reports whether err is a service Error with the given code.
func IsCode(err error, code ErrorCode) bool {
	var svcErr *Error
	return errors.As(err, &svcErr) && svcErr.Code == code
}

func newError(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// ─── SQL constants ────────────────────────────────────────────────────────────

const (
	setupCheckExistsSQL = `
		SELECT id FROM email_domains
		WHERE project_id = $1 AND domain = $2
	`
	setupInsertSQL = `
		INSERT INTO email_domains (project_id, domain, provider, provider_id, status, dns_records)
		VALUES ($1, $2, 'resend', $3, 'pending_dns', $4)
	`
	verifySelectSQL = `
		SELECT id, provider_id, status FROM email_domains
		WHERE project_id = $1 AND domain = $2
	`
	verifyUpdateSQL = `
		UPDATE email_domains SET status = 'verified', updated_at = NOW()
		WHERE id = $1 AND status != 'verified'
		RETURNING id
	`
	getConfigSelectSQL = `
		SELECT domain, provider, status, dns_records
		FROM email_domains
		WHERE project_id = $1
		ORDER BY created_at
	`
)

// ─── Service ─────────────────────────────────────────────────────────────────

type Service struct {
	db       *pgxpool.Pool
	provider provider.Email
	enc      *deploy.Encryptor
}

func NewService(db *pgxpool.Pool, p provider.Email, enc *deploy.Encryptor) *Service {
	return &Service{db: db, provider: p, enc: enc}
}

// ─── Setup ───────────────────────────────────────────────────────────────────

type SetupInput struct {
	ProjectID string
	Domain    string
}

type SetupResult struct {
	Domain     string
	ProviderID string
	Records    []provider.DNSRecord
}

// Setup registers a domain with Resend and persists the DNS records to verify.
func (s *Service) Setup(ctx context.Context, input SetupInput) (*SetupResult, error) {
	if s.provider == nil {
		return nil, newError(ErrUnavailable, "email provider not configured — set RESEND_API_KEY")
	}
	if strings.TrimSpace(input.Domain) == "" {
		return nil, newError(ErrInvalidArgument, "domain is required")
	}
	if strings.TrimSpace(input.ProjectID) == "" {
		return nil, newError(ErrInvalidArgument, "project ID is required")
	}
	if s.db == nil {
		return nil, newError(ErrInternal, "database not configured")
	}

	// Check for existing registration.
	var existingID string
	err := s.db.QueryRow(ctx, setupCheckExistsSQL, input.ProjectID, input.Domain).Scan(&existingID)
	if err == nil {
		return nil, newError(ErrConflict, "email domain %q is already configured for this project — use verify to check status", input.Domain)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, newError(ErrInternal, "check existing domain: %v", err)
	}

	// Register domain with Resend.
	result, err := s.provider.CreateDomain(ctx, input.Domain)
	if err != nil {
		return nil, newError(ErrExternal, "create email domain: %v", err)
	}

	// Persist to DB.
	recordsJSON, _ := json.Marshal(result.Records)
	if _, err := s.db.Exec(ctx, setupInsertSQL, input.ProjectID, input.Domain, result.ProviderID, recordsJSON); err != nil {
		return nil, newError(ErrInternal, "save email domain: %v", err)
	}

	return &SetupResult{
		Domain:     input.Domain,
		ProviderID: result.ProviderID,
		Records:    result.Records,
	}, nil
}

// ─── Verify ──────────────────────────────────────────────────────────────────

type VerifyInput struct {
	ProjectID string
	Domain    string
}

type VerifyResult struct {
	Domain         string
	Status         string // "pending" | "verified"
	Records        []provider.DNSRecordCheck
	APIKeyInjected bool
	EnvVar         string
}

// Verify checks DNS propagation with Resend. On first successful verification
// it auto-generates a scoped API key and injects it as RESEND_API_KEY.
func (s *Service) Verify(ctx context.Context, input VerifyInput) (*VerifyResult, error) {
	if s.provider == nil {
		return nil, newError(ErrUnavailable, "email provider not configured — set RESEND_API_KEY")
	}
	if strings.TrimSpace(input.Domain) == "" {
		return nil, newError(ErrInvalidArgument, "domain is required")
	}
	if strings.TrimSpace(input.ProjectID) == "" {
		return nil, newError(ErrInvalidArgument, "project ID is required")
	}
	if s.db == nil {
		return nil, newError(ErrInternal, "database not configured")
	}

	var emailDomainID, providerID, currentStatus string
	err := s.db.QueryRow(ctx, verifySelectSQL, input.ProjectID, input.Domain).Scan(&emailDomainID, &providerID, &currentStatus)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, newError(ErrNotFound, "email domain %q not found — use setup first", input.Domain)
		}
		return nil, newError(ErrInternal, "lookup email domain: %v", err)
	}

	if currentStatus == "verified" {
		return &VerifyResult{Domain: input.Domain, Status: "verified"}, nil
	}

	status, err := s.provider.VerifyDomain(ctx, providerID)
	if err != nil {
		return nil, newError(ErrExternal, "verification failed: %v", err)
	}

	result := &VerifyResult{
		Domain:  input.Domain,
		Status:  status.Status,
		Records: status.Records,
	}

	if status.Status == "verified" {
		keyName := fmt.Sprintf("lk-%s-%s", input.ProjectID, input.Domain)
		apiKey, keyErr := s.provider.CreateAPIKey(ctx, keyName, providerID)
		if keyErr != nil || apiKey == "" {
			return nil, newError(ErrExternal, "create API key: %v", keyErr)
		}

		var confirmedID string
		dbErr := s.db.QueryRow(ctx, verifyUpdateSQL, emailDomainID).Scan(&confirmedID)
		if dbErr != nil || confirmedID == "" {
			// Another concurrent request already verified — key is still injected
			result.APIKeyInjected = true
			result.EnvVar = "RESEND_API_KEY"
			_ = deploy.SetSecretValues(ctx, s.db, input.ProjectID, map[string]string{"RESEND_API_KEY": apiKey}, s.enc)
			return &VerifyResult{Domain: input.Domain, Status: "verified"}, nil
		}

		_ = deploy.SetSecretValues(ctx, s.db, input.ProjectID, map[string]string{"RESEND_API_KEY": apiKey}, s.enc)
		result.APIKeyInjected = true
		result.EnvVar = "RESEND_API_KEY"
	}

	return result, nil
}

// ─── GetConfig ───────────────────────────────────────────────────────────────

type EmailDomainInfo struct {
	Domain   string
	Provider string
	Status   string
	Records  json.RawMessage
}

type GetConfigResult struct {
	Domains []EmailDomainInfo
}

// GetConfig returns all email domains configured for a project.
func (s *Service) GetConfig(ctx context.Context, projectID string) (*GetConfigResult, error) {
	if s.db == nil {
		return nil, newError(ErrInternal, "database not configured")
	}
	if strings.TrimSpace(projectID) == "" {
		return nil, newError(ErrInvalidArgument, "project ID is required")
	}

	rows, err := s.db.Query(ctx, getConfigSelectSQL, projectID)
	if err != nil {
		return nil, newError(ErrInternal, "query email domains: %v", err)
	}
	defer rows.Close()

	var domains []EmailDomainInfo
	for rows.Next() {
		var info EmailDomainInfo
		if err := rows.Scan(&info.Domain, &info.Provider, &info.Status, &info.Records); err != nil {
			return nil, newError(ErrInternal, "scan email domain: %v", err)
		}
		domains = append(domains, info)
	}
	if err := rows.Err(); err != nil {
		return nil, newError(ErrInternal, "iterate email domains: %v", err)
	}

	return &GetConfigResult{Domains: domains}, nil
}
