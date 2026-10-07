package provider

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	run "cloud.google.com/go/run/apiv2"
	"cloud.google.com/go/run/apiv2/runpb"

	logging "cloud.google.com/go/logging/apiv2"
	loggingpb "cloud.google.com/go/logging/apiv2/loggingpb"

	iampb "cloud.google.com/go/iam/apiv1/iampb"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Compile-time interface guard.
var _ Compute = (*CloudRun)(nil)

// CloudRun implements Compute using Google Cloud Run.
type CloudRun struct {
	services *run.ServicesClient
	logs     *logging.Client
	project  string
}

func NewCloudRun(ctx context.Context, projectID string) (*CloudRun, error) {
	svcClient, err := run.NewServicesClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create Cloud Run services client: %w", err)
	}

	logClient, err := logging.NewClient(ctx)
	if err != nil {
		svcClient.Close()
		return nil, fmt.Errorf("create Cloud Logging client: %w", err)
	}

	return &CloudRun{
		services: svcClient,
		logs:     logClient,
		project:  projectID,
	}, nil
}

func (cr *CloudRun) CreateService(ctx context.Context, opts DeployOpts) (string, error) {
	parent := fmt.Sprintf("projects/%s/locations/%s", cr.project, opts.Region)

	envVars := makeEnvVars(opts.EnvVars)
	port := int32(opts.Port)
	if port == 0 {
		port = 8080
	}

	tmpl := &runpb.RevisionTemplate{
		Containers: []*runpb.Container{
			{
				Image: coalesce(opts.ImageURI, "us-docker.pkg.dev/cloudrun/container/hello"),
				Ports: []*runpb.ContainerPort{{ContainerPort: port}},
				Env:   envVars,
				Resources: &runpb.ResourceRequirements{
					Limits: map[string]string{
						"memory": coalesce(opts.Memory, "512Mi"),
						"cpu":    coalesce(opts.CPU, "1"),
					},
				},
			},
		},
		Scaling: &runpb.RevisionScaling{
			MinInstanceCount: int32(opts.MinScale),
			MaxInstanceCount: int32(coalesceInt(opts.MaxScale, 10)),
		},
	}

	// WebSocket support: session affinity keeps connections routed to the same instance.
	if opts.SessionAffinity {
		tmpl.SessionAffinity = true
	}

	// Request timeout: default 300s, extended up to 3600s for WebSocket/long-lived connections.
	if opts.RequestTimeout > 0 {
		timeout := opts.RequestTimeout
		if timeout > 3600 {
			timeout = 3600
		}
		tmpl.Timeout = durationpb.New(time.Duration(timeout) * time.Second)
	}

	// Max concurrent requests per container instance.
	if opts.Concurrency > 0 {
		tmpl.Scaling.MaxInstanceCount = int32(coalesceInt(opts.MaxScale, 10))
		tmpl.MaxInstanceRequestConcurrency = int32(opts.Concurrency)
	}

	service := &runpb.Service{
		Template: tmpl,
		Ingress:  runpb.IngressTraffic_INGRESS_TRAFFIC_ALL,
	}

	op, err := cr.services.CreateService(ctx, &runpb.CreateServiceRequest{
		Parent:    parent,
		Service:   service,
		ServiceId: opts.ServiceName,
	})
	if err != nil {
		// AlreadyExists → return the existing service URL (idempotent)
		if status.Code(err) == codes.AlreadyExists {
			name := fmt.Sprintf("%s/services/%s", parent, opts.ServiceName)
			existing, getErr := cr.services.GetService(ctx, &runpb.GetServiceRequest{Name: name})
			if getErr != nil {
				return "", fmt.Errorf("get existing service: %w", getErr)
			}
			return existing.Uri, nil
		}
		return "", fmt.Errorf("create Cloud Run service: %w", err)
	}

	resp, err := op.Wait(ctx)
	if err != nil {
		return "", fmt.Errorf("wait for service creation: %w", err)
	}

	name := fmt.Sprintf("%s/services/%s", parent, opts.ServiceName)
	if err := cr.allowUnauthenticated(ctx, name); err != nil {
		slog.Warn("failed to set IAM policy for unauthenticated access", "service", opts.ServiceName, "error", err)
	}

	return resp.Uri, nil
}

func (cr *CloudRun) Deploy(ctx context.Context, opts DeployOpts) (string, error) {
	name := fmt.Sprintf("projects/%s/locations/%s/services/%s", cr.project, opts.Region, opts.ServiceName)

	svc, err := cr.services.GetService(ctx, &runpb.GetServiceRequest{Name: name})
	if err != nil {
		return "", fmt.Errorf("get service for update: %w", err)
	}

	envVars := makeEnvVars(opts.EnvVars)
	port := int32(opts.Port)
	if port == 0 {
		port = 8080
	}

	if len(svc.Template.Containers) > 0 {
		svc.Template.Containers[0].Image = opts.ImageURI
		svc.Template.Containers[0].Env = envVars
		svc.Template.Containers[0].Ports = []*runpb.ContainerPort{{ContainerPort: port}}
	}

	// WebSocket support: session affinity keeps connections routed to the same instance.
	if opts.SessionAffinity {
		svc.Template.SessionAffinity = true
	}

	// Request timeout: default 300s, extended up to 3600s for WebSocket/long-lived connections.
	if opts.RequestTimeout > 0 {
		timeout := opts.RequestTimeout
		if timeout > 3600 {
			timeout = 3600
		}
		svc.Template.Timeout = durationpb.New(time.Duration(timeout) * time.Second)
	}

	// Max concurrent requests per container instance.
	if opts.Concurrency > 0 {
		svc.Template.MaxInstanceRequestConcurrency = int32(opts.Concurrency)
	}

	op, err := cr.services.UpdateService(ctx, &runpb.UpdateServiceRequest{
		Service: svc,
	})
	if err != nil {
		return "", fmt.Errorf("update Cloud Run service: %w", err)
	}

	resp, err := op.Wait(ctx)
	if err != nil {
		return "", fmt.Errorf("wait for service update: %w", err)
	}

	if err := cr.allowUnauthenticated(ctx, name); err != nil {
		slog.Warn("failed to set IAM policy for unauthenticated access", "service", opts.ServiceName, "error", err)
	}

	return resp.Uri, nil
}

func (cr *CloudRun) allowUnauthenticated(ctx context.Context, serviceName string) error {
	policy, err := cr.services.GetIamPolicy(ctx, &iampb.GetIamPolicyRequest{
		Resource: serviceName,
	})
	if err != nil {
		return fmt.Errorf("get IAM policy: %w", err)
	}

	hasBinding := false
	for i, b := range policy.Bindings {
		if b.Role == "roles/run.invoker" {
			for _, m := range b.Members {
				if m == "allUsers" {
					hasBinding = true
					break
				}
			}
			if !hasBinding {
				policy.Bindings[i].Members = append(policy.Bindings[i].Members, "allUsers")
				hasBinding = true
			}
			break
		}
	}

	if !hasBinding {
		policy.Bindings = append(policy.Bindings, &iampb.Binding{
			Role:    "roles/run.invoker",
			Members: []string{"allUsers"},
		})
	}

	_, err = cr.services.SetIamPolicy(ctx, &iampb.SetIamPolicyRequest{
		Resource: serviceName,
		Policy:   policy,
	})
	return err
}

func (cr *CloudRun) GetStatus(ctx context.Context, serviceName, region string) (*ServiceStatus, error) {
	name := fmt.Sprintf("projects/%s/locations/%s/services/%s", cr.project, region, serviceName)

	svc, err := cr.services.GetService(ctx, &runpb.GetServiceRequest{Name: name})
	if err != nil {
		return nil, fmt.Errorf("get service status: %w", err)
	}

	status := "deploying"
	for _, cond := range svc.Conditions {
		if cond.Type == "Ready" {
			if cond.State == runpb.Condition_CONDITION_SUCCEEDED {
				status = "ready"
			} else if cond.State == runpb.Condition_CONDITION_FAILED {
				status = "failed"
			}
			break
		}
	}

	return &ServiceStatus{
		Name:   serviceName,
		URL:    svc.Uri,
		Status: status,
	}, nil
}

var safeNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func (cr *CloudRun) GetLogs(ctx context.Context, serviceName, region string, limit int) ([]LogEntry, error) {
	if !safeNameRe.MatchString(serviceName) {
		return nil, fmt.Errorf("invalid service name: %q", serviceName)
	}
	if !safeNameRe.MatchString(region) {
		return nil, fmt.Errorf("invalid region: %q", region)
	}
	filter := fmt.Sprintf(
		`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND resource.labels.location="%s"`,
		serviceName, region,
	)

	it := cr.logs.ListLogEntries(ctx, &loggingpb.ListLogEntriesRequest{
		ResourceNames: []string{"projects/" + cr.project},
		Filter:        filter,
		OrderBy:       "timestamp desc",
		PageSize:      int32(limit),
	})

	var entries []LogEntry
	for i := 0; i < limit; i++ {
		entry, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return entries, fmt.Errorf("read log entry: %w", err)
		}

		msg := ""
		if entry.GetTextPayload() != "" {
			msg = entry.GetTextPayload()
		} else if entry.GetJsonPayload() != nil {
			msg = entry.GetJsonPayload().String()
		}

		var ts time.Time
		if entry.Timestamp != nil {
			ts = entry.Timestamp.AsTime()
		}

		entries = append(entries, LogEntry{
			Timestamp: ts,
			Severity:  entry.Severity.String(),
			Message:   msg,
		})
	}

	return entries, nil
}

func (cr *CloudRun) DeleteService(ctx context.Context, serviceName, region string) error {
	name := fmt.Sprintf("projects/%s/locations/%s/services/%s", cr.project, region, serviceName)

	op, err := cr.services.DeleteService(ctx, &runpb.DeleteServiceRequest{Name: name})
	if err != nil {
		return fmt.Errorf("delete Cloud Run service: %w", err)
	}

	_, err = op.Wait(ctx)
	return err
}

func (cr *CloudRun) Close() error {
	return errors.Join(cr.services.Close(), cr.logs.Close())
}

func makeEnvVars(m map[string]string) []*runpb.EnvVar {
	vars := make([]*runpb.EnvVar, 0, len(m))
	for k, v := range m {
		vars = append(vars, &runpb.EnvVar{
			Name:   k,
			Values: &runpb.EnvVar_Value{Value: v},
		})
	}
	return vars
}

func coalesce(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

func coalesceInt(v, fallback int) int {
	if v != 0 {
		return v
	}
	return fallback
}
