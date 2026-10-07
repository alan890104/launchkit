package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const resendBaseURL = "https://api.resend.com"

// Compile-time interface guard.
var _ Email = (*Resend)(nil)

// Resend implements Email using the Resend API.
type Resend struct {
	apiKey string
	client *http.Client
}

func NewResend(apiKey string) *Resend {
	return &Resend{
		apiKey: apiKey,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// CreateDomain registers a domain with Resend and returns the DNS records
// the user must add for DKIM/SPF/DMARC verification.
func (r *Resend) CreateDomain(ctx context.Context, domain string) (*EmailDomainResult, error) {
	body := map[string]string{"name": domain}
	var resp resendDomainResponse
	if err := r.doJSON(ctx, http.MethodPost, "/domains", body, &resp); err != nil {
		return nil, fmt.Errorf("resend create domain: %w", err)
	}

	var records []DNSRecord
	for _, rec := range resp.Records {
		records = append(records, DNSRecord{
			Type:     rec.RecordType,
			Name:     rec.Name,
			Value:    rec.Value,
			Priority: rec.Priority,
			TTL:      rec.TTL,
		})
	}

	return &EmailDomainResult{
		ProviderID: resp.ID,
		Records:    records,
		Status:     resp.Status,
	}, nil
}

// VerifyDomain triggers a verification check and returns the current status.
func (r *Resend) VerifyDomain(ctx context.Context, domainID string) (*EmailDomainStatus, error) {
	// Trigger verification
	if err := r.doJSON(ctx, http.MethodPost, "/domains/"+domainID+"/verify", nil, nil); err != nil {
		return nil, fmt.Errorf("resend trigger verify: %w", err)
	}

	// Fetch current status
	var resp resendDomainResponse
	if err := r.doJSON(ctx, http.MethodGet, "/domains/"+domainID, nil, &resp); err != nil {
		return nil, fmt.Errorf("resend get domain: %w", err)
	}

	var checks []DNSRecordCheck
	for _, rec := range resp.Records {
		checks = append(checks, DNSRecordCheck{
			DNSRecord: DNSRecord{
				Type:     rec.RecordType,
				Name:     rec.Name,
				Value:    rec.Value,
				Priority: rec.Priority,
				TTL:      rec.TTL,
			},
			Verified: rec.Status == "verified",
		})
	}

	return &EmailDomainStatus{
		Status:  resp.Status,
		Records: checks,
	}, nil
}

// CreateAPIKey creates a Resend API key scoped to the given domain.
func (r *Resend) CreateAPIKey(ctx context.Context, name, domainID string) (string, error) {
	body := map[string]string{
		"name":       name,
		"permission": "sending_access",
		"domain_id":  domainID,
	}
	var resp struct {
		ID  string `json:"id"`
		Key string `json:"token"`
	}
	if err := r.doJSON(ctx, http.MethodPost, "/api-keys", body, &resp); err != nil {
		return "", fmt.Errorf("resend create api key: %w", err)
	}
	return resp.Key, nil
}

// DeleteDomain removes a domain from Resend.
func (r *Resend) DeleteDomain(ctx context.Context, domainID string) error {
	if err := r.doJSON(ctx, http.MethodDelete, "/domains/"+domainID, nil, nil); err != nil {
		return fmt.Errorf("resend delete domain: %w", err)
	}
	return nil
}

// ── Resend API types ─────────────────────────────────────────────

type resendDomainResponse struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Status  string            `json:"status"` // "not_started" | "pending" | "verified" | "failed"
	Records []resendDNSRecord `json:"records"`
}

type resendDNSRecord struct {
	RecordType string `json:"record"` // "MX", "TXT", "CNAME"
	Name       string `json:"name"`
	Value      string `json:"value"`
	Priority   int    `json:"priority,omitempty"`
	TTL        int    `json:"ttl,omitempty"`
	Status     string `json:"status"` // "not_started" | "verified"
}

type resendErrorResponse struct {
	StatusCode int    `json:"statusCode"`
	Message    string `json:"message"`
	Name       string `json:"name"`
}

// ── HTTP helper ──────────────────────────────────────────────────

func (r *Resend) doJSON(ctx context.Context, method, path string, reqBody any, respBody any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, resendBaseURL+path, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var apiErr resendErrorResponse
		if json.Unmarshal(respData, &apiErr) == nil && apiErr.Message != "" {
			return fmt.Errorf("resend API %d: %s", resp.StatusCode, apiErr.Message)
		}
		return fmt.Errorf("resend API %d: %s", resp.StatusCode, string(respData))
	}

	if respBody != nil && len(respData) > 0 {
		if err := json.Unmarshal(respData, respBody); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
