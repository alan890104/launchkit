package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// NeonFetcher implements ConsumptionFetcher for Neon Postgres.
// Uses the Neon Consumption History API v2 which provides billing-accurate data.
type NeonFetcher struct {
	apiKey string
	client *http.Client
}

var _ ConsumptionFetcher = (*NeonFetcher)(nil)

func NewNeonFetcher(apiKey string) *NeonFetcher {
	return &NeonFetcher{
		apiKey: apiKey,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (f *NeonFetcher) ProviderName() string { return "neon" }

func (f *NeonFetcher) FetchConsumption(ctx context.Context, resources []ActiveResource, periodStart, periodEnd time.Time) ([]ConsumptionRecord, error) {
	// Neon returns data in hourly buckets. Our metering window [H-65m, H-5m] doesn't
	// align to hour boundaries, so querying with raw timestamps would return overlapping
	// buckets across consecutive cycles (e.g., the 14:00 bucket appears in both the
	// 14:55 and 15:55 queries). Fix: snap to the nearest complete hour boundaries.
	// This ensures each Neon hourly bucket is fetched by exactly one metering cycle.
	alignedStart := periodStart.Truncate(time.Hour)
	alignedEnd := periodEnd.Truncate(time.Hour)
	if !alignedEnd.After(alignedStart) {
		alignedEnd = alignedStart.Add(time.Hour)
	}

	// Collect Neon project IDs → LaunchKit resource mapping
	type mapping struct {
		resource ActiveResource
		neonID   string
	}
	var mappings []mapping
	for _, res := range resources {
		neonID := OutputStr(res.Outputs, "provider_id")
		if neonID == "" {
			continue
		}
		mappings = append(mappings, mapping{resource: res, neonID: neonID})
	}
	if len(mappings) == 0 {
		return nil, nil
	}

	// Neon Consumption API supports batching up to 100 project IDs per request.
	var allRecords []ConsumptionRecord

	for i := 0; i < len(mappings); i += 100 {
		batchEnd := i + 100
		if batchEnd > len(mappings) {
			batchEnd = len(mappings)
		}
		batch := mappings[i:batchEnd]

		ids := make([]string, len(batch))
		for j, m := range batch {
			ids[j] = m.neonID
		}

		consumption, err := f.fetchConsumptionBatch(ctx, ids, alignedStart, alignedEnd)
		if err != nil {
			return nil, fmt.Errorf("neon consumption batch: %w", err)
		}

		// Map Neon project IDs back to LaunchKit resources
		for _, m := range batch {
			data, ok := consumption[m.neonID]
			if !ok {
				continue
			}

			if data.computeUnitSeconds == 0 && data.storageBytesMonth == 0 {
				continue
			}

			allRecords = append(allRecords, ConsumptionRecord{
				ResourceURN:  m.resource.URN,
				ProjectID:    m.resource.ProjectID,
				ResourceType: "database",
				Provider:     "neon",
				ProviderID:   m.neonID,
				Region:       OutputStr(m.resource.Outputs, "region"),
				PeriodStart:  alignedStart,
				PeriodEnd:    alignedEnd,
				Metrics: map[string]float64{
					"compute_unit_seconds": data.computeUnitSeconds,
					// Neon returns byte-hours, not raw bytes.
					// To convert to GB-month: byte_hours / (1024^3) / hours_in_month
					// But since the API already gives us the right unit for their billing,
					// we store it as-is and let CalculateCost handle the conversion.
					"storage_byte_hours":             data.storageBytesMonth,
					"public_network_transfer_bytes":  data.publicNetworkBytes,
					"private_network_transfer_bytes": data.privateNetworkBytes,
				},
			})
		}
	}

	return allRecords, nil
}

type neonConsumptionData struct {
	computeUnitSeconds  float64
	storageBytesMonth   float64 // byte-hours (Neon's unit for storage billing)
	publicNetworkBytes  float64 // public egress bytes ($0.09/GB)
	privateNetworkBytes float64 // private/VPC egress bytes ($0.01/GB)
}

func (f *NeonFetcher) fetchConsumptionBatch(ctx context.Context, projectIDs []string, start, end time.Time) (map[string]neonConsumptionData, error) {
	params := url.Values{}
	params.Set("project_ids", strings.Join(projectIDs, ","))
	params.Set("from", start.Format(time.RFC3339))
	params.Set("to", end.Format(time.RFC3339))
	params.Set("granularity", "hourly")
	apiURL := "https://console.neon.tech/api/v2/consumption_history/v2/projects?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+f.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("neon API request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("neon API %d: %s", resp.StatusCode, string(body))
	}

	var apiResp struct {
		Projects []struct {
			ProjectID string `json:"project_id"`
			Periods   []struct {
				ComputeUnitSeconds          float64 `json:"compute_unit_seconds"`
				RootBranchBytesMonth        float64 `json:"root_branch_bytes_month"`
				ChildBranchBytesMonth       float64 `json:"child_branch_bytes_month"`
				PublicNetworkTransferBytes  float64 `json:"public_network_transfer_bytes"`
				PrivateNetworkTransferBytes float64 `json:"private_network_transfer_bytes"`
			} `json:"periods"`
		} `json:"projects"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("decode neon response: %w", err)
	}

	result := make(map[string]neonConsumptionData)
	for _, proj := range apiResp.Projects {
		var data neonConsumptionData
		for _, p := range proj.Periods {
			data.computeUnitSeconds += p.ComputeUnitSeconds
			data.storageBytesMonth += p.RootBranchBytesMonth + p.ChildBranchBytesMonth
			data.publicNetworkBytes += p.PublicNetworkTransferBytes
			data.privateNetworkBytes += p.PrivateNetworkTransferBytes
		}
		result[proj.ProjectID] = data
	}

	return result, nil
}
