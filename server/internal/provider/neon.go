package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Compile-time interface guard.
var _ Database = (*Neon)(nil)

const neonAPIBase = "https://console.neon.tech/api/v2"

// Neon implements Database using the Neon API.
type Neon struct {
	apiKey string
	client *http.Client
}

func NewNeon(apiKey string) *Neon {
	return &Neon{
		apiKey: apiKey,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (n *Neon) Create(ctx context.Context, opts CreateDBOpts) (*CreateDBResult, error) {
	// Idempotency: check if a project with this name already exists.
	// Naming convention: launchkit-{projectID}-{type}
	// If it exists, return the existing project instead of creating a duplicate.
	// This is required for crash recovery — orchestrator re-runs won't create orphans.
	existing, err := n.findByName(ctx, opts.Name)
	if err == nil && existing != nil {
		return existing, nil
	}

	body := map[string]any{
		"project": map[string]any{
			"name":       opts.Name,
			"region_id":  neonRegion(opts.Region),
			"pg_version": 17,
		},
	}

	resp, err := n.doRequest(ctx, "POST", "/projects", body)
	if err != nil {
		return nil, fmt.Errorf("create Neon project: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create Neon project: status %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
		ConnectionURIs []struct {
			ConnectionURI string `json:"connection_uri"`
		} `json:"connection_uris"`
		Databases []struct {
			Name string `json:"name"`
		} `json:"databases"`
		Roles []struct {
			Name     string `json:"name"`
			Password string `json:"password"`
		} `json:"roles"`
		Endpoints []struct {
			Host string `json:"host"`
		} `json:"endpoints"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode Neon response: %w", err)
	}

	connURL := ""
	if len(result.ConnectionURIs) > 0 {
		connURL = result.ConnectionURIs[0].ConnectionURI
	}

	host := ""
	if len(result.Endpoints) > 0 {
		host = result.Endpoints[0].Host
	}

	dbName := "neondb"
	if len(result.Databases) > 0 {
		dbName = result.Databases[0].Name
	}

	user := ""
	password := ""
	if len(result.Roles) > 0 {
		user = result.Roles[0].Name
		password = result.Roles[0].Password
	}

	return &CreateDBResult{
		ProviderID:    result.Project.ID,
		ConnectionURL: connURL,
		Host:          host,
		Database:      dbName,
		User:          user,
		Password:      password,
	}, nil
}

func (n *Neon) Delete(ctx context.Context, providerID string) error {
	resp, err := n.doRequest(ctx, "DELETE", "/projects/"+providerID, nil)
	if err != nil {
		return fmt.Errorf("delete Neon project: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete Neon project: status %d: %s", resp.StatusCode, respBody)
	}

	return nil
}

func (n *Neon) doRequest(ctx context.Context, method, path string, body any) (*http.Response, error) {
	return n.doRequestWithRetry(ctx, method, path, body, 4)
}

// doRequestWithRetry executes a Neon API request with exponential backoff on 429.
// maxAttempts: total attempts including the first try (1 = no retry).
func (n *Neon) doRequestWithRetry(ctx context.Context, method, path string, body any, maxAttempts int) (*http.Response, error) {
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		var bodyReader io.Reader
		if bodyBytes != nil {
			bodyReader = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, neonAPIBase+path, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+n.apiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := n.client.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}

		// 429: read and discard body, then back off.
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		// Honour Retry-After header if present.
		wait := neonBackoff(attempt)
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil {
				wait = time.Duration(secs) * time.Second
			}
		}

		slog.Warn("neon API rate limited, retrying",
			"attempt", attempt+1, "max", maxAttempts, "wait", wait)
		lastErr = fmt.Errorf("neon API 429 rate limited")

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}

	return nil, fmt.Errorf("neon API rate limited after %d attempts: %w", maxAttempts, lastErr)
}

// neonBackoff returns the wait duration for attempt n (0-indexed): 1s, 2s, 4s, 8s.
func neonBackoff(attempt int) time.Duration {
	base := time.Second
	for i := 0; i < attempt; i++ {
		base *= 2
	}
	if base > 30*time.Second {
		base = 30 * time.Second
	}
	return base
}

// findByName searches for an existing Neon project by name (idempotency).
// It paginates through all pages using cursor-based pagination.
func (n *Neon) findByName(ctx context.Context, name string) (*CreateDBResult, error) {
	cursor := ""
	for {
		path := "/projects?limit=100"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}

		resp, err := n.doRequest(ctx, "GET", path, nil)
		if err != nil {
			return nil, err
		}

		var listResult struct {
			Projects []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"projects"`
			Cursor string `json:"cursor"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&listResult); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		for _, p := range listResult.Projects {
			if p.Name == name {
				return n.getProjectDetails(ctx, p.ID)
			}
		}

		// No more pages
		if listResult.Cursor == "" || len(listResult.Projects) == 0 {
			break
		}
		cursor = listResult.Cursor
	}
	return nil, fmt.Errorf("not found")
}

// getProjectDetails fetches the full connection details for an existing Neon project.
func (n *Neon) getProjectDetails(ctx context.Context, projectID string) (*CreateDBResult, error) {
	resp, err := n.doRequest(ctx, "GET", "/projects/"+projectID, nil)
	if err != nil {
		return nil, fmt.Errorf("get project details: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
		ConnectionURIs []struct {
			ConnectionURI string `json:"connection_uri"`
		} `json:"connection_uris"`
		Databases []struct {
			Name string `json:"name"`
		} `json:"databases"`
		Roles []struct {
			Name     string `json:"name"`
			Password string `json:"password"`
		} `json:"roles"`
		Endpoints []struct {
			Host string `json:"host"`
		} `json:"endpoints"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode project details: %w", err)
	}

	connURL := ""
	if len(result.ConnectionURIs) > 0 {
		connURL = result.ConnectionURIs[0].ConnectionURI
	}
	host := ""
	if len(result.Endpoints) > 0 {
		host = result.Endpoints[0].Host
	}
	dbName := "neondb"
	if len(result.Databases) > 0 {
		dbName = result.Databases[0].Name
	}
	user := ""
	password := ""
	if len(result.Roles) > 0 {
		user = result.Roles[0].Name
		password = result.Roles[0].Password
	}

	return &CreateDBResult{
		ProviderID:    projectID,
		ConnectionURL: connURL,
		Host:          host,
		Database:      dbName,
		User:          user,
		Password:      password,
	}, nil
}

// neonRegion maps our region naming to Neon's region IDs.
// Supports both GCP region names and AWS region names.
func neonRegion(region string) string {
	regionMap := map[string]string{
		// GCP regions
		"us-east4":        "aws-us-east-1",
		"us-central1":     "aws-us-east-1",
		"us-west1":        "aws-us-west-2",
		"europe-west1":    "aws-eu-central-1",
		"asia-northeast1": "aws-ap-northeast-1",
		"asia-southeast1": "aws-ap-southeast-1",
		// AWS regions (pass through with prefix)
		"us-east-1":      "aws-us-east-1",
		"us-east-2":      "aws-us-east-2",
		"us-west-2":      "aws-us-west-2",
		"eu-central-1":   "aws-eu-central-1",
		"eu-west-2":      "aws-eu-west-2",
		"ap-southeast-1": "aws-ap-southeast-1",
		"ap-southeast-2": "aws-ap-southeast-2",
		"ap-northeast-1": "aws-ap-northeast-1",
		"sa-east-1":      "aws-sa-east-1",
	}
	if r, ok := regionMap[region]; ok {
		return r
	}
	// If already prefixed with "aws-", pass through
	if strings.HasPrefix(region, "aws-") {
		return region
	}
	return "aws-us-east-1" // safe default
}
