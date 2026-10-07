package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	billing "cloud.google.com/go/billing/apiv1"
	billingpb "cloud.google.com/go/billing/apiv1/billingpb"
	"github.com/alan890104/launchkit/server/internal/provider"
	"google.golang.org/api/iterator"
)

// Margin applied on top of raw provider cost.
const defaultMargin = 0.35

// ─── Plan Definitions ────────────────────────────────────────────────────────

// Plan represents a subscription tier.
type Plan string

const (
	PlanNone    Plan = "none" // cancelled / no subscription — zero free allowance
	PlanStarter Plan = "starter"
	PlanPro     Plan = "pro"
)

// PlanLimits defines free-tier allowances per plan.
type PlanLimits struct {
	MonthlyCredit        float64 // $ credit included per month
	EgressFreePerProject int64   // bytes per project per month
	EgressCapPerTeam     int64   // total free egress bytes per team per month (abuse prevention)
	BuildFreeMinutes     int     // free build minutes per month
	EgressOveragePerGB   float64 // $/GB after free tier
	MaxProjects          int     // max projects per team (prevents project spam)
}

var PlanConfig = map[Plan]PlanLimits{
	PlanNone: {
		MonthlyCredit:        0,
		EgressFreePerProject: 0,
		EgressCapPerTeam:     0,
		BuildFreeMinutes:     0,
		EgressOveragePerGB:   0.12, // at cost
		MaxProjects:          1,    // can keep existing but not create new
	},
	PlanStarter: {
		MonthlyCredit:        5.00,
		EgressFreePerProject: 10 * 1_073_741_824, // 10 GiB per project
		EgressCapPerTeam:     50 * 1_073_741_824, // 50 GiB total per team (caps 5 projects worth)
		BuildFreeMinutes:     100,
		EgressOveragePerGB:   0.12,
		MaxProjects:          10,
	},
	PlanPro: {
		MonthlyCredit:        20.00,
		EgressFreePerProject: 50 * 1_073_741_824,  // 50 GiB per project
		EgressCapPerTeam:     500 * 1_073_741_824, // 500 GiB total per team
		BuildFreeMinutes:     500,
		EgressOveragePerGB:   0.10,
		MaxProjects:          50,
	},
}

// BuildRatePerSecond is the overage rate for build time after free tier.
const BuildRatePerSecond = 0.005

// ─── Per-Region Rate Structures ──────────────────────────────────────────────

// CloudRunRates holds GCP Cloud Run pricing for a single region.
type CloudRunRates struct {
	CPUSecond          float64 // $/vCPU-second (our price, including margin)
	MemoryGiBSecond    float64 // $/GiB-second
	RequestPer1M       float64 // $/1M requests
	NetworkEgressPerGB float64 // $/GB egress
}

// FargateRates holds AWS Fargate pricing for a single region.
// Rates are stored per-second for consistency with Cloud Run.
type FargateRates struct {
	CPUPerSecond      float64 // $/vCPU-second (our price, including margin)
	MemoryGBPerSecond float64 // $/GB-second
}

// StaticRates holds rates for providers where pricing doesn't vary by region.
type StaticRates struct {
	// Neon Postgres — same price all regions, no pricing API
	NeonComputeUnitSecond   float64
	NeonStoragePerGBMonth   float64
	NeonPublicNetworkPerGB  float64 // public egress ($0.09 + margin)
	NeonPrivateNetworkPerGB float64 // private/VPC egress ($0.01 + margin)

	// Upstash Redis — pass-through
	UpstashMarginPercent float64

	// GCS/S3 storage
	StoragePerGBMonth float64

	// Artifact Registry (container images)
	ArtifactRegistryPerGB float64
}

// ─── PricingTable: the top-level container ───────────────────────────────────

// PricingTable holds per-region pricing for Cloud Run and Fargate,
// plus static rates for Neon/Upstash/Storage. Thread-safe — pricing is
// refreshed in the background once per day.
type PricingTable struct {
	mu       sync.RWMutex
	cloudRun map[string]CloudRunRates // region → rates
	fargate  map[string]FargateRates  // region → rates
	static   StaticRates
	margin   float64
}

// NewPricingTable creates a PricingTable with default fallback rates.
// Call RefreshFromAPIs() after creation to populate live rates.
func NewPricingTable() *PricingTable {
	pt := &PricingTable{
		cloudRun: make(map[string]CloudRunRates),
		fargate:  make(map[string]FargateRates),
		margin:   defaultMargin,
		static: StaticRates{
			// Neon: $0.106/CU-hour = $0.0000294/CU-second × (1 + 35%)
			NeonComputeUnitSecond:   0.0000397,
			NeonStoragePerGBMonth:   0.135,  // Neon $0.10 + 35%
			NeonPublicNetworkPerGB:  0.1215, // Neon $0.09 + 35%
			NeonPrivateNetworkPerGB: 0.0135, // Neon $0.01 + 35%
			UpstashMarginPercent:    0.30,   // was 0.15, raised for margin parity
			StoragePerGBMonth:       0.027,  // GCS $0.020 + 35%
			ArtifactRegistryPerGB:   0.135,  // AR $0.10 + 35%
		},
	}

	// Seed with hardcoded defaults (GCP Tier 1 / AWS us-east-1)
	// so metering works even if API fetch fails on first boot.
	pt.cloudRun["default"] = CloudRunRates{
		CPUSecond:          applyMargin(0.0000240, defaultMargin),
		MemoryGiBSecond:    applyMargin(0.0000025, defaultMargin),
		RequestPer1M:       applyMargin(0.40, defaultMargin),
		NetworkEgressPerGB: applyMargin(0.085, defaultMargin),
	}
	// AWS Fargate default rates (us-east-1 Linux on-demand, converted to per-second).
	// Source: https://aws.amazon.com/fargate/pricing/
	// $0.04048/vCPU-hour → /3600 = $0.000011244/vCPU-second
	// $0.004445/GB-hour  → /3600 = $0.0000012347/GB-second
	pt.fargate["default"] = FargateRates{
		CPUPerSecond:      applyMargin(0.04048/3600, defaultMargin),
		MemoryGBPerSecond: applyMargin(0.004445/3600, defaultMargin),
	}

	return pt
}

// CloudRunForRegion returns the pricing for a specific GCP region.
// Falls back to "default" if the region isn't found.
func (pt *PricingTable) CloudRunForRegion(region string) CloudRunRates {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	if r, ok := pt.cloudRun[region]; ok {
		return r
	}
	return pt.cloudRun["default"]
}

// FargateForRegion returns the pricing for a specific AWS region.
// Falls back to "default" if the region isn't found.
func (pt *PricingTable) FargateForRegion(region string) FargateRates {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	if r, ok := pt.fargate[region]; ok {
		return r
	}
	return pt.fargate["default"]
}

// Static returns the non-region-varying rates.
func (pt *PricingTable) Static() StaticRates {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return pt.static
}

// CalculateCost applies the pricing table to a ConsumptionRecord.
func (pt *PricingTable) CalculateCost(record provider.ConsumptionRecord) float64 {
	// Pass-through: Upstash gives us $ directly
	if record.DirectCost != nil {
		s := pt.Static()
		return *record.DirectCost * (1 + s.UpstashMarginPercent)
	}

	m := record.Metrics
	if m == nil {
		return 0
	}

	var total float64

	switch record.Provider {
	case "cloud_run":
		r := pt.CloudRunForRegion(record.Region)
		total += m["cpu_seconds"] * r.CPUSecond
		total += m["memory_gib_seconds"] * r.MemoryGiBSecond
		total += (m["requests"] / 1_000_000) * r.RequestPer1M
		// Egress is handled separately by CalculateEgressCost (plan-aware free tier)

	case "ecs_fargate":
		r := pt.FargateForRegion(record.Region)
		total += m["vcpu_seconds"] * r.CPUPerSecond
		total += m["memory_gb_seconds"] * r.MemoryGBPerSecond

	case "neon":
		s := pt.Static()
		total += m["compute_unit_seconds"] * s.NeonComputeUnitSecond
		// Neon returns storage as byte-hours. Convert to GB-month:
		// GB-month = byte_hours / 1e9 (Neon uses decimal GB) / 744 (Neon's fixed 31-day month)
		gbMonth := m["storage_byte_hours"] / 1_000_000_000 / 744
		total += gbMonth * s.NeonStoragePerGBMonth
		// Neon uses decimal GB (10^9) for network pricing
		total += (m["public_network_transfer_bytes"] / 1_000_000_000) * s.NeonPublicNetworkPerGB
		total += (m["private_network_transfer_bytes"] / 1_000_000_000) * s.NeonPrivateNetworkPerGB

	case "gcs", "s3":
		s := pt.Static()
		hours := record.PeriodEnd.Sub(record.PeriodStart).Hours()
		if hours <= 0 {
			hours = 1
		}
		gbStored := m["storage_bytes"] / 1_073_741_824
		total += gbStored * s.StoragePerGBMonth * (hours / 720)

	case "artifact_registry":
		s := pt.Static()
		hours := record.PeriodEnd.Sub(record.PeriodStart).Hours()
		if hours <= 0 {
			hours = 1
		}
		// AR charges $0.10/GB/month; we apply 35% markup = $0.135/GB/month.
		// Prorate to the metering window (hours / 720 = fraction of a 30-day month).
		gbStored := m["storage_bytes"] / 1_073_741_824
		total += gbStored * s.ArtifactRegistryPerGB * (hours / 720)
	}

	// Guard against NaN/Inf from corrupted metrics
	if math.IsNaN(total) || math.IsInf(total, 0) || total < 0 {
		slog.Error("CalculateCost: invalid result, clamping to 0",
			"provider", record.Provider, "urn", record.ResourceURN, "raw_total", total)
		return 0
	}

	return total
}

// ─── Live Pricing Refresh ────────────────────────────────────────────────────

// RefreshFromAPIs fetches live pricing from GCP Cloud Billing Catalog
// and AWS Bulk Pricing API. Safe to call concurrently. Errors are logged
// but don't prevent the system from using cached/default rates.
func (pt *PricingTable) RefreshFromAPIs(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		if err := pt.refreshCloudRunPricing(ctx); err != nil {
			slog.Error("refresh Cloud Run pricing failed", "error", err)
		}
	}()

	go func() {
		defer wg.Done()
		if err := pt.refreshFargatePricing(ctx); err != nil {
			slog.Error("refresh Fargate pricing failed", "error", err)
		}
	}()

	wg.Wait()
	pt.mu.RLock()
	crCount := len(pt.cloudRun) - 1 // -1 for "default"
	fgCount := len(pt.fargate) - 1
	pt.mu.RUnlock()
	slog.Info("pricing table refreshed",
		"cloud_run_regions", crCount,
		"fargate_regions", fgCount,
	)
}

// StartRefreshLoop starts a background goroutine that refreshes pricing daily.
// The initial fetch is async so it doesn't block server startup.
func (pt *PricingTable) StartRefreshLoop(ctx context.Context) {
	go func() {
		// Initial fetch (async — server starts with hardcoded defaults)
		pt.RefreshFromAPIs(ctx)

		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pt.RefreshFromAPIs(ctx)
			}
		}
	}()
}

// ─── GCP Cloud Billing Catalog ───────────────────────────────────────────────

const gcpCloudRunServiceID = "services/152E-C115-5142"

func (pt *PricingTable) refreshCloudRunPricing(ctx context.Context) error {
	client, err := billing.NewCloudCatalogClient(ctx)
	if err != nil {
		return fmt.Errorf("create billing catalog client: %w", err)
	}
	defer client.Close()

	rates := make(map[string]*CloudRunRates)
	ensureRegion := func(region string) *CloudRunRates {
		if r, ok := rates[region]; ok {
			return r
		}
		r := &CloudRunRates{}
		rates[region] = r
		return r
	}

	it := client.ListSkus(ctx, &billingpb.ListSkusRequest{
		Parent: gcpCloudRunServiceID,
	})

	for {
		sku, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return fmt.Errorf("list skus: %w", err)
		}

		// Only OnDemand pricing
		cat := sku.GetCategory()
		if cat == nil || cat.GetUsageType() != "OnDemand" {
			continue
		}

		price := extractGCPPrice(sku)
		if price <= 0 {
			continue
		}
		priceWithMargin := applyMargin(price, pt.margin)

		desc := strings.ToLower(sku.GetDescription())

		for _, region := range sku.GetServiceRegions() {
			r := ensureRegion(region)
			switch {
			case strings.Contains(desc, "vcpu") && strings.Contains(desc, "allocation"):
				r.CPUSecond = priceWithMargin
			case strings.Contains(desc, "memory") && strings.Contains(desc, "allocation"):
				r.MemoryGiBSecond = priceWithMargin
			case strings.Contains(desc, "request"):
				r.RequestPer1M = priceWithMargin
			case strings.Contains(desc, "network") || strings.Contains(desc, "egress"):
				r.NetworkEgressPerGB = priceWithMargin
			}
		}
	}

	// Apply to pricing table
	pt.mu.Lock()
	defer pt.mu.Unlock()
	for region, r := range rates {
		// Only store if we got at least CPU pricing (sanity check)
		if r.CPUSecond > 0 {
			pt.cloudRun[region] = *r
		}
	}

	slog.Info("Cloud Run pricing fetched", "regions", len(rates))
	return nil
}

func extractGCPPrice(sku *billingpb.Sku) float64 {
	pricingInfo := sku.GetPricingInfo()
	if len(pricingInfo) == 0 {
		return 0
	}
	expr := pricingInfo[0].GetPricingExpression()
	if expr == nil {
		return 0
	}
	tiers := expr.GetTieredRates()
	if len(tiers) == 0 {
		return 0
	}
	// Use the first tier (base rate)
	up := tiers[0].GetUnitPrice()
	if up == nil {
		return 0
	}
	return float64(up.GetUnits()) + float64(up.GetNanos())/1_000_000_000
}

// ─── AWS Bulk Pricing API ────────────────────────────────────────────────────

func (pt *PricingTable) refreshFargatePricing(ctx context.Context) error {
	// Fetch the region index for ECS (Fargate is under AmazonECS)
	indexURL := "https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonECS/current/region_index.json"

	req, err := http.NewRequestWithContext(ctx, "GET", indexURL, nil)
	if err != nil {
		return err
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch ECS region index: %w", err)
	}
	defer resp.Body.Close()

	var regionIndex struct {
		Regions map[string]struct {
			CurrentVersionURL string `json:"currentVersionUrl"`
		} `json:"regions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&regionIndex); err != nil {
		return fmt.Errorf("decode region index: %w", err)
	}

	// Fetch pricing for each region (concurrently, limited)
	type regionResult struct {
		region string
		rates  FargateRates
		ok     bool
	}

	results := make(chan regionResult, len(regionIndex.Regions))
	sem := make(chan struct{}, 10) // concurrency limit

	for regionName := range regionIndex.Regions {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break
		}
		go func(region string) {
			defer func() { <-sem }()

			url := fmt.Sprintf(
				"https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonECS/current/%s/index.json",
				region,
			)
			rates, fetchErr := fetchFargateRegionPricing(ctx, url)
			if fetchErr != nil {
				slog.Warn("fetch Fargate pricing failed", "region", region, "error", fetchErr)
				results <- regionResult{region: region}
				return
			}
			// AWS Bulk Pricing API returns per-hour rates; convert to per-second
			// for consistency with Cloud Run and the per-second metering approach.
			results <- regionResult{
				region: region,
				rates: FargateRates{
					CPUPerSecond:      applyMargin(rates.cpuPerHour/3600, pt.margin),
					MemoryGBPerSecond: applyMargin(rates.memGBPerHour/3600, pt.margin),
				},
				ok: true,
			}
		}(regionName)
	}

	// Collect results
	pt.mu.Lock()
	defer pt.mu.Unlock()
	count := 0
	for i := 0; i < len(regionIndex.Regions); i++ {
		r := <-results
		if r.ok && r.rates.CPUPerSecond > 0 {
			pt.fargate[r.region] = r.rates
			count++
		}
	}

	slog.Info("Fargate pricing fetched", "regions", count)
	return nil
}

type rawFargateRates struct {
	cpuPerHour   float64
	memGBPerHour float64
}

func fetchFargateRegionPricing(ctx context.Context, url string) (rawFargateRates, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return rawFargateRates{}, err
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return rawFargateRates{}, err
	}
	defer resp.Body.Close()

	// The JSON structure has products and terms.OnDemand
	var data struct {
		Products map[string]struct {
			Attributes map[string]string `json:"attributes"`
		} `json:"products"`
		Terms struct {
			OnDemand map[string]map[string]struct {
				PriceDimensions map[string]struct {
					PricePerUnit map[string]string `json:"pricePerUnit"`
					Unit         string            `json:"unit"`
					Description  string            `json:"description"`
				} `json:"priceDimensions"`
			} `json:"OnDemand"`
		} `json:"terms"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return rawFargateRates{}, fmt.Errorf("decode pricing JSON: %w", err)
	}

	var rates rawFargateRates

	// Find Linux x86 Fargate CPU and Memory SKUs
	for sku, prod := range data.Products {
		usage := prod.Attributes["usagetype"]
		if usage == "" {
			continue
		}

		// Linux x86 Fargate (not ARM, not Windows)
		isCPU := strings.Contains(usage, "Fargate-vCPU-Hours") && !strings.Contains(usage, "ARM")
		isMem := strings.Contains(usage, "Fargate-GB-Hours") && !strings.Contains(usage, "ARM") && !strings.Contains(usage, "Ephemeral")

		if !isCPU && !isMem {
			continue
		}

		// Find price in terms
		if offers, ok := data.Terms.OnDemand[sku]; ok {
			for _, offer := range offers {
				for _, dim := range offer.PriceDimensions {
					usd := dim.PricePerUnit["USD"]
					if usd == "" {
						continue
					}
					var price float64
					if _, scanErr := fmt.Sscanf(usd, "%f", &price); scanErr != nil || price <= 0 {
						continue
					}

					if isCPU {
						rates.cpuPerHour = price
					} else if isMem {
						rates.memGBPerHour = price
					}
				}
			}
		}
	}

	return rates, nil
}

// ─── Utilities ───────────────────────────────────────────────────────────────

func applyMargin(cost, margin float64) float64 {
	return cost * (1 + margin)
}
