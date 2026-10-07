package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"
)

// Compile-time interface guard.
var _ DomainRegistrar = (*NameCom)(nil)

const (
	namecomProdBase    = "https://api.name.com/v4"
	namecomSandboxBase = "https://api.dev.name.com/v4"

	// Retry config for 429/5xx.
	namecomMaxRetries  = 3
	namecomBaseBackoff = 1 * time.Second
)

// NameCom implements DomainRegistrar using the Name.com v4 REST API.
type NameCom struct {
	username string
	token    string
	baseURL  string
	client   *http.Client
	limiter  *RateLimiter
	cb       *CircuitBreaker
}

// NewNameCom creates a new Name.com provider.
// Set sandbox=true for development (uses api.dev.name.com).
func NewNameCom(username, token string, sandbox bool) *NameCom {
	base := namecomProdBase
	if sandbox {
		base = namecomSandboxBase
	}
	return &NameCom{
		username: username,
		token:    token,
		baseURL:  base,
		client:   &http.Client{Timeout: 30 * time.Second},
		limiter: NewRateLimiter(RateLimiterConfig{
			PerSecond: 16,   // 80% of Name.com's 20/sec limit
			PerHour:   2400, // 80% of Name.com's 3000/hr limit
		}),
		cb: NewCircuitBreaker(CircuitBreakerConfig{
			FailThreshold: 5,
			ResetTimeout:  30 * time.Second,
			Name:          "namecom",
		}),
	}
}

// ═══════════════════════════════════════════════════════════════════
// HTTP helpers
// ═══════════════════════════════════════════════════════════════════

// namecomAPIError represents a Name.com API error response.
type namecomAPIError struct {
	Message string `json:"message"`
	Details string `json:"details"`
}

func (e *namecomAPIError) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("%s: %s", e.Message, e.Details)
	}
	return e.Message
}

// doJSON executes a rate-limited, circuit-broken HTTP request against the Name.com API.
func (nc *NameCom) doJSON(ctx context.Context, method, path string, reqBody, respBody any) error {
	// 1. Circuit breaker check.
	if err := nc.cb.Allow(); err != nil {
		return err
	}

	// 2. Rate limiter — blocks until slot available or ctx cancelled.
	if err := nc.limiter.Wait(ctx); err != nil {
		return err
	}

	// 3. Build request.
	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, nc.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.SetBasicAuth(nc.username, nc.token)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// 4. Execute.
	resp, err := nc.client.Do(req)
	if err != nil {
		nc.cb.RecordFailure()
		return fmt.Errorf("name.com request failed: %w", err)
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		nc.cb.RecordFailure()
		return fmt.Errorf("read response: %w", err)
	}

	// 5. Handle errors.
	if resp.StatusCode == http.StatusTooManyRequests {
		nc.cb.RecordFailure()
		return &namecomRateLimitError{retryAfter: parseRetryAfter(resp)}
	}
	if resp.StatusCode >= 500 {
		nc.cb.RecordFailure()
		return fmt.Errorf("name.com server error %d: %s", resp.StatusCode, string(respData))
	}
	if resp.StatusCode >= 400 {
		nc.cb.RecordSuccess() // 4xx is not a server failure
		var apiErr namecomAPIError
		if json.Unmarshal(respData, &apiErr) == nil && apiErr.Message != "" {
			return fmt.Errorf("name.com API %d: %s", resp.StatusCode, apiErr.Error())
		}
		return fmt.Errorf("name.com API %d: %s", resp.StatusCode, string(respData))
	}

	// 6. Success.
	nc.cb.RecordSuccess()
	if respBody != nil && len(respData) > 0 {
		if err := json.Unmarshal(respData, respBody); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// namecomRateLimitError signals a 429 with optional Retry-After.
type namecomRateLimitError struct {
	retryAfter time.Duration
}

func (e *namecomRateLimitError) Error() string {
	return fmt.Sprintf("name.com rate limited (429), retry after %s", e.retryAfter)
}

func parseRetryAfter(resp *http.Response) time.Duration {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 2 * time.Second
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}
	return 2 * time.Second
}

// doJSONWithRetry wraps doJSON with exponential backoff retry for 429.
func (nc *NameCom) doJSONWithRetry(ctx context.Context, method, path string, reqBody, respBody any) error {
	var lastErr error
	for attempt := 0; attempt < namecomMaxRetries; attempt++ {
		err := nc.doJSON(ctx, method, path, reqBody, respBody)
		if err == nil {
			return nil
		}

		lastErr = err

		// Only retry on rate limit errors.
		rlErr, isRateLimit := err.(*namecomRateLimitError)
		if !isRateLimit {
			return err
		}

		wait := rlErr.retryAfter
		if wait == 0 {
			wait = namecomBaseBackoff * time.Duration(math.Pow(2, float64(attempt)))
		}

		slog.Warn("namecom rate limited, retrying",
			"attempt", attempt+1,
			"max_attempts", namecomMaxRetries,
			"wait", wait,
		)

		select {
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled: %w", ctx.Err())
		case <-time.After(wait):
		}
	}
	return fmt.Errorf("name.com request failed after %d retries: %w", namecomMaxRetries, lastErr)
}

// ═══════════════════════════════════════════════════════════════════
// DomainRegistrar interface implementation
// ═══════════════════════════════════════════════════════════════════

// SearchDomains checks availability for up to 50 domains per batch.
func (nc *NameCom) SearchDomains(ctx context.Context, domains []string) ([]DomainSearchResult, error) {
	var results []DomainSearchResult

	const batchSize = 50
	for i := 0; i < len(domains); i += batchSize {
		end := i + batchSize
		if end > len(domains) {
			end = len(domains)
		}
		batch := domains[i:end]

		reqBody := map[string]any{"domainNames": batch}
		var resp struct {
			Results []struct {
				DomainName    string `json:"domainName"`
				Purchasable   bool   `json:"purchasable"`
				Premium       bool   `json:"premium"`
				PurchasePrice struct {
					Price string `json:"price"`
				} `json:"purchasePrice"`
				RenewalPrice struct {
					Price string `json:"price"`
				} `json:"renewalPrice"`
			} `json:"results"`
		}

		if err := nc.doJSONWithRetry(ctx, "POST", "/domains:checkAvailability", reqBody, &resp); err != nil {
			return nil, fmt.Errorf("check availability: %w", err)
		}

		for _, r := range resp.Results {
			price, _ := strconv.ParseFloat(r.PurchasePrice.Price, 64)
			renewal, _ := strconv.ParseFloat(r.RenewalPrice.Price, 64)
			results = append(results, DomainSearchResult{
				Domain:     r.DomainName,
				Available:  r.Purchasable,
				Premium:    r.Premium,
				PriceUSD:   price,
				RenewalUSD: renewal,
			})
		}
	}
	return results, nil
}

// PurchaseDomain registers a new domain via Name.com.
func (nc *NameCom) PurchaseDomain(ctx context.Context, opts DomainPurchaseOpts) (*DomainPurchaseResult, error) {
	contact := namecomContact(opts.Contacts)
	reqBody := map[string]any{
		"domain": map[string]any{
			"domainName":       opts.Domain,
			"autorenewEnabled": opts.AutoRenew,
		},
		"years": opts.Years,
		"contacts": map[string]any{
			"registrant": contact,
			"admin":      contact,
			"tech":       contact,
			"billing":    contact,
		},
		"privacyEnabled": true,
	}

	var resp struct {
		Domain struct {
			DomainName  string   `json:"domainName"`
			ExpireDate  string   `json:"expireDate"`
			Nameservers []string `json:"nameservers"`
		} `json:"domain"`
		Order struct {
			OrderID int `json:"orderId"`
		} `json:"order"`
	}

	if err := nc.doJSONWithRetry(ctx, "POST", "/domains", reqBody, &resp); err != nil {
		return nil, fmt.Errorf("register domain: %w", err)
	}

	expires, _ := time.Parse(time.RFC3339, resp.Domain.ExpireDate)
	return &DomainPurchaseResult{
		Domain:      resp.Domain.DomainName,
		ExpiresAt:   expires,
		ProviderID:  resp.Domain.DomainName,
		Nameservers: resp.Domain.Nameservers,
	}, nil
}

// RenewDomain renews an existing domain.
func (nc *NameCom) RenewDomain(ctx context.Context, domain string, years int) (*DomainPurchaseResult, error) {
	reqBody := map[string]any{"years": years}

	var resp struct {
		Domain struct {
			DomainName string `json:"domainName"`
			ExpireDate string `json:"expireDate"`
		} `json:"domain"`
	}

	path := fmt.Sprintf("/domains/%s:renew", domain)
	if err := nc.doJSONWithRetry(ctx, "POST", path, reqBody, &resp); err != nil {
		return nil, fmt.Errorf("renew domain: %w", err)
	}

	expires, _ := time.Parse(time.RFC3339, resp.Domain.ExpireDate)
	return &DomainPurchaseResult{
		Domain:     resp.Domain.DomainName,
		ExpiresAt:  expires,
		ProviderID: resp.Domain.DomainName,
	}, nil
}

// LockDomain enables registrar lock to prevent unauthorized transfers.
func (nc *NameCom) LockDomain(ctx context.Context, domain string) error {
	path := fmt.Sprintf("/domains/%s:lock", domain)
	return nc.doJSONWithRetry(ctx, "POST", path, nil, nil)
}

// UnlockDomain disables registrar lock (required before transfer).
func (nc *NameCom) UnlockDomain(ctx context.Context, domain string) error {
	path := fmt.Sprintf("/domains/%s:unlock", domain)
	return nc.doJSONWithRetry(ctx, "POST", path, nil, nil)
}

// CreateDNSRecord adds a DNS record for a domain.
func (nc *NameCom) CreateDNSRecord(ctx context.Context, domain string, rec RegistrarDNSRecord) (*RegistrarDNSRecord, error) {
	reqBody := map[string]any{
		"type":   rec.Type,
		"host":   rec.Host,
		"answer": rec.Value,
		"ttl":    rec.TTL,
	}
	if rec.Prio > 0 {
		reqBody["priority"] = rec.Prio
	}

	var resp struct {
		ID       int    `json:"id"`
		Type     string `json:"type"`
		Host     string `json:"host"`
		Answer   string `json:"answer"`
		TTL      int    `json:"ttl"`
		Priority int    `json:"priority"`
	}

	path := fmt.Sprintf("/domains/%s/records", domain)
	if err := nc.doJSONWithRetry(ctx, "POST", path, reqBody, &resp); err != nil {
		return nil, fmt.Errorf("create DNS record: %w", err)
	}

	return &RegistrarDNSRecord{
		ID:    resp.ID,
		Type:  resp.Type,
		Host:  resp.Host,
		Value: resp.Answer,
		TTL:   resp.TTL,
		Prio:  resp.Priority,
	}, nil
}

// ListDNSRecords returns all DNS records for a domain.
func (nc *NameCom) ListDNSRecords(ctx context.Context, domain string) ([]RegistrarDNSRecord, error) {
	var resp struct {
		Records []struct {
			ID       int    `json:"id"`
			Type     string `json:"type"`
			Host     string `json:"host"`
			Answer   string `json:"answer"`
			TTL      int    `json:"ttl"`
			Priority int    `json:"priority"`
		} `json:"records"`
	}

	path := fmt.Sprintf("/domains/%s/records", domain)
	if err := nc.doJSONWithRetry(ctx, "GET", path, nil, &resp); err != nil {
		return nil, fmt.Errorf("list DNS records: %w", err)
	}

	records := make([]RegistrarDNSRecord, len(resp.Records))
	for i, r := range resp.Records {
		records[i] = RegistrarDNSRecord{
			ID:    r.ID,
			Type:  r.Type,
			Host:  r.Host,
			Value: r.Answer,
			TTL:   r.TTL,
			Prio:  r.Priority,
		}
	}
	return records, nil
}

// DeleteDNSRecord removes a DNS record by ID.
func (nc *NameCom) DeleteDNSRecord(ctx context.Context, domain string, recordID int) error {
	path := fmt.Sprintf("/domains/%s/records/%d", domain, recordID)
	return nc.doJSONWithRetry(ctx, "DELETE", path, nil, nil)
}

// ListDomains returns all domains in the account.
func (nc *NameCom) ListDomains(ctx context.Context) ([]DomainInfo, error) {
	var allDomains []DomainInfo
	page := 1

	for {
		var resp struct {
			Domains []struct {
				DomainName       string `json:"domainName"`
				ExpireDate       string `json:"expireDate"`
				AutorenewEnabled bool   `json:"autorenewEnabled"`
				Locked           bool   `json:"locked"`
			} `json:"domains"`
			NextPage int `json:"nextPage"`
		}

		path := fmt.Sprintf("/domains?page=%d&perPage=100", page)
		if err := nc.doJSONWithRetry(ctx, "GET", path, nil, &resp); err != nil {
			return nil, fmt.Errorf("list domains: %w", err)
		}

		for _, d := range resp.Domains {
			expires, _ := time.Parse(time.RFC3339, d.ExpireDate)
			allDomains = append(allDomains, DomainInfo{
				Domain:    d.DomainName,
				ExpiresAt: expires,
				AutoRenew: d.AutorenewEnabled,
				Locked:    d.Locked,
			})
		}

		if resp.NextPage == 0 {
			break
		}
		page = resp.NextPage
	}
	return allDomains, nil
}

// ═══════════════════════════════════════════════════════════════════
// Helpers
// ═══════════════════════════════════════════════════════════════════

func namecomContact(c DomainContacts) map[string]any {
	contact := map[string]any{
		"firstName": c.FirstName,
		"lastName":  c.LastName,
		"email":     c.Email,
		"phone":     c.Phone,
		"address1":  c.Address1,
		"city":      c.City,
		"state":     c.State,
		"zip":       c.PostalCode,
		"country":   c.Country,
	}
	if c.Organization != "" {
		contact["organization"] = c.Organization
	}
	return contact
}
