package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/alan890104/launchkit/server/internal/config"
	"github.com/alan890104/launchkit/server/internal/deploy"
	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ErrorCode string

const (
	ErrInvalidArgument   ErrorCode = "invalid_argument"
	ErrNotFound          ErrorCode = "not_found"
	ErrConflict          ErrorCode = "conflict"
	ErrFailedPrecondtion ErrorCode = "failed_precondition"
	ErrUnavailable       ErrorCode = "unavailable"
	ErrInternal          ErrorCode = "internal"
	ErrExternal          ErrorCode = "external"
)

type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string { return e.Message }

func IsCode(err error, code ErrorCode) bool {
	var svcErr *Error
	return errors.As(err, &svcErr) && svcErr.Code == code
}

func newError(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

type Service struct {
	cfg       *config.Config
	db        *pgxpool.Pool
	logger    *slog.Logger
	compute   provider.Compute
	registrar provider.DomainRegistrar
}

func NewService(cfg *config.Config, db *pgxpool.Pool, logger *slog.Logger, compute provider.Compute, registrar provider.DomainRegistrar) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		cfg:       cfg,
		db:        db,
		logger:    logger,
		compute:   compute,
		registrar: registrar,
	}
}

type SearchResponse struct {
	Results      []provider.DomainSearchResult `json:"results"`
	Total        int                           `json:"total"`
	Available    int                           `json:"available"`
	Instructions string                        `json:"instructions"`
}

type PurchasedDomainEntry struct {
	Domain        string  `json:"domain"`
	Status        string  `json:"status"`
	ExpiresAt     string  `json:"expires_at"`
	AutoRenew     bool    `json:"auto_renew"`
	DNSConfigured bool    `json:"dns_configured"`
	PurchasePrice float64 `json:"purchase_price"`
	RenewalPrice  float64 `json:"renewal_price"`
	Registrar     string  `json:"registrar"`
	Project       string  `json:"project,omitempty"`
}

type PurchasedDomainsResponse struct {
	Domains []PurchasedDomainEntry `json:"domains"`
	Total   int                    `json:"total"`
}

type PurchaseInput struct {
	Domain      string
	ProjectID   string
	ProjectName string
	ServiceName string
	TeamID      string
	Years       int
}

type PurchaseResult struct {
	Domain     string  `json:"domain"`
	Status     string  `json:"status"`
	ExpiresAt  string  `json:"expires_at"`
	PriceUSD   float64 `json:"price_usd"`
	RenewalUSD float64 `json:"renewal_usd"`
	Years      int     `json:"years"`
	AutoRenew  bool    `json:"auto_renew"`
	DNSStatus  string  `json:"dns_status"`
	Message    string  `json:"message"`
}

type AddDomainInput struct {
	ProjectID   string
	ServiceName string
	ServiceURL  string
	Domain      string
	TeamIDs     []string
}

type AddDomainResult struct {
	Domain       string               `json:"domain"`
	Service      string               `json:"service"`
	Status       string               `json:"status"`
	DNSRecords   []provider.DNSRecord `json:"dns_records"`
	Message      string               `json:"message,omitempty"`
	Instructions string               `json:"instructions,omitempty"`
	ServiceID    string               `json:"-"`
}

type VerifyDomainInput struct {
	ProjectID   string
	ServiceName string
	ServiceURL  string
	Domain      string
}

type VerifyDomainResult struct {
	Domain        string `json:"domain"`
	DNSVerified   bool   `json:"dns_verified"`
	SSLStatus     string `json:"ssl_status"`
	OwnerVerified bool   `json:"owner_verified"`
	Status        string `json:"status"`
	Message       string `json:"message"`
}

type RemoveDomainInput struct {
	ProjectID   string
	ServiceName string
	ServiceURL  string
	Domain      string
	TeamIDs     []string
}

type RemoveDomainResult struct {
	Domain    string `json:"domain"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	ServiceID string `json:"-"`
}

type DNSRecordInput struct {
	ID       int    `json:"id"`
	Type     string `json:"type"`
	Host     string `json:"host"`
	Value    string `json:"value"`
	TTL      int    `json:"ttl"`
	Priority int    `json:"priority"`
}

type DNSListResult struct {
	Domain  string                        `json:"domain"`
	Records []provider.RegistrarDNSRecord `json:"records"`
	Total   int                           `json:"total"`
}

type DNSChangeResult struct {
	Domain  string                        `json:"domain"`
	Created []provider.RegistrarDNSRecord `json:"created,omitempty"`
	Deleted []int                         `json:"deleted,omitempty"`
	Errors  []string                      `json:"errors,omitempty"`
	Total   int                           `json:"total"`
}

type serviceRef struct {
	ID     string
	Name   string
	Target string
	EnvID  string
}

func (s *Service) Search(ctx context.Context, query string) (*SearchResponse, error) {
	if s.registrar == nil {
		return nil, newError(ErrUnavailable, "domain registrar not configured — set NAMECOM_API_USER and NAMECOM_API_TOKEN")
	}

	domains := ExpandSearchQuery(query)
	if len(domains) == 0 {
		return nil, newError(ErrInvalidArgument, "no valid domain names provided")
	}
	if len(domains) > 50 {
		return nil, newError(ErrInvalidArgument, "too many domains to check (max 50)")
	}

	results, err := s.registrar.SearchDomains(ctx, domains)
	if err != nil {
		return nil, newError(ErrExternal, "domain search failed: %v", err)
	}

	available := 0
	for _, result := range results {
		if result.Available {
			available++
		}
	}

	return &SearchResponse{
		Results:      results,
		Total:        len(results),
		Available:    available,
		Instructions: "Use purchase_domain to buy an available domain. Price is per year in USD.",
	}, nil
}

func (s *Service) ListPurchased(ctx context.Context, teamIDs []string) (*PurchasedDomainsResponse, error) {
	rows, err := s.db.Query(ctx, `
		SELECT pd.domain, pd.status, pd.expires_at, pd.auto_renew, pd.dns_configured,
		       pd.purchase_price, pd.renewal_price, pd.registrar,
		       COALESCE(p.name, '') AS project_name
		FROM purchased_domains pd
		LEFT JOIN projects p ON pd.project_id = p.id
		WHERE pd.team_id = ANY($1)
		ORDER BY pd.created_at DESC
	`, teamIDs)
	if err != nil {
		return nil, newError(ErrInternal, "query failed: %v", err)
	}
	defer rows.Close()

	var domains []PurchasedDomainEntry
	for rows.Next() {
		var item PurchasedDomainEntry
		var expiresAt time.Time
		if err := rows.Scan(
			&item.Domain,
			&item.Status,
			&expiresAt,
			&item.AutoRenew,
			&item.DNSConfigured,
			&item.PurchasePrice,
			&item.RenewalPrice,
			&item.Registrar,
			&item.Project,
		); err != nil {
			continue
		}
		item.ExpiresAt = expiresAt.Format(time.RFC3339)
		domains = append(domains, item)
	}

	if domains == nil {
		domains = []PurchasedDomainEntry{}
	}

	return &PurchasedDomainsResponse{Domains: domains, Total: len(domains)}, nil
}

func (s *Service) Purchase(ctx context.Context, input PurchaseInput) (*PurchaseResult, error) {
	if s.registrar == nil {
		return nil, newError(ErrUnavailable, "domain registrar not configured")
	}
	if err := deploy.ValidateDomain(input.Domain); err != nil {
		return nil, newError(ErrInvalidArgument, "invalid domain: %v", err)
	}

	years := input.Years
	if years < 1 || years > 10 {
		years = 1
	}

	var existingID string
	_ = s.db.QueryRow(ctx, "SELECT id FROM purchased_domains WHERE domain = $1", input.Domain).Scan(&existingID)
	if existingID != "" {
		return nil, newError(ErrConflict, "domain %q is already purchased through LaunchKit", input.Domain)
	}

	results, err := s.registrar.SearchDomains(ctx, []string{input.Domain})
	if err != nil {
		return nil, newError(ErrExternal, "availability check failed: %v", err)
	}
	if len(results) == 0 || !results[0].Available {
		return nil, newError(ErrConflict, "domain %q is not available for registration", input.Domain)
	}

	pricePerYear := results[0].PriceUSD
	totalPrice := pricePerYear * float64(years)
	renewalPrice := results[0].RenewalUSD

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, newError(ErrInternal, "internal error: could not start transaction")
	}
	defer tx.Rollback(ctx)

	var balance float64
	err = tx.QueryRow(ctx, "SELECT balance FROM teams WHERE id = $1 FOR UPDATE", input.TeamID).Scan(&balance)
	if err != nil {
		return nil, newError(ErrInternal, "failed to check team balance")
	}
	if balance < totalPrice {
		return nil, newError(
			ErrFailedPrecondtion,
			"insufficient balance: domain costs $%.2f (%d year) but team balance is $%.2f — top up first",
			totalPrice, years, balance,
		)
	}

	if _, err := tx.Exec(ctx, "UPDATE teams SET balance = balance - $1 WHERE id = $2", totalPrice, input.TeamID); err != nil {
		return nil, newError(ErrInternal, "failed to deduct balance")
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO usage_records (team_id, project_id, resource_type, resource_id, quantity, unit, cost, recorded_at, provider, resource_urn)
		VALUES ($1, $2, 'domain', $3, $4, 'years', $5, NOW(), 'namecom', $6)
	`, input.TeamID, input.ProjectID, input.Domain, years, totalPrice, "namecom:register:"+input.Domain); err != nil {
		return nil, newError(ErrInternal, "failed to record usage")
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, newError(ErrInternal, "transaction failed")
	}

	ownerEmail := s.OwnerEmail(ctx, input.TeamID)
	purchaseResult, err := s.registrar.PurchaseDomain(ctx, provider.DomainPurchaseOpts{
		Domain: input.Domain,
		Years:  years,
		Contacts: provider.DomainContacts{
			FirstName:  "LaunchKit",
			LastName:   "User",
			Email:      ownerEmail,
			Phone:      "+1.0000000000",
			Address1:   "N/A",
			City:       "San Francisco",
			State:      "CA",
			PostalCode: "94105",
			Country:    "US",
		},
		AutoRenew: true,
	})
	if err != nil {
		s.refundPurchase(ctx, input.TeamID, input.ProjectID, input.Domain, totalPrice)
		return nil, newError(ErrExternal, "domain purchase failed: %v — balance has been refunded", err)
	}

	if lockErr := s.registrar.LockDomain(ctx, input.Domain); lockErr != nil {
		s.logger.Warn("failed to lock domain (non-fatal)", "domain", input.Domain, "error", lockErr)
	}

	if _, err := s.db.Exec(ctx, `
		INSERT INTO purchased_domains (team_id, project_id, domain, registrar, provider_id,
			status, purchase_price, renewal_price, years, auto_renew, expires_at)
		VALUES ($1, $2, $3, 'namecom', $4, 'active', $5, $6, $7, true, $8)
	`, input.TeamID, input.ProjectID, input.Domain, purchaseResult.ProviderID,
		totalPrice, renewalPrice, years, purchaseResult.ExpiresAt); err != nil {
		s.logger.Error("failed to persist purchased domain (domain IS registered at registrar)", "domain", input.Domain, "error", err)
	}

	dnsStatus := "skipped: no service specified"
	if input.ServiceName != "" && input.ProjectID != "" {
		dnsStatus = s.autoDNSForService(ctx, input.Domain, input.ProjectID, input.ServiceName)
	}

	return &PurchaseResult{
		Domain:     input.Domain,
		Status:     "active",
		ExpiresAt:  purchaseResult.ExpiresAt.Format(time.RFC3339),
		PriceUSD:   totalPrice,
		RenewalUSD: renewalPrice,
		Years:      years,
		AutoRenew:  true,
		DNSStatus:  dnsStatus,
		Message: fmt.Sprintf(
			"Domain %s purchased successfully! DNS has been auto-configured. It should be accessible within 1-2 minutes. Note: a 60-day transfer lock applies per ICANN policy.",
			input.Domain,
		),
	}, nil
}

func (s *Service) AddDomain(ctx context.Context, input AddDomainInput) (*AddDomainResult, error) {
	if err := deploy.ValidateDomain(input.Domain); err != nil {
		return nil, newError(ErrInvalidArgument, "invalid domain: %v", err)
	}

	serviceRef, err := s.lookupService(ctx, input.ProjectID, input.ServiceName, input.ServiceURL)
	if err != nil {
		return nil, err
	}

	mapper, region, err := s.domainMapperForTarget(serviceRef.Target)
	if err != nil {
		return nil, err
	}

	result, err := mapper.MapDomain(ctx, provider.MapDomainOpts{
		ServiceName: deploy.ScopeServiceNameWithEnv(input.ProjectID, serviceRef.EnvID, serviceRef.Name),
		Domain:      input.Domain,
		Region:      region,
	})
	if err != nil {
		return nil, newError(ErrExternal, "failed to map domain: %v", err)
	}

	dnsAutoConfigured := false
	if s.registrar != nil {
		rootDomain, purchasedID := s.findOwnedPurchasedRoot(ctx, input.Domain, input.TeamIDs)
		if purchasedID != "" && len(result.Records) > 0 {
			rec := result.Records[0]
			host := subdomainOf(input.Domain, rootDomain)
			if _, dnsErr := s.registrar.CreateDNSRecord(ctx, rootDomain, provider.RegistrarDNSRecord{
				Type:  rec.Type,
				Host:  host,
				Value: rec.Value,
				TTL:   300,
			}); dnsErr != nil {
				s.logger.Warn("auto-DNS record creation failed", "domain", input.Domain, "error", dnsErr)
			} else {
				dnsAutoConfigured = true
			}
		}
	}

	status := "pending_dns"
	if dnsAutoConfigured {
		status = "configured"
	}
	recordsJSON, _ := json.Marshal(result.Records)
	if _, err := s.db.Exec(ctx, `
		INSERT INTO domains (service_id, domain, status, ssl_status, dns_records)
		VALUES ($1, $2, $3, 'pending', $4)
		ON CONFLICT (service_id, domain)
		DO UPDATE SET dns_records = $4, status = $3, updated_at = NOW()
	`, serviceRef.ID, input.Domain, status, recordsJSON); err != nil {
		return nil, newError(ErrInternal, "failed to save domain: %v", err)
	}

	resp := &AddDomainResult{
		Domain:     input.Domain,
		Service:    serviceRef.Name,
		Status:     status,
		DNSRecords: result.Records,
		ServiceID:  serviceRef.ID,
	}
	if dnsAutoConfigured {
		resp.Message = fmt.Sprintf(
			"Domain %s has been auto-configured (DNS record created automatically). SSL certificate is being provisioned — should be active within 1-2 minutes.",
			input.Domain,
		)
	} else {
		resp.Instructions = "Add the DNS records above at your domain registrar. Once added, use verify_domain to check propagation. DNS changes typically take 5-30 minutes."
	}
	return resp, nil
}

func (s *Service) VerifyDomain(ctx context.Context, input VerifyDomainInput) (*VerifyDomainResult, error) {
	if err := deploy.ValidateDomain(input.Domain); err != nil {
		return nil, newError(ErrInvalidArgument, "invalid domain: %v", err)
	}

	serviceRef, err := s.lookupService(ctx, input.ProjectID, input.ServiceName, input.ServiceURL)
	if err != nil {
		return nil, err
	}

	var domainID string
	err = s.db.QueryRow(ctx, "SELECT id FROM domains WHERE service_id = $1 AND domain = $2", serviceRef.ID, input.Domain).Scan(&domainID)
	if err != nil {
		return nil, newError(ErrNotFound, "domain %q not configured for service %q — use add_domain first", input.Domain, serviceRef.Name)
	}

	mapper, region, err := s.domainMapperForTarget(serviceRef.Target)
	if err != nil {
		return nil, err
	}

	status, err := mapper.VerifyDomain(ctx, provider.VerifyDomainOpts{
		ServiceName: deploy.ScopeServiceNameWithEnv(input.ProjectID, serviceRef.EnvID, serviceRef.Name),
		Domain:      input.Domain,
		Region:      region,
	})
	if err != nil {
		return nil, newError(ErrExternal, "verify failed: %v", err)
	}

	domainStatus := "pending_dns"
	if status.DNSVerified {
		domainStatus = "configured"
	}
	_, _ = s.db.Exec(ctx, `
		UPDATE domains SET status = $1, ssl_status = $2, updated_at = $3
		WHERE id = $4
	`, domainStatus, status.SSLStatus, time.Now().UTC(), domainID)

	resp := &VerifyDomainResult{
		Domain:        input.Domain,
		DNSVerified:   status.DNSVerified,
		SSLStatus:     status.SSLStatus,
		OwnerVerified: status.OwnerVerified,
		Status:        domainStatus,
	}
	if !status.DNSVerified {
		resp.Message = "DNS records not yet propagated. This can take 5-30 minutes. Try again shortly."
	} else if status.SSLStatus != "active" {
		resp.Message = "DNS verified! SSL certificate is being provisioned. This usually takes a few minutes."
	} else {
		resp.Message = fmt.Sprintf("Domain %s is fully active with SSL.", input.Domain)
	}
	return resp, nil
}

func (s *Service) RemoveDomain(ctx context.Context, input RemoveDomainInput) (*RemoveDomainResult, error) {
	if err := deploy.ValidateDomain(input.Domain); err != nil {
		return nil, newError(ErrInvalidArgument, "invalid domain: %v", err)
	}

	serviceRef, err := s.lookupService(ctx, input.ProjectID, input.ServiceName, input.ServiceURL)
	if err != nil {
		return nil, err
	}

	mapper, region, err := s.domainMapperForTarget(serviceRef.Target)
	if err != nil {
		return nil, err
	}

	if err := mapper.UnmapDomain(ctx, provider.UnmapDomainOpts{
		ServiceName: deploy.ScopeServiceNameWithEnv(input.ProjectID, serviceRef.EnvID, serviceRef.Name),
		Domain:      input.Domain,
		Region:      region,
	}); err != nil {
		return nil, newError(ErrExternal, "failed to remove domain: %v", err)
	}

	if s.registrar != nil {
		rootDomain, purchasedID := s.findOwnedPurchasedRoot(ctx, input.Domain, input.TeamIDs)
		if purchasedID != "" {
			host := subdomainOf(input.Domain, rootDomain)
			records, listErr := s.registrar.ListDNSRecords(ctx, rootDomain)
			if listErr == nil {
				for _, rec := range records {
					if rec.Host == host && (rec.Type == "CNAME" || rec.Type == "A" || rec.Type == "AAAA") {
						if delErr := s.registrar.DeleteDNSRecord(ctx, rootDomain, rec.ID); delErr != nil {
							s.logger.Warn("failed to delete DNS record", "domain", input.Domain, "record_id", rec.ID, "error", delErr)
						}
					}
				}
			}
		}
	}

	_, _ = s.db.Exec(ctx, "DELETE FROM domains WHERE service_id = $1 AND domain = $2", serviceRef.ID, input.Domain)

	return &RemoveDomainResult{
		Domain:    input.Domain,
		Status:    "removed",
		Message:   fmt.Sprintf("Domain %s has been removed from service %s.", input.Domain, serviceRef.Name),
		ServiceID: serviceRef.ID,
	}, nil
}

func (s *Service) ListDNS(ctx context.Context, domain string, teamIDs []string) (*DNSListResult, error) {
	if s.registrar == nil {
		return nil, newError(ErrUnavailable, "domain registrar not configured")
	}
	if err := deploy.ValidateDomain(domain); err != nil {
		return nil, newError(ErrInvalidArgument, "invalid domain: %v", err)
	}

	rootDomain, _, err := s.verifyDomainOwnership(ctx, domain, teamIDs)
	if err != nil {
		return nil, err
	}

	records, err := s.registrar.ListDNSRecords(ctx, rootDomain)
	if err != nil {
		return nil, newError(ErrExternal, "failed to list DNS records: %v", err)
	}
	return &DNSListResult{Domain: rootDomain, Records: records, Total: len(records)}, nil
}

func (s *Service) CreateDNS(ctx context.Context, domain string, teamIDs []string, records []DNSRecordInput) (*DNSChangeResult, error) {
	if s.registrar == nil {
		return nil, newError(ErrUnavailable, "domain registrar not configured")
	}
	if err := deploy.ValidateDomain(domain); err != nil {
		return nil, newError(ErrInvalidArgument, "invalid domain: %v", err)
	}
	if len(records) == 0 {
		return nil, newError(ErrInvalidArgument, "no records provided — pass 'records' array with type, host, value")
	}

	rootDomain, _, err := s.verifyDomainOwnership(ctx, domain, teamIDs)
	if err != nil {
		return nil, err
	}

	var created []provider.RegistrarDNSRecord
	var errorsList []string
	for _, rec := range records {
		if rec.Type == "" || rec.Value == "" {
			errorsList = append(errorsList, fmt.Sprintf("record missing type or value: %+v", rec))
			continue
		}

		result, err := s.registrar.CreateDNSRecord(ctx, rootDomain, provider.RegistrarDNSRecord{
			Type:  rec.Type,
			Host:  rec.Host,
			Value: rec.Value,
			TTL:   rec.TTL,
			Prio:  rec.Priority,
		})
		if err != nil {
			errorsList = append(errorsList, fmt.Sprintf("%s %s → %s: %v", rec.Type, rec.Host, rec.Value, err))
			continue
		}
		created = append(created, *result)
	}

	return &DNSChangeResult{
		Domain:  rootDomain,
		Created: created,
		Errors:  errorsList,
		Total:   len(created),
	}, nil
}

func (s *Service) DeleteDNS(ctx context.Context, domain string, teamIDs []string, records []DNSRecordInput) (*DNSChangeResult, error) {
	if s.registrar == nil {
		return nil, newError(ErrUnavailable, "domain registrar not configured")
	}
	if err := deploy.ValidateDomain(domain); err != nil {
		return nil, newError(ErrInvalidArgument, "invalid domain: %v", err)
	}
	if len(records) == 0 {
		return nil, newError(ErrInvalidArgument, "no records provided — pass 'records' array with 'id' field (get IDs from 'list' action)")
	}

	rootDomain, _, err := s.verifyDomainOwnership(ctx, domain, teamIDs)
	if err != nil {
		return nil, err
	}

	var deleted []int
	var errorsList []string
	for _, rec := range records {
		if rec.ID == 0 {
			errorsList = append(errorsList, "record missing 'id' field — use 'list' action to get record IDs")
			continue
		}
		if err := s.registrar.DeleteDNSRecord(ctx, rootDomain, rec.ID); err != nil {
			errorsList = append(errorsList, fmt.Sprintf("id %d: %v", rec.ID, err))
			continue
		}
		deleted = append(deleted, rec.ID)
	}

	return &DNSChangeResult{
		Domain:  rootDomain,
		Deleted: deleted,
		Errors:  errorsList,
		Total:   len(deleted),
	}, nil
}

func (s *Service) OwnerEmail(ctx context.Context, teamID string) string {
	var email string
	_ = s.db.QueryRow(ctx, `
		SELECT u.email FROM users u
		JOIN team_members tm ON u.id = tm.user_id
		WHERE tm.team_id = $1 AND tm.role = 'owner'
		LIMIT 1
	`, teamID).Scan(&email)
	if email == "" {
		email = "domains@launchkit.dev"
	}
	return email
}

func (s *Service) ResolveProjectID(ctx context.Context, projectName string, teamIDs []string) (string, error) {
	var projectID string
	err := s.db.QueryRow(ctx, "SELECT id FROM projects WHERE name = $1 AND team_id = ANY($2)", projectName, teamIDs).Scan(&projectID)
	if err != nil {
		return "", newError(ErrNotFound, "project %q not found", projectName)
	}
	return projectID, nil
}

func (s *Service) ResolveTeamID(ctx context.Context, projectID string, teamIDs []string) (string, error) {
	var teamID string
	err := s.db.QueryRow(ctx, "SELECT team_id FROM projects WHERE id = $1 AND team_id = ANY($2)", projectID, teamIDs).Scan(&teamID)
	if err != nil {
		return "", newError(ErrNotFound, "project not found or not accessible")
	}
	return teamID, nil
}

func (s *Service) ResolveOwnedDomainTeamID(ctx context.Context, domain string, teamIDs []string) (string, error) {
	_, teamID, err := s.verifyDomainOwnership(ctx, domain, teamIDs)
	if err != nil {
		return "", err
	}
	return teamID, nil
}

func ExpandSearchQuery(query string) []string {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return nil
	}

	if strings.Contains(query, ",") {
		var domains []string
		for _, part := range strings.Split(query, ",") {
			if domain := strings.TrimSpace(part); domain != "" {
				domains = append(domains, domain)
			}
		}
		return domains
	}

	if strings.Contains(query, ".") {
		return []string{query}
	}

	tlds := []string{".com", ".net", ".io", ".dev", ".app", ".co", ".xyz", ".org"}
	domains := make([]string, len(tlds))
	for i, tld := range tlds {
		domains[i] = query + tld
	}
	return domains
}

func (s *Service) lookupService(ctx context.Context, projectID, serviceName, serviceURL string) (*serviceRef, error) {
	ref := &serviceRef{}
	switch {
	case serviceName != "":
		err := s.db.QueryRow(ctx, `
			SELECT s.id, s.name, s.target, s.environment_id
			FROM services s
			JOIN environments e ON s.environment_id = e.id
			WHERE e.project_id = $1 AND s.name = $2
			ORDER BY e.created_at DESC LIMIT 1
		`, projectID, serviceName).Scan(&ref.ID, &ref.Name, &ref.Target, &ref.EnvID)
		if err != nil {
			return nil, newError(ErrNotFound, "service %q not found", serviceName)
		}
	case serviceURL != "":
		err := s.db.QueryRow(ctx, `
			SELECT s.id, s.name, s.target, s.environment_id
			FROM services s
			JOIN environments e ON s.environment_id = e.id
			WHERE e.project_id = $1 AND s.url = $2
			ORDER BY e.created_at DESC LIMIT 1
		`, projectID, serviceURL).Scan(&ref.ID, &ref.Name, &ref.Target, &ref.EnvID)
		if err != nil {
			return nil, newError(ErrNotFound, "service %q not found", serviceURL)
		}
	default:
		return nil, newError(ErrInvalidArgument, "missing service reference")
	}

	return ref, nil
}

func (s *Service) verifyDomainOwnership(ctx context.Context, domain string, teamIDs []string) (rootDomain, teamID string, err error) {
	rootDomain, purchasedID := s.findPurchasedRoot(ctx, domain)
	if purchasedID == "" {
		var id string
		_ = s.db.QueryRow(ctx,
			"SELECT id FROM purchased_domains WHERE domain = $1 AND status = 'active'",
			domain,
		).Scan(&id)
		if id != "" {
			rootDomain = domain
			purchasedID = id
		}
	}
	if purchasedID == "" {
		return "", "", newError(ErrNotFound, "domain %q is not purchased through LaunchKit — use purchase_domain first, or use add_domain for externally-owned domains", domain)
	}

	err = s.db.QueryRow(ctx, `
		SELECT pd.team_id FROM purchased_domains pd
		WHERE pd.id = $1 AND pd.team_id = ANY($2)
	`, purchasedID, teamIDs).Scan(&teamID)
	if err != nil {
		return "", "", newError(ErrNotFound, "you do not have access to domain %q", domain)
	}

	return rootDomain, teamID, nil
}

func (s *Service) autoDNSForService(ctx context.Context, domain, projectID, serviceName string) string {
	if s.registrar == nil {
		return "skipped: domain registrar not configured"
	}

	serviceRef, err := s.lookupService(ctx, projectID, serviceName, "")
	if err != nil {
		return fmt.Sprintf("skipped: service %q not found", serviceName)
	}

	cloudServiceName := deploy.ScopeServiceNameWithEnv(projectID, serviceRef.EnvID, serviceRef.Name)
	var recType, recValue string

	switch serviceRef.Target {
	case "cloudflare_pages":
		recType = "CNAME"
		recValue = cloudServiceName + ".pages.dev"
	case "cloud_run":
		recType = "CNAME"
		recValue = "ghs.googlehosted.com"
	default:
		return fmt.Sprintf("skipped: auto-DNS not supported for target %q", serviceRef.Target)
	}

	host := ""
	parts := strings.SplitN(domain, ".", 2)
	if len(strings.Split(domain, ".")) > 2 {
		host = parts[0]
	}

	if _, err := s.registrar.CreateDNSRecord(ctx, domain, provider.RegistrarDNSRecord{
		Type:  recType,
		Host:  host,
		Value: recValue,
		TTL:   300,
	}); err != nil {
		s.logger.Warn("auto-DNS record creation failed", "domain", domain, "error", err)
		return fmt.Sprintf("DNS record creation failed: %v — you may need to add a %s record for %s pointing to %s manually", err, recType, domain, recValue)
	}

	mapper, region, err := s.domainMapperForTarget(serviceRef.Target)
	if err != nil {
		return fmt.Sprintf("DNS record created, but service mapping failed: %v", err)
	}

	result, mapErr := mapper.MapDomain(ctx, provider.MapDomainOpts{
		ServiceName: cloudServiceName,
		Domain:      domain,
		Region:      region,
	})
	if mapErr != nil {
		return fmt.Sprintf("DNS record created, but service mapping failed: %v", mapErr)
	}

	recordsJSON, _ := json.Marshal(result.Records)
	_, _ = s.db.Exec(ctx, `
		INSERT INTO domains (service_id, domain, status, ssl_status, dns_records)
		VALUES ($1, $2, 'configured', 'provisioning', $3)
		ON CONFLICT (service_id, domain) DO UPDATE SET
			dns_records = $3, status = 'configured', ssl_status = 'provisioning', updated_at = NOW()
	`, serviceRef.ID, domain, recordsJSON)

	_, _ = s.db.Exec(ctx,
		"UPDATE purchased_domains SET dns_configured = true, updated_at = NOW() WHERE domain = $1",
		domain,
	)

	return "auto-configured: DNS record created and domain mapped to service"
}

func (s *Service) refundPurchase(ctx context.Context, teamID, projectID, domain string, amount float64) {
	_, _ = s.db.Exec(ctx, "UPDATE teams SET balance = balance + $1 WHERE id = $2", amount, teamID)
	_, _ = s.db.Exec(ctx, `
		INSERT INTO usage_records (team_id, project_id, resource_type, resource_id, quantity, unit, cost, recorded_at, provider, resource_urn)
		VALUES ($1, $2, 'domain_refund', $3, 1, 'refund', -$4, NOW(), 'namecom', $5)
	`, teamID, projectID, domain, amount, "namecom:refund:"+domain)
	s.logger.Warn("domain purchase refunded", "team_id", teamID, "domain", domain, "amount", amount)
}

func (s *Service) domainMapperForTarget(target string) (provider.DomainMapper, string, error) {
	switch target {
	case "cloud_run":
		domainMapper, ok := s.compute.(provider.DomainMapper)
		if !ok {
			return nil, "", newError(ErrUnavailable, "compute provider does not support custom domains")
		}
		return domainMapper, s.cfg.Region(), nil
	case "cloudflare_pages":
		if s.cfg.CloudflareAccountID == "" {
			return nil, "", newError(ErrUnavailable, "Cloudflare not configured — set CLOUDFLARE_ACCOUNT_ID and CLOUDFLARE_API_TOKEN")
		}
		return provider.NewCloudflarePages(s.cfg.CloudflareAccountID, s.cfg.CloudflareAPIToken), "", nil
	default:
		return nil, "", newError(ErrInvalidArgument, "custom domains not supported for target %q", target)
	}
}

func (s *Service) findPurchasedRoot(ctx context.Context, domain string) (rootDomain string, purchasedID string) {
	parts := strings.Split(domain, ".")
	for i := 1; i < len(parts)-1; i++ {
		candidate := strings.Join(parts[i:], ".")
		var id string
		err := s.db.QueryRow(ctx,
			"SELECT id FROM purchased_domains WHERE domain = $1 AND status = 'active'",
			candidate,
		).Scan(&id)
		if err == nil && id != "" {
			return candidate, id
		}
	}
	return "", ""
}

func (s *Service) findOwnedPurchasedRoot(ctx context.Context, domain string, teamIDs []string) (rootDomain string, purchasedID string) {
	parts := strings.Split(domain, ".")
	for i := 1; i < len(parts)-1; i++ {
		candidate := strings.Join(parts[i:], ".")
		var id string
		err := s.db.QueryRow(ctx, `
			SELECT pd.id FROM purchased_domains pd
			WHERE pd.domain = $1 AND pd.status = 'active' AND pd.team_id = ANY($2)
		`, candidate, teamIDs).Scan(&id)
		if err == nil && id != "" {
			return candidate, id
		}
	}
	return "", ""
}

func subdomainOf(domain, root string) string {
	if domain == root {
		return ""
	}
	return strings.TrimSuffix(domain, "."+root)
}
