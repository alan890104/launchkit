package provider

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
)

// GetMetrics queries AWS CloudWatch for ECS and ALB metrics.
func (e *ECSFargate) GetMetrics(ctx context.Context, params MetricsQueryParams) ([]MetricsResponse, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(e.cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	cw := cloudwatch.NewFromConfig(awsCfg)

	now := time.Now().UTC()
	startTime, err := parsePeriodToTimeCW(params.Period, now)
	if err != nil {
		return nil, fmt.Errorf("parse period: %w", err)
	}

	period := int32(300) // 5-min granularity default
	if now.Sub(startTime) > 24*time.Hour {
		period = 3600 // 1-hour granularity for longer windows
	}

	serviceName := params.ServiceName
	if serviceName == "" {
		return nil, fmt.Errorf("service_name is required for ECS metrics")
	}

	// Collect requested metrics (or a default set)
	wantedMetrics := map[string]bool{}
	for _, m := range params.Metrics {
		wantedMetrics[m] = true
	}
	if len(wantedMetrics) == 0 {
		// Default: all ECS metrics
		for _, m := range []string{"cpu", "memory", "requests", "error_rate", "latency_p50"} {
			wantedMetrics[m] = true
		}
	}

	// Build CloudWatch metric data queries
	queries := buildECSMetricQueries(serviceName, e.cfg.ClusterName, wantedMetrics, period)

	if len(queries) == 0 {
		return []MetricsResponse{}, nil
	}

	result, err := cw.GetMetricData(ctx, &cloudwatch.GetMetricDataInput{
		MetricDataQueries: queries,
		StartTime:         aws.Time(startTime),
		EndTime:           aws.Time(now),
	})
	if err != nil {
		return nil, fmt.Errorf("get metric data: %w", err)
	}

	// Parse results into MetricsResponse
	data := make(map[string]Metric)
	for _, res := range result.MetricDataResults {
		id := aws.ToString(res.Id)
		if len(res.Values) == 0 {
			continue
		}
		values := res.Values
		current := values[len(values)-1]
		avg, min, maxV := statsOf(values)
		unit, label := cwMetricLabel(id)
		data[label] = Metric{
			Current: roundTo2(current),
			Avg:     roundTo2(avg),
			Min:     roundTo2(min),
			Max:     roundTo2(maxV),
			Unit:    unit,
			Trend:   trendOf(values),
		}
	}

	return []MetricsResponse{{
		Service: serviceName,
		Period: map[string]string{
			"start": startTime.Format(time.RFC3339),
			"end":   now.Format(time.RFC3339),
		},
		Data: data,
	}}, nil
}

// GetUptime estimates uptime from ECS service running/desired count ratio.
func (e *ECSFargate) GetUptime(ctx context.Context, serviceName, region string, period string) (UptimeInfo, error) {
	result, err := e.ecs.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Services: []string{serviceName},
		Cluster:  aws.String(e.cfg.ClusterName),
	})
	if err != nil {
		return UptimeInfo{Service: serviceName, UptimePercent: 0}, fmt.Errorf("describe service: %w", err)
	}

	if len(result.Services) == 0 {
		return UptimeInfo{Service: serviceName, UptimePercent: 0, TotalDowntime: "unknown"}, nil
	}

	svc := result.Services[0]
	if svc.DesiredCount == 0 {
		return UptimeInfo{Service: serviceName, UptimePercent: 0, TotalDowntime: "service stopped"}, nil
	}

	// Health = running / desired
	runRatio := float64(svc.RunningCount) / float64(svc.DesiredCount)
	uptimePct := math.Round(runRatio*10000) / 100 // 2 decimal places

	downtime := "0s"
	if uptimePct < 100 {
		now := time.Now().UTC()
		start, _ := parsePeriodToTimeCW(period, now)
		totalDur := now.Sub(start)
		downtimeDur := time.Duration(float64(totalDur) * (1 - runRatio))
		downtime = formatDurationCW(downtimeDur)
	}

	return UptimeInfo{
		Service:       serviceName,
		UptimePercent: uptimePct,
		TotalDowntime: downtime,
	}, nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func buildECSMetricQueries(serviceName, clusterName string, wanted map[string]bool, period int32) []cwtypes.MetricDataQuery {
	var queries []cwtypes.MetricDataQuery

	ecsDims := []cwtypes.Dimension{
		{Name: aws.String("ClusterName"), Value: aws.String(clusterName)},
		{Name: aws.String("ServiceName"), Value: aws.String(serviceName)},
	}

	if wanted["cpu"] {
		queries = append(queries, cwtypes.MetricDataQuery{
			Id:    aws.String("cpu"),
			Label: aws.String("CPU Utilization"),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  aws.String("AWS/ECS"),
					MetricName: aws.String("CPUUtilization"),
					Dimensions: ecsDims,
				},
				Period: aws.Int32(period),
				Stat:   aws.String("Average"),
			},
		})
	}

	if wanted["memory"] {
		queries = append(queries, cwtypes.MetricDataQuery{
			Id:    aws.String("memory"),
			Label: aws.String("Memory Utilization"),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  aws.String("AWS/ECS"),
					MetricName: aws.String("MemoryUtilization"),
					Dimensions: ecsDims,
				},
				Period: aws.Int32(period),
				Stat:   aws.String("Average"),
			},
		})
	}

	if wanted["requests"] {
		queries = append(queries, cwtypes.MetricDataQuery{
			Id:    aws.String("requests"),
			Label: aws.String("Request Count"),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  aws.String("AWS/ApplicationELB"),
					MetricName: aws.String("RequestCount"),
					Dimensions: []cwtypes.Dimension{{
						Name:  aws.String("TargetGroup"),
						Value: aws.String("lk-" + serviceName),
					}},
				},
				Period: aws.Int32(period),
				Stat:   aws.String("Sum"),
			},
		})
	}

	if wanted["error_rate"] {
		queries = append(queries, cwtypes.MetricDataQuery{
			Id:    aws.String("errors5xx"),
			Label: aws.String("5xx Errors"),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  aws.String("AWS/ApplicationELB"),
					MetricName: aws.String("HTTPCode_Target_5XX_Count"),
					Dimensions: []cwtypes.Dimension{{
						Name:  aws.String("TargetGroup"),
						Value: aws.String("lk-" + serviceName),
					}},
				},
				Period: aws.Int32(period),
				Stat:   aws.String("Sum"),
			},
		})
	}

	if wanted["latency_p50"] || wanted["latency_p95"] || wanted["latency_p99"] {
		stat := "p50"
		if wanted["latency_p95"] {
			stat = "p95"
		}
		if wanted["latency_p99"] {
			stat = "p99"
		}
		queries = append(queries, cwtypes.MetricDataQuery{
			Id:    aws.String("latency"),
			Label: aws.String("Response Latency"),
			MetricStat: &cwtypes.MetricStat{
				Metric: &cwtypes.Metric{
					Namespace:  aws.String("AWS/ApplicationELB"),
					MetricName: aws.String("TargetResponseTime"),
					Dimensions: []cwtypes.Dimension{{
						Name:  aws.String("TargetGroup"),
						Value: aws.String("lk-" + serviceName),
					}},
				},
				Period: aws.Int32(period),
				Stat:   aws.String(stat),
			},
		})
	}

	return queries
}

func parsePeriodToTimeCW(period string, now time.Time) (time.Time, error) {
	switch period {
	case "", "1h":
		return now.Add(-1 * time.Hour), nil
	case "6h":
		return now.Add(-6 * time.Hour), nil
	case "24h":
		return now.Add(-24 * time.Hour), nil
	case "7d":
		return now.Add(-7 * 24 * time.Hour), nil
	case "30d":
		return now.Add(-30 * 24 * time.Hour), nil
	default:
		return time.Time{}, fmt.Errorf("unsupported period %q (use 1h, 6h, 24h, 7d, 30d)", period)
	}
}

func formatDurationCW(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%.1fh", d.Hours())
	}
	return fmt.Sprintf("%.1fd", d.Hours()/24)
}

func statsOf(values []float64) (avg, min, max float64) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	sum := 0.0
	min = values[0]
	max = values[0]
	for _, v := range values {
		sum += v
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	return sum / float64(len(values)), min, max
}

func roundTo2(v float64) float64 {
	return math.Round(v*100) / 100
}

func trendOf(values []float64) string {
	if len(values) < 2 {
		return "stable"
	}
	last := values[len(values)-1]
	prev := values[len(values)-2]
	if prev == 0 {
		return "stable"
	}
	delta := (last - prev) / prev
	switch {
	case delta > 0.05:
		return "up"
	case delta < -0.05:
		return "down"
	default:
		return "stable"
	}
}

func cwMetricLabel(id string) (unit, label string) {
	switch id {
	case "cpu":
		return "%", "cpu"
	case "memory":
		return "%", "memory"
	case "requests":
		return "req/period", "requests"
	case "errors5xx":
		return "errors/period", "error_rate"
	case "latency":
		return "seconds", "latency"
	default:
		return "", id
	}
}
