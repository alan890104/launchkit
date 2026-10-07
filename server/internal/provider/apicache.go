package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// APICache is a thin Upstash REST API client for LaunchKit-internal caching.
// If baseURL is empty it is a no-op (graceful degradation — callers fall through to DB).
type APICache struct {
	baseURL string // e.g. https://xxx.upstash.io (no trailing slash)
	token   string // Upstash REST token
	client  *http.Client
}

func NewAPICache(baseURL, token string) *APICache {
	if baseURL == "" {
		return &APICache{} // no-op mode
	}
	return &APICache{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: 2 * time.Second},
	}
}

func (c *APICache) enabled() bool { return c.baseURL != "" }

// Enabled reports whether this cache is active (non-empty baseURL).
// Exported so server.go and auth package can check without importing internal state.
func (c *APICache) Enabled() bool { return c.enabled() }

// Get retrieves a value. Returns ("", false, nil) on cache miss or if disabled.
func (c *APICache) Get(ctx context.Context, key string) (string, bool, error) {
	if !c.enabled() {
		return "", false, nil
	}

	url := fmt.Sprintf("%s/get/%s", c.baseURL, key)
	resp, err := c.do(ctx, url)
	if err != nil {
		// Treat network/timeout errors as cache miss — never fail the request.
		return "", false, nil
	}

	if resp.Result == nil {
		return "", false, nil
	}

	val, ok := resp.Result.(string)
	if !ok {
		return "", false, nil
	}
	return val, true, nil
}

// Set stores key=value with a TTL. No-op if disabled.
func (c *APICache) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if !c.enabled() {
		return nil
	}

	secs := int64(ttl.Seconds())
	if secs <= 0 {
		secs = 1
	}
	url := fmt.Sprintf("%s/setex/%s/%d/%s", c.baseURL, key, secs, value)
	_, err := c.do(ctx, url)
	if err != nil {
		// Graceful degradation — cache write failure is not fatal.
		return nil
	}
	return nil
}

// Del removes a key. No-op if disabled.
func (c *APICache) Del(ctx context.Context, key string) error {
	if !c.enabled() {
		return nil
	}

	url := fmt.Sprintf("%s/del/%s", c.baseURL, key)
	_, err := c.do(ctx, url)
	if err != nil {
		// Graceful degradation — cache delete failure is not fatal.
		return nil
	}
	return nil
}

// ── internal helpers ─────────────────────────────────────────────

type upstashRESTResponse struct {
	Result interface{} `json:"result"`
	Error  string      `json:"error,omitempty"`
}

// do issues a GET request to the Upstash REST API and decodes the response.
// Returns an error only on network/decode failures; a non-200 status is treated
// as a graceful no-op (cache miss / write fail) rather than a hard error.
func (c *APICache) do(ctx context.Context, url string) (*upstashRESTResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		// Non-200: treat as graceful miss, not a hard error.
		return &upstashRESTResponse{}, nil
	}

	var result upstashRESTResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
