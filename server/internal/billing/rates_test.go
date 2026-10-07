package billing

import (
	"math"
	"testing"
	"time"

	"github.com/alan890104/launchkit/server/internal/provider"
)

func TestPlanConfig(t *testing.T) {
	starter := PlanConfig[PlanStarter]
	pro := PlanConfig[PlanPro]

	if starter.MonthlyCredit != 5.0 {
		t.Errorf("Starter credit = %v, want 5.0", starter.MonthlyCredit)
	}
	if pro.MonthlyCredit != 20.0 {
		t.Errorf("Pro credit = %v, want 20.0", pro.MonthlyCredit)
	}
	if starter.EgressFreePerProject >= pro.EgressFreePerProject {
		t.Error("Pro should have more egress free tier than Starter")
	}
	if starter.BuildFreeMinutes >= pro.BuildFreeMinutes {
		t.Error("Pro should have more build minutes than Starter")
	}
}

func TestCalculateCost_CloudRun(t *testing.T) {
	pt := NewPricingTable()

	record := provider.ConsumptionRecord{
		Provider:    "cloud_run",
		Region:      "default",
		PeriodStart: time.Now().Add(-1 * time.Hour),
		PeriodEnd:   time.Now(),
		Metrics: map[string]float64{
			"cpu_seconds":        3600,  // 1 vCPU-hour
			"memory_gib_seconds": 3600,  // 1 GiB-hour
			"requests":           10000, // 10K requests
			// egress is NOT included in CalculateCost (handled separately)
		},
	}

	cost := pt.CalculateCost(record)

	if cost <= 0 {
		t.Fatal("cost should be > 0")
	}

	// Verify individual components
	defaults := pt.CloudRunForRegion("default")
	expectedCPU := 3600 * defaults.CPUSecond
	expectedMem := 3600 * defaults.MemoryGiBSecond
	expectedReq := (10000.0 / 1_000_000) * defaults.RequestPer1M
	expected := expectedCPU + expectedMem + expectedReq

	if math.Abs(cost-expected) > 0.0001 {
		t.Errorf("cost = %v, want %v", cost, expected)
	}

	// Verify margin is applied (cost should be > raw GCP cost)
	rawCPU := 3600 * 0.0000240
	if cost <= rawCPU {
		t.Error("cost should be greater than raw GCP CPU cost (margin not applied)")
	}
}

func TestCalculateCost_CloudRun_NoEgress(t *testing.T) {
	pt := NewPricingTable()

	// Egress should NOT be included in CalculateCost
	record := provider.ConsumptionRecord{
		Provider: "cloud_run",
		Region:   "default",
		Metrics: map[string]float64{
			"cpu_seconds":          0,
			"memory_gib_seconds":   0,
			"requests":             0,
			"network_egress_bytes": 10_000_000_000, // 10GB — should be ignored
		},
	}

	cost := pt.CalculateCost(record)
	if cost != 0 {
		t.Errorf("cost should be 0 when compute is 0, got %v (egress should not be included)", cost)
	}
}

func TestCalculateCost_Fargate(t *testing.T) {
	pt := NewPricingTable()

	record := provider.ConsumptionRecord{
		Provider: "ecs_fargate",
		Region:   "default",
		Metrics: map[string]float64{
			"vcpu_seconds":      3600.0, // 1 vCPU-hour in seconds
			"memory_gb_seconds": 7200.0, // 2 GB-hours in seconds
		},
	}

	cost := pt.CalculateCost(record)
	defaults := pt.FargateForRegion("default")
	expected := 3600.0*defaults.CPUPerSecond + 7200.0*defaults.MemoryGBPerSecond

	if math.Abs(cost-expected) > 0.0001 {
		t.Errorf("cost = %v, want %v", cost, expected)
	}
}

func TestCalculateCost_Neon(t *testing.T) {
	pt := NewPricingTable()

	// Neon uses decimal GB (10^9) and fixed 744h/month (31 days).
	record := provider.ConsumptionRecord{
		Provider: "neon",
		Metrics: map[string]float64{
			"compute_unit_seconds":           3600,                  // 1 CU-hour
			"storage_byte_hours":             1_000_000_000 * 744.0, // 1 GB × 744 hours = 1 GB-month
			"public_network_transfer_bytes":  1_000_000_000,         // 1 GB
			"private_network_transfer_bytes": 1_000_000_000,         // 1 GB
		},
	}

	cost := pt.CalculateCost(record)
	s := pt.Static()

	expectedCompute := 3600 * s.NeonComputeUnitSecond
	expectedStorage := 1.0 * s.NeonStoragePerGBMonth // 1 GB-month
	expectedPubNet := 1.0 * s.NeonPublicNetworkPerGB
	expectedPrivNet := 1.0 * s.NeonPrivateNetworkPerGB
	expected := expectedCompute + expectedStorage + expectedPubNet + expectedPrivNet

	if math.Abs(cost-expected) > 0.001 {
		t.Errorf("cost = %v, want %v", cost, expected)
	}
}

func TestCalculateCost_Upstash_PassThrough(t *testing.T) {
	pt := NewPricingTable()

	directCost := 1.50
	record := provider.ConsumptionRecord{
		Provider:   "upstash",
		DirectCost: &directCost,
		Metrics: map[string]float64{
			"monthly_requests": 100000,
		},
	}

	cost := pt.CalculateCost(record)
	expected := 1.50 * (1 + pt.Static().UpstashMarginPercent)

	if math.Abs(cost-expected) > 0.001 {
		t.Errorf("cost = %v, want %v (pass-through + %v%% margin)", cost, expected, pt.Static().UpstashMarginPercent*100)
	}
}

func TestCalculateCost_Storage_Prorated(t *testing.T) {
	pt := NewPricingTable()

	now := time.Now()
	record := provider.ConsumptionRecord{
		Provider:    "gcs",
		PeriodStart: now.Add(-1 * time.Hour),
		PeriodEnd:   now,
		Metrics: map[string]float64{
			"storage_bytes": 10 * 1_073_741_824, // 10 GiB
		},
	}

	cost := pt.CalculateCost(record)
	s := pt.Static()
	// 10 GB × rate × (1 hour / 720 hours per month)
	expected := 10.0 * s.StoragePerGBMonth * (1.0 / 720.0)

	if math.Abs(cost-expected) > 0.0001 {
		t.Errorf("cost = %v, want %v", cost, expected)
	}
}

func TestCalculateCost_ZeroMetrics(t *testing.T) {
	pt := NewPricingTable()

	for _, prov := range []string{"cloud_run", "ecs_fargate", "neon", "gcs", "s3"} {
		record := provider.ConsumptionRecord{
			Provider: prov,
			Metrics:  map[string]float64{},
		}
		cost := pt.CalculateCost(record)
		if cost != 0 {
			t.Errorf("%s: cost should be 0 for empty metrics, got %v", prov, cost)
		}
	}
}

func TestCalculateCost_NilMetrics(t *testing.T) {
	pt := NewPricingTable()

	record := provider.ConsumptionRecord{
		Provider: "cloud_run",
		Metrics:  nil,
	}
	cost := pt.CalculateCost(record)
	if cost != 0 {
		t.Errorf("cost should be 0 for nil metrics, got %v", cost)
	}
}

func TestDefaultMargin(t *testing.T) {
	if defaultMargin != 0.35 {
		t.Errorf("defaultMargin = %v, want 0.35", defaultMargin)
	}
}

func TestBuildRatePerSecond(t *testing.T) {
	if BuildRatePerSecond != 0.005 {
		t.Errorf("BuildRatePerSecond = %v, want 0.005", BuildRatePerSecond)
	}

	// 3 minute build = 180 seconds × $0.005 = $0.90
	cost := 180.0 * BuildRatePerSecond
	if math.Abs(cost-0.90) > 0.001 {
		t.Errorf("3-min build cost = %v, want $0.90", cost)
	}
}

func TestRegionFallback(t *testing.T) {
	pt := NewPricingTable()

	// Unknown region should fall back to default
	r := pt.CloudRunForRegion("mars-west1")
	d := pt.CloudRunForRegion("default")

	if r.CPUSecond != d.CPUSecond {
		t.Errorf("unknown region should fall back to default: %v vs %v", r.CPUSecond, d.CPUSecond)
	}

	f := pt.FargateForRegion("antarctic-south-1")
	fd := pt.FargateForRegion("default")

	if f.CPUPerSecond != fd.CPUPerSecond {
		t.Errorf("unknown region should fall back to default: %v vs %v", f.CPUPerSecond, fd.CPUPerSecond)
	}
}

func TestApplyMargin(t *testing.T) {
	cost := 1.00
	result := applyMargin(cost, 0.35)
	if math.Abs(result-1.35) > 0.001 {
		t.Errorf("applyMargin(1.00, 0.35) = %v, want 1.35", result)
	}

	// Verify gross margin: (price - cost) / price = 0.35 / 1.35 = 25.9%
	grossMargin := (result - cost) / result
	if grossMargin < 0.25 || grossMargin > 0.27 {
		t.Errorf("gross margin = %v%%, want ~25.9%%", grossMargin*100)
	}
}
