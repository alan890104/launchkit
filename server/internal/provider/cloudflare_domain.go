package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Compile-time interface guard.
var _ DomainMapper = (*CloudflarePages)(nil)

// MapDomain adds a custom domain to a Cloudflare Pages project.
// Returns DNS records the user must add (CNAME to {project}.pages.dev).
func (cf *CloudflarePages) MapDomain(ctx context.Context, opts MapDomainOpts) (*MapDomainResult, error) {
	url := fmt.Sprintf("%s/%s/domains", cf.projectsURL(), opts.ServiceName)
	body := map[string]string{"name": opts.Domain}

	resp, err := cf.doJSON(ctx, "POST", url, body)
	if err != nil {
		return nil, fmt.Errorf("add custom domain: %w", err)
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// 409 = domain already exists — that's fine, fetch it
	if resp.StatusCode == 409 {
		return cf.getExistingDomain(ctx, opts)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("cloudflare add domain %d: %s", resp.StatusCode, string(respData))
	}

	var apiResp struct {
		Result struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respData, &apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// Build CNAME record pointing to the Pages project
	records := cf.buildDomainRecords(opts.Domain, opts.ServiceName)

	return &MapDomainResult{Records: records}, nil
}

// UnmapDomain removes a custom domain from a Cloudflare Pages project.
func (cf *CloudflarePages) UnmapDomain(ctx context.Context, opts UnmapDomainOpts) error {
	url := fmt.Sprintf("%s/%s/domains/%s", cf.projectsURL(), opts.ServiceName, opts.Domain)

	resp, err := cf.doJSON(ctx, "DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("remove custom domain: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode != 404 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("cloudflare remove domain %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// VerifyDomain checks the status of a custom domain on Cloudflare Pages.
func (cf *CloudflarePages) VerifyDomain(ctx context.Context, opts VerifyDomainOpts) (*DomainStatus, error) {
	url := fmt.Sprintf("%s/%s/domains/%s", cf.projectsURL(), opts.ServiceName, opts.Domain)

	resp, err := cf.doGet(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("get domain status: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("cloudflare get domain %d: %s", resp.StatusCode, string(body))
	}

	var apiResp struct {
		Result struct {
			Name             string `json:"name"`
			Status           string `json:"status"`            // "active" | "pending" | "deactivated"
			ValidationStatus string `json:"validation_status"` // "active" | "pending"
			SSLStatus        string `json:"ssl_status"`        // "active" | "pending" | "initializing"
			VerificationData struct {
				Status string `json:"status"`
			} `json:"verification_data"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	r := apiResp.Result
	status := &DomainStatus{
		DNSVerified:   r.Status == "active" || r.ValidationStatus == "active",
		OwnerVerified: r.Status == "active",
		SSLStatus:     mapCFSSLStatus(r.SSLStatus),
	}
	return status, nil
}

func (cf *CloudflarePages) getExistingDomain(ctx context.Context, opts MapDomainOpts) (*MapDomainResult, error) {
	records := cf.buildDomainRecords(opts.Domain, opts.ServiceName)
	return &MapDomainResult{Records: records}, nil
}

func (cf *CloudflarePages) buildDomainRecords(domain, projectName string) []DNSRecord {
	target := projectName + ".pages.dev"

	// Determine the record name (subdomain portion)
	parts := strings.SplitN(domain, ".", 2)
	name := domain
	if len(parts) > 2 {
		name = parts[0] // subdomain like "www" or "app"
	}

	return []DNSRecord{
		{
			Type:  "CNAME",
			Name:  name,
			Value: target,
		},
	}
}

func mapCFSSLStatus(s string) string {
	switch s {
	case "active":
		return "active"
	case "initializing":
		return "provisioning"
	default:
		return "pending"
	}
}
