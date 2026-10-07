package provider

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	monitoringpb "cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// CloudRunFetcher implements ConsumptionFetcher for GCP Cloud Run.
type CloudRunFetcher struct {
	projectID string
	region    string
}

var _ ConsumptionFetcher = (*CloudRunFetcher)(nil)

func NewCloudRunFetcher(projectID, region string) *CloudRunFetcher {
	return &CloudRunFetcher{projectID: projectID, region: region}
}

func (f *CloudRunFetcher) ProviderName() string { return "cloud_run" }

func (f *CloudRunFetcher) FetchConsumption(ctx context.Context, resources []ActiveResource, start, end time.Time) ([]ConsumptionRecord, error) {
	client, err := monitoring.NewMetricClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create monitoring client: %w", err)
	}
	defer client.Close()

	projectName := fmt.Sprintf("projects/%s", f.projectID)
	interval := &monitoringpb.TimeInterval{
		StartTime: timestamppb.New(start),
		EndTime:   timestamppb.New(end),
	}

	var records []ConsumptionRecord

	for _, res := range resources {
		serviceName := OutputStr(res.Outputs, "service_name")
		region := OutputStr(res.Outputs, "region")
		if serviceName == "" {
			continue
		}
		if region == "" {
			region = f.region
		}

		metrics := map[string]float64{}

		// Build all filters for this service, then query once per metric.
		// Cloud Monitoring rate limit is ~6000 calls/min so this is fine for
		// hundreds of services, but we batch into a single filter per metric.
		svcFilter := fmt.Sprintf(
			`resource.type="cloud_run_revision" AND resource.labels.service_name=%q`,
			serviceName,
		)

		// CPU allocation time (vCPU-seconds) — this is what GCP actually bills
		metrics["cpu_seconds"] = queryMonitoringSum(ctx, client, projectName,
			svcFilter+` AND metric.type="run.googleapis.com/container/cpu/allocation_time"`, interval)

		// Memory allocation time (GiB-seconds)
		metrics["memory_gib_seconds"] = queryMonitoringSum(ctx, client, projectName,
			svcFilter+` AND metric.type="run.googleapis.com/container/memory/allocation_time"`, interval)

		// Request count
		metrics["requests"] = queryMonitoringSum(ctx, client, projectName,
			svcFilter+` AND metric.type="run.googleapis.com/request_count"`, interval)

		// Network egress: sent_bytes_count is a DELTA metric (bytes sent per sample interval),
		// so summing all points in [start, end) gives total bytes for the period — no cumulative
		// dedup needed. Cloud Monitoring does NOT distinguish egress destination
		// (same-region=free, cross-region=$0.01, internet=$0.085-0.12).
		// Egress cost is calculated separately by billing.CalculateEgressCost (plan-aware free tier).
		metrics["network_egress_bytes"] = queryMonitoringSum(ctx, client, projectName,
			svcFilter+` AND metric.type="run.googleapis.com/container/network/sent_bytes_count"`, interval)

		// Skip if no activity at all
		if metrics["cpu_seconds"] == 0 && metrics["requests"] == 0 {
			continue
		}

		records = append(records, ConsumptionRecord{
			ResourceURN:  res.URN,
			ProjectID:    res.ProjectID,
			ResourceType: "compute",
			Provider:     "cloud_run",
			ProviderID:   serviceName,
			Region:       region,
			PeriodStart:  start,
			PeriodEnd:    end,
			Metrics:      metrics,
		})
	}

	return records, nil
}

// queryMonitoringSum queries Cloud Monitoring and returns the sum of all data points.
func queryMonitoringSum(ctx context.Context, client *monitoring.MetricClient, projectName, filter string, interval *monitoringpb.TimeInterval) float64 {
	it := client.ListTimeSeries(ctx, &monitoringpb.ListTimeSeriesRequest{
		Name:     projectName,
		Filter:   filter,
		Interval: interval,
		View:     monitoringpb.ListTimeSeriesRequest_FULL,
	})

	var total float64
	for {
		ts, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			slog.Warn("monitoring query failed", "filter", filter, "error", err)
			break
		}
		for _, point := range ts.Points {
			switch v := point.GetValue().GetValue().(type) {
			case *monitoringpb.TypedValue_Int64Value:
				total += float64(v.Int64Value)
			case *monitoringpb.TypedValue_DoubleValue:
				total += v.DoubleValue
			}
		}
	}
	return total
}
