package provider

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	monitoringpb "cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ─── GCS Storage Fetcher ─────────────────────────────────────────────────────

// GCSStorageFetcher implements ConsumptionFetcher for Google Cloud Storage.
type GCSStorageFetcher struct {
	projectID string
}

var _ ConsumptionFetcher = (*GCSStorageFetcher)(nil)

func NewGCSStorageFetcher(projectID string) *GCSStorageFetcher {
	return &GCSStorageFetcher{projectID: projectID}
}

func (f *GCSStorageFetcher) ProviderName() string { return "gcs" }

func (f *GCSStorageFetcher) FetchConsumption(ctx context.Context, resources []ActiveResource, start, end time.Time) ([]ConsumptionRecord, error) {
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
		bucketName := OutputStr(res.Outputs, "provider_id")
		if bucketName == "" {
			continue
		}

		// Query storage size (gauge metric — take the latest value)
		filter := fmt.Sprintf(
			`resource.type="gcs_bucket" AND resource.labels.bucket_name=%q AND metric.type="storage.googleapis.com/storage/total_bytes"`,
			bucketName,
		)

		storageBytes := queryMonitoringLatest(ctx, client, projectName, filter, interval)
		if storageBytes <= 0 {
			continue
		}

		records = append(records, ConsumptionRecord{
			ResourceURN:  res.URN,
			ProjectID:    res.ProjectID,
			ResourceType: "storage",
			Provider:     "gcs",
			ProviderID:   bucketName,
			PeriodStart:  start,
			PeriodEnd:    end,
			Metrics: map[string]float64{
				"storage_bytes": storageBytes,
			},
		})
	}

	return records, nil
}

// queryMonitoringLatest returns the most recent value from a gauge metric.
func queryMonitoringLatest(ctx context.Context, client *monitoring.MetricClient, projectName, filter string, interval *monitoringpb.TimeInterval) float64 {
	it := client.ListTimeSeries(ctx, &monitoringpb.ListTimeSeriesRequest{
		Name:     projectName,
		Filter:   filter,
		Interval: interval,
		View:     monitoringpb.ListTimeSeriesRequest_FULL,
	})

	var latest float64
	var latestTime time.Time

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
			t := point.GetInterval().GetEndTime().AsTime()
			var val float64
			switch v := point.GetValue().GetValue().(type) {
			case *monitoringpb.TypedValue_Int64Value:
				val = float64(v.Int64Value)
			case *monitoringpb.TypedValue_DoubleValue:
				val = v.DoubleValue
			}
			if val > 0 && (latestTime.IsZero() || t.After(latestTime)) {
				latest = val
				latestTime = t
			}
		}
	}
	return latest
}

// ─── S3 Storage Fetcher ──────────────────────────────────────────────────────

// S3StorageFetcher implements ConsumptionFetcher for AWS S3.
type S3StorageFetcher struct {
	region string
}

var _ ConsumptionFetcher = (*S3StorageFetcher)(nil)

func NewS3StorageFetcher(region string) *S3StorageFetcher {
	return &S3StorageFetcher{region: region}
}

func (f *S3StorageFetcher) ProviderName() string { return "s3" }

func (f *S3StorageFetcher) FetchConsumption(ctx context.Context, resources []ActiveResource, start, end time.Time) ([]ConsumptionRecord, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(f.region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	cw := cloudwatch.NewFromConfig(awsCfg)

	var records []ConsumptionRecord

	for _, res := range resources {
		bucketName := OutputStr(res.Outputs, "provider_id")
		if bucketName == "" {
			continue
		}

		// S3 BucketSizeBytes is a daily metric (reported once per day).
		// Use a 2-day window to ensure we get at least one data point.
		cwStart := start.Add(-24 * time.Hour)

		out, err := cw.GetMetricStatistics(ctx, &cloudwatch.GetMetricStatisticsInput{
			Namespace:  aws.String("AWS/S3"),
			MetricName: aws.String("BucketSizeBytes"),
			Dimensions: []cwtypes.Dimension{
				{Name: aws.String("BucketName"), Value: aws.String(bucketName)},
				{Name: aws.String("StorageType"), Value: aws.String("StandardStorage")},
			},
			StartTime:  &cwStart,
			EndTime:    &end,
			Period:     aws.Int32(86400), // 1 day
			Statistics: []cwtypes.Statistic{cwtypes.StatisticAverage},
		})
		if err != nil {
			slog.Warn("S3 storage metric failed", "bucket", bucketName, "error", err)
			continue
		}

		if len(out.Datapoints) == 0 {
			continue
		}

		// Use the most recent data point
		var storageBytes float64
		var latestTime time.Time
		for _, dp := range out.Datapoints {
			if dp.Average != nil && dp.Timestamp != nil {
				if latestTime.IsZero() || dp.Timestamp.After(latestTime) {
					storageBytes = *dp.Average
					latestTime = *dp.Timestamp
				}
			}
		}

		if storageBytes <= 0 {
			continue
		}

		records = append(records, ConsumptionRecord{
			ResourceURN:  res.URN,
			ProjectID:    res.ProjectID,
			ResourceType: "storage",
			Provider:     "s3",
			ProviderID:   bucketName,
			Region:       f.region,
			PeriodStart:  start,
			PeriodEnd:    end,
			Metrics: map[string]float64{
				"storage_bytes": storageBytes,
			},
		})
	}

	return records, nil
}
