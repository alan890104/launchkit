package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// UpstashFetcher implements ConsumptionFetcher for Upstash Redis.
// Upstash uniquely provides total_monthly_billing (actual USD), enabling
// pass-through billing with markup. Since Upstash only exposes cumulative
// monthly totals, this fetcher uses metering_checkpoints for delta calculation.
type UpstashFetcher struct {
	email  string
	apiKey string
	client *http.Client
	db     *pgxpool.Pool // needed for checkpoint reads (delta calculation)
}

var _ ConsumptionFetcher = (*UpstashFetcher)(nil)

func NewUpstashFetcher(email, apiKey string, db *pgxpool.Pool) *UpstashFetcher {
	return &UpstashFetcher{
		email:  email,
		apiKey: apiKey,
		client: &http.Client{Timeout: 30 * time.Second},
		db:     db,
	}
}

func (f *UpstashFetcher) ProviderName() string { return "upstash" }

func (f *UpstashFetcher) FetchConsumption(ctx context.Context, resources []ActiveResource, start, end time.Time) ([]ConsumptionRecord, error) {
	var records []ConsumptionRecord

	for _, res := range resources {
		dbID := OutputStr(res.Outputs, "provider_id")
		if dbID == "" {
			continue
		}

		stats, err := f.fetchStats(ctx, dbID)
		if err != nil {
			slog.Warn("upstash stats fetch failed", "database_id", dbID, "error", err)
			continue
		}

		// Delta calculation: Upstash returns cumulative monthly totals.
		// We subtract the last checkpoint to get the delta for this period.
		delta := f.calculateDelta(ctx, res.URN, stats)

		if delta.billing == 0 && delta.requests == 0 {
			continue
		}

		directCost := delta.billing
		records = append(records, ConsumptionRecord{
			ResourceURN:  res.URN,
			ProjectID:    res.ProjectID,
			ResourceType: "cache",
			Provider:     "upstash",
			ProviderID:   dbID,
			PeriodStart:  start,
			PeriodEnd:    end,
			DirectCost:   &directCost,
			Metrics: map[string]float64{
				"monthly_requests":        delta.requests,
				"monthly_bandwidth_bytes": delta.bandwidth,
				"monthly_storage_bytes":   stats.MonthlyStorage,
				"monthly_billing_usd":     delta.billing,
				// Store cumulative values for checkpoint update
				"_cumulative_requests":  stats.MonthlyRequests,
				"_cumulative_bandwidth": stats.MonthlyBandwidth,
				"_cumulative_billing":   stats.MonthlyBilling,
			},
		})
	}

	return records, nil
}

type upstashStats struct {
	MonthlyRequests  float64
	MonthlyBandwidth float64
	MonthlyBilling   float64
	MonthlyStorage   float64
}

type upstashDelta struct {
	requests  float64
	bandwidth float64
	billing   float64
}

func (f *UpstashFetcher) fetchStats(ctx context.Context, databaseID string) (upstashStats, error) {
	apiURL := fmt.Sprintf("https://api.upstash.com/v2/redis/stats/%s", url.PathEscape(databaseID))

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return upstashStats{}, err
	}
	req.SetBasicAuth(f.email, f.apiKey)

	resp, err := f.client.Do(req)
	if err != nil {
		return upstashStats{}, fmt.Errorf("upstash API request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return upstashStats{}, fmt.Errorf("upstash API %d: %s", resp.StatusCode, string(body))
	}

	var apiResp struct {
		TotalMonthlyRequests  float64 `json:"total_monthly_requests"`
		TotalMonthlyBandwidth float64 `json:"total_monthly_bandwidth"`
		TotalMonthlyBilling   float64 `json:"total_monthly_billing"`
		TotalMonthlyStorage   float64 `json:"total_monthly_storage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return upstashStats{}, fmt.Errorf("decode upstash response: %w", err)
	}

	// Guard against negative values (Upstash credits/refunds) — don't let
	// negative billing flow into cost calculation which would increase user credit.
	if apiResp.TotalMonthlyBilling < 0 {
		slog.Warn("upstash returned negative billing, clamping to 0",
			"database_id", databaseID, "billing", apiResp.TotalMonthlyBilling)
		apiResp.TotalMonthlyBilling = 0
	}

	return upstashStats{
		MonthlyRequests:  apiResp.TotalMonthlyRequests,
		MonthlyBandwidth: apiResp.TotalMonthlyBandwidth,
		MonthlyBilling:   apiResp.TotalMonthlyBilling,
		MonthlyStorage:   apiResp.TotalMonthlyStorage,
	}, nil
}

func (f *UpstashFetcher) calculateDelta(ctx context.Context, resourceURN string, current upstashStats) upstashDelta {
	// Read last checkpoint
	var lastRaw string
	err := f.db.QueryRow(ctx, `
		SELECT cumulative::text FROM metering_checkpoints
		WHERE resource_urn = $1 AND provider = 'upstash'
	`, resourceURN).Scan(&lastRaw)

	if err != nil {
		// No prior checkpoint: first metering cycle, use current as full delta
		return upstashDelta{
			requests:  current.MonthlyRequests,
			bandwidth: current.MonthlyBandwidth,
			billing:   current.MonthlyBilling,
		}
	}

	var last map[string]float64
	if err := json.Unmarshal([]byte(lastRaw), &last); err != nil {
		slog.Warn("corrupt upstash checkpoint, treating as first metering cycle",
			"urn", resourceURN, "error", err)
		return upstashDelta{
			requests:  current.MonthlyRequests,
			bandwidth: current.MonthlyBandwidth,
			billing:   current.MonthlyBilling,
		}
	}

	lastRequests := last["_cumulative_requests"]
	lastBandwidth := last["_cumulative_bandwidth"]
	lastBilling := last["_cumulative_billing"]

	// Delta = current - last checkpoint.
	// If current < last, a monthly reset happened (new month started).
	// In that case, current IS the full delta since the reset.
	delta := upstashDelta{
		requests:  safeDelta(current.MonthlyRequests, lastRequests),
		bandwidth: safeDelta(current.MonthlyBandwidth, lastBandwidth),
		billing:   safeDelta(current.MonthlyBilling, lastBilling),
	}

	return delta
}

// safeDelta computes current - last, handling monthly resets.
func safeDelta(current, last float64) float64 {
	if current >= last {
		return current - last
	}
	// Monthly reset: current < last means the counter reset to 0.
	// Current value is the full delta since the reset.
	return current
}
