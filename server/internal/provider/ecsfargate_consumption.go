package provider

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

const (
	// AWS Fargate minimum billable duration per task (Linux on-demand).
	// https://aws.amazon.com/fargate/pricing/ — "rounded up to the nearest second,
	// with a 1-minute minimum per task"
	fargateMinBillableSeconds = 60.0
)

// ECSFargateFetcher implements ConsumptionFetcher for AWS ECS Fargate.
// Fargate billing is allocation-based: you pay for the task definition size
// (CPU units, memory MiB) × running duration, not actual utilization.
//
// This fetcher uses DescribeTasks to obtain exact startedAt/stoppedAt timestamps
// for every task (RUNNING + STOPPED) that overlapped with the metering window,
// giving per-second billing accuracy instead of the snapshot-based RunningCount.
type ECSFargateFetcher struct {
	region      string
	clusterName string
	client      *ecs.Client // shared client, created once at construction
}

var _ ConsumptionFetcher = (*ECSFargateFetcher)(nil)

// NewECSFargateFetcher creates an ECSFargateFetcher.
// The AWS client is initialised once here so FetchConsumption doesn't pay
// the config-loading overhead on every metering tick.
func NewECSFargateFetcher(ctx context.Context, region, clusterName string) (*ECSFargateFetcher, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return &ECSFargateFetcher{
		region:      region,
		clusterName: clusterName,
		client:      ecs.NewFromConfig(awsCfg),
	}, nil
}

func (f *ECSFargateFetcher) ProviderName() string { return "ecs_fargate" }

// FetchConsumption returns one ConsumptionRecord per ECS service that had any
// billable activity in [start, end).
//
// Algorithm:
//  1. ListTasks (RUNNING + STOPPED) for the service.
//  2. DescribeTasks to obtain exact startedAt / stoppedAt timestamps.
//  3. Keep tasks that overlapped [start, end): startedAt < end AND stoppedAt > start
//     (or still running, i.e. stoppedAt is nil / zero).
//  4. Per-task billable seconds = min(end, stoppedAt) − max(start, startedAt),
//     floored at fargateMinBillableSeconds (60 s).
//  5. Multiply by vCPU / memory allocation from the task definition.
func (f *ECSFargateFetcher) FetchConsumption(ctx context.Context, resources []ActiveResource, start, end time.Time) ([]ConsumptionRecord, error) {
	if !end.After(start) {
		return nil, fmt.Errorf("invalid time window: end %v is not after start %v", end, start)
	}

	var records []ConsumptionRecord

	for _, res := range resources {
		serviceName := OutputStr(res.Outputs, "service_name")
		if serviceName == "" {
			continue
		}

		rec, err := f.fetchServiceConsumption(ctx, res, serviceName, start, end)
		if err != nil {
			slog.Warn("ECS consumption fetch failed", "service", serviceName, "error", err)
			continue
		}
		if rec != nil {
			records = append(records, *rec)
		}
	}

	return records, nil
}

// fetchServiceConsumption computes the ConsumptionRecord for a single ECS service.
// Returns nil (no error) when the service had zero billable usage in the window.
func (f *ECSFargateFetcher) fetchServiceConsumption(
	ctx context.Context,
	res ActiveResource,
	serviceName string,
	start, end time.Time,
) (*ConsumptionRecord, error) {

	// ── 1. Collect all task ARNs that could overlap the billing window ───────
	taskARNs, err := f.listAllTaskARNs(ctx, serviceName)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	if len(taskARNs) == 0 {
		return nil, nil
	}

	// ── 2. Describe tasks in batches of 100 (API limit) ──────────────────────
	tasks, err := f.describeTasks(ctx, taskARNs)
	if err != nil {
		return nil, fmt.Errorf("describe tasks: %w", err)
	}

	// ── 3–5. Compute per-task billable seconds ────────────────────────────────
	var totalVCPUSeconds float64
	var totalMemGBSeconds float64
	var billableTaskCount int

	// Cache task-def lookups so we don't repeat the same ARN multiple times.
	type tdAlloc struct{ cpu, memMiB int }
	tdCache := make(map[string]tdAlloc)

	for i := range tasks {
		t := &tasks[i]

		billable := taskBillableSeconds(t, start, end)
		if billable <= 0 {
			continue
		}

		tdARN := ""
		if t.TaskDefinitionArn != nil {
			tdARN = *t.TaskDefinitionArn
		}

		alloc, cached := tdCache[tdARN]
		if !cached {
			cpu, memMiB, tdErr := getTaskDefAllocation(ctx, f.client, t.TaskDefinitionArn)
			if tdErr != nil {
				slog.Warn("get task def allocation failed",
					"task", aws.ToString(t.TaskArn), "error", tdErr)
				continue
			}
			alloc = tdAlloc{cpu: cpu, memMiB: memMiB}
			tdCache[tdARN] = alloc
		}

		vcpu := float64(alloc.cpu) / 1024.0
		memGB := float64(alloc.memMiB) / 1024.0

		totalVCPUSeconds += vcpu * billable
		totalMemGBSeconds += memGB * billable
		billableTaskCount++
	}

	if billableTaskCount == 0 {
		return nil, nil
	}

	return &ConsumptionRecord{
		ResourceURN:  res.URN,
		ProjectID:    res.ProjectID,
		ResourceType: "compute",
		Provider:     "ecs_fargate",
		ProviderID:   serviceName,
		Region:       f.region,
		PeriodStart:  start,
		PeriodEnd:    end,
		Metrics: map[string]float64{
			"vcpu_seconds":      totalVCPUSeconds,
			"memory_gb_seconds": totalMemGBSeconds,
			"task_count":        float64(billableTaskCount),
		},
	}, nil
}

// listAllTaskARNs returns ARNs for both RUNNING and STOPPED tasks in the service.
// STOPPED tasks are available from the ECS API for ~1 hour after they stop,
// which is sufficient for an hourly metering cadence.
func (f *ECSFargateFetcher) listAllTaskARNs(ctx context.Context, serviceName string) ([]string, error) {
	var allARNs []string

	for _, desiredStatus := range []ecstypes.DesiredStatus{
		ecstypes.DesiredStatusRunning,
		ecstypes.DesiredStatusStopped,
	} {
		var nextToken *string
		for {
			out, err := f.client.ListTasks(ctx, &ecs.ListTasksInput{
				Cluster:       &f.clusterName,
				ServiceName:   &serviceName,
				DesiredStatus: desiredStatus,
				NextToken:     nextToken,
			})
			if err != nil {
				return nil, fmt.Errorf("ListTasks (status=%s): %w", desiredStatus, err)
			}
			allARNs = append(allARNs, out.TaskArns...)
			if out.NextToken == nil {
				break
			}
			nextToken = out.NextToken
		}
	}

	return allARNs, nil
}

// describeTasks fetches full task details in batches of 100 (API limit).
func (f *ECSFargateFetcher) describeTasks(ctx context.Context, arns []string) ([]ecstypes.Task, error) {
	const batchSize = 100
	var all []ecstypes.Task

	for i := 0; i < len(arns); i += batchSize {
		batch := arns[i:min(i+batchSize, len(arns))]
		out, err := f.client.DescribeTasks(ctx, &ecs.DescribeTasksInput{
			Cluster: &f.clusterName,
			Tasks:   batch,
		})
		if err != nil {
			return nil, fmt.Errorf("DescribeTasks batch %d: %w", i/batchSize, err)
		}
		all = append(all, out.Tasks...)
	}

	return all, nil
}

// taskBillableSeconds returns the number of billable seconds for a task within
// the metering window [windowStart, windowEnd).
//
// Rules:
//   - If the task never started (startedAt is nil), it contributed no compute.
//   - If the task stopped before the window started, it's outside the window.
//   - If the task started after the window ended, it's outside the window.
//   - Otherwise: clamp overlap to the window and floor at 60 s (Fargate minimum).
func taskBillableSeconds(t *ecstypes.Task, windowStart, windowEnd time.Time) float64 {
	if t.StartedAt == nil {
		return 0
	}

	taskStart := *t.StartedAt

	// Determine when the task stopped (nil = still running → use window end)
	var taskStop time.Time
	if t.StoppedAt != nil && !t.StoppedAt.IsZero() {
		taskStop = *t.StoppedAt
	} else {
		taskStop = windowEnd
	}

	// No overlap: task stopped before window or started after window
	if !taskStop.After(windowStart) || !taskStart.Before(windowEnd) {
		return 0
	}

	// Clamp to window
	billStart := maxTime(taskStart, windowStart)
	billEnd := minTime(taskStop, windowEnd)

	seconds := billEnd.Sub(billStart).Seconds()
	if seconds < fargateMinBillableSeconds {
		seconds = fargateMinBillableSeconds
	}
	return seconds
}

// ─── helpers ────────────────────────────────────────────────────────────────

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// getTaskDefAllocation returns the CPU (units) and Memory (MiB) from a task definition.
func getTaskDefAllocation(ctx context.Context, client *ecs.Client, taskDefARN *string) (cpu int, memMiB int, err error) {
	if taskDefARN == nil {
		return 0, 0, fmt.Errorf("nil task definition ARN")
	}

	out, err := client.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: taskDefARN,
	})
	if err != nil {
		return 0, 0, err
	}

	td := out.TaskDefinition
	if td == nil {
		return 0, 0, fmt.Errorf("nil task definition")
	}

	// Fargate task-level CPU/Memory (stored as strings like "256", "512")
	if td.Cpu != nil {
		fmt.Sscanf(*td.Cpu, "%d", &cpu)
	}
	if td.Memory != nil {
		fmt.Sscanf(*td.Memory, "%d", &memMiB)
	}

	// Fallback: sum container-level independently if task-level not set.
	// CPU and memory are checked separately to avoid double-counting
	// when only one is set at the task level.
	if cpu == 0 {
		for _, c := range td.ContainerDefinitions {
			if c.Cpu != 0 {
				cpu += int(c.Cpu)
			}
		}
	}
	if memMiB == 0 {
		for _, c := range td.ContainerDefinitions {
			if c.Memory != nil && *c.Memory != 0 {
				memMiB += int(*c.Memory)
			}
		}
	}

	// Default Fargate minimum
	if cpu == 0 {
		cpu = 256 // 0.25 vCPU
	}
	if memMiB == 0 {
		memMiB = 512
	}

	return cpu, memMiB, nil
}
