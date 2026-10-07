package provider

import (
	"context"
	"time"
)

// ActiveResource is a simplified view of a resource_states row,
// passed to ConsumptionFetcher so it knows what to query.
type ActiveResource struct {
	URN          string
	ProjectID    string
	ResourceType string         // compute, database, cache, storage, static
	Provider     string         // cloud_run, ecs_fargate, neon, upstash, gcs, s3
	Outputs      map[string]any // parsed from JSONB outputs column
}

// ConsumptionRecord represents raw usage from a single resource over a time window.
type ConsumptionRecord struct {
	ResourceURN  string
	ProjectID    string
	ResourceType string // compute, database, cache, storage
	Provider     string // cloud_run, ecs_fargate, neon, upstash, gcs, s3
	ProviderID   string // external ID for the provider API
	Region       string // cloud region (e.g. "us-east4", "us-east-1") for per-region pricing
	PeriodStart  time.Time
	PeriodEnd    time.Time
	Metrics      map[string]float64 // metric_name → quantity
	DirectCost   *float64           // non-nil when provider gives $ directly (Upstash)
}

// ConsumptionFetcher retrieves usage data from a cloud provider for a time window.
// Each provider implements this interface. The fetcher receives active resources
// (from resource_states) and returns consumption records for each.
type ConsumptionFetcher interface {
	// FetchConsumption queries the provider API for usage in [start, end).
	// Returns one ConsumptionRecord per resource that had any usage.
	// Must be idempotent — calling twice for the same window returns the same data.
	FetchConsumption(ctx context.Context, resources []ActiveResource, start, end time.Time) ([]ConsumptionRecord, error)

	// ProviderName returns the provider identifier (e.g. "cloud_run", "neon").
	ProviderName() string
}

// OutputStr safely extracts a string value from a resource's Outputs map.
func OutputStr(outputs map[string]any, key string) string {
	if outputs == nil {
		return ""
	}
	v, ok := outputs[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
