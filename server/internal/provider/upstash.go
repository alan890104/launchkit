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

const upstashBaseURL = "https://api.upstash.com/v2/redis"

// CreateRedisOpts configures a new managed Redis instance.
type CreateRedisOpts struct {
	Name   string
	Region string
}

// CreateRedisResult is returned after provisioning Redis.
type CreateRedisResult struct {
	ProviderID    string // Upstash database ID
	Endpoint      string // Redis endpoint (host:port)
	Password      string
	ConnectionURL string // redis://default:xxx@xxx.upstash.io:6379
}

// Cache provisions and manages Redis instances.
type Cache interface {
	Create(ctx context.Context, opts CreateRedisOpts) (*CreateRedisResult, error)
	Delete(ctx context.Context, providerID string) error
}

// Upstash implements Cache using Upstash Redis.
type Upstash struct {
	apiKey string
	email  string
	client *http.Client
}

// Compile-time guard.
var _ Cache = (*Upstash)(nil)

func NewUpstash(email, apiKey string) *Upstash {
	return &Upstash{
		apiKey: apiKey,
		email:  email,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Create provisions a new Upstash Redis database.
func (u *Upstash) Create(ctx context.Context, opts CreateRedisOpts) (*CreateRedisResult, error) {
	body := map[string]any{
		"name":   opts.Name,
		"region": mapUpstashRegion(opts.Region),
		"tls":    true,
	}

	var resp upstashDatabaseResponse
	if err := u.doJSON(ctx, http.MethodPost, "/database", body, &resp); err != nil {
		return nil, fmt.Errorf("upstash create database: %w", err)
	}

	connURL := fmt.Sprintf("rediss://default:%s@%s:%d", resp.Password, resp.Endpoint, resp.Port)

	return &CreateRedisResult{
		ProviderID:    resp.DatabaseID,
		Endpoint:      fmt.Sprintf("%s:%d", resp.Endpoint, resp.Port),
		Password:      resp.Password,
		ConnectionURL: connURL,
	}, nil
}

// Delete removes an Upstash Redis database.
func (u *Upstash) Delete(ctx context.Context, providerID string) error {
	if err := u.doJSON(ctx, http.MethodDelete, "/database/"+providerID, nil, nil); err != nil {
		return fmt.Errorf("upstash delete database: %w", err)
	}
	return nil
}

// ── Upstash API types ────────────────────────────────────────────

type upstashDatabaseResponse struct {
	DatabaseID string `json:"database_id"`
	Endpoint   string `json:"endpoint"`
	Port       int    `json:"port"`
	Password   string `json:"password"`
	State      string `json:"state"`
}

// ── HTTP helper ──────────────────────────────────────────────────

func (u *Upstash) doJSON(ctx context.Context, method, path string, reqBody any, respBody any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, upstashBaseURL+path, bodyReader)
	if err != nil {
		return err
	}
	req.SetBasicAuth(u.email, u.apiKey)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("upstash API %d: %s", resp.StatusCode, string(respData))
	}

	if respBody != nil && len(respData) > 0 {
		if err := json.Unmarshal(respData, respBody); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// mapUpstashRegion maps LaunchKit regions to Upstash region codes.
func mapUpstashRegion(region string) string {
	switch {
	case region == "us-east4" || region == "us-east-1" || region == "":
		return "us-east-1"
	case region == "us-central1" || region == "us-west-2":
		return "us-west-1"
	case region == "europe-west1" || region == "eu-west-1":
		return "eu-west-1"
	case region == "asia-east1" || region == "ap-northeast-1":
		return "ap-northeast-1"
	default:
		return "us-east-1" // safe default
	}
}
