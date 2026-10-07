package provider

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	ec2svc "github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
)

// Compile-time interface guard.
var _ Compute = (*ECSFargate)(nil)

// ECSFargateConfig holds the configuration for ECS Fargate deployments.
type ECSFargateConfig struct {
	Region           string
	ClusterName      string
	SubnetIDs        []string
	SecurityGroupIDs []string
	ExecutionRoleARN string
	TaskRoleARN      string
	ALBListenerARN   string // HTTPS listener for path-based routing

	// StabilizeTimeout is how long to wait for a service to reach stable state after deploy.
	// Zero uses the default of 5 minutes.
	StabilizeTimeout time.Duration
}

// ECSFargate implements Compute using AWS ECS with Fargate launch type.
type ECSFargate struct {
	ecs    *ecs.Client
	logs   *cloudwatchlogs.Client
	alb    *elbv2.Client
	ec2    *ec2svc.Client
	cfg    ECSFargateConfig
	albDNS string // cached ALB DNS name; empty when ALBListenerARN not set
}

func NewECSFargate(ctx context.Context, cfg ECSFargateConfig) (*ECSFargate, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	e := &ECSFargate{
		ecs:  ecs.NewFromConfig(awsCfg),
		logs: cloudwatchlogs.NewFromConfig(awsCfg),
		alb:  elbv2.NewFromConfig(awsCfg),
		ec2:  ec2svc.NewFromConfig(awsCfg),
		cfg:  cfg,
	}

	if cfg.ALBListenerARN != "" {
		dns, err := e.fetchALBDNS(ctx)
		if err != nil {
			// Non-fatal: fall back to placeholder URL
			slog.Warn("ECS Fargate: failed to fetch ALB DNS, using placeholder URLs", "error", err)
		} else {
			e.albDNS = dns
			slog.Info("ECS Fargate: ALB configured", "dns", dns)
		}
	}

	return e, nil
}

// fetchALBDNS resolves the DNS name of the load balancer behind the configured listener ARN.
func (e *ECSFargate) fetchALBDNS(ctx context.Context) (string, error) {
	listenerResult, err := e.alb.DescribeListeners(ctx, &elbv2.DescribeListenersInput{
		ListenerArns: []string{e.cfg.ALBListenerARN},
	})
	if err != nil {
		return "", fmt.Errorf("describe listener: %w", err)
	}
	if len(listenerResult.Listeners) == 0 {
		return "", fmt.Errorf("listener %s not found", e.cfg.ALBListenerARN)
	}

	lbARN := aws.ToString(listenerResult.Listeners[0].LoadBalancerArn)
	lbResult, err := e.alb.DescribeLoadBalancers(ctx, &elbv2.DescribeLoadBalancersInput{
		LoadBalancerArns: []string{lbARN},
	})
	if err != nil {
		return "", fmt.Errorf("describe load balancer: %w", err)
	}
	if len(lbResult.LoadBalancers) == 0 {
		return "", fmt.Errorf("load balancer not found for ARN %s", lbARN)
	}
	return aws.ToString(lbResult.LoadBalancers[0].DNSName), nil
}

// serviceURL returns the public URL for a service, using the ALB if configured.
func (e *ECSFargate) serviceURL(serviceName string) string {
	if e.albDNS != "" {
		return "https://" + e.albDNS + "/" + serviceName
	}
	return fmt.Sprintf("https://%s.launchkit.app", serviceName)
}

// vpcIDFromSubnet returns the VPC ID for the first configured subnet.
func (e *ECSFargate) vpcIDFromSubnet(ctx context.Context) (string, error) {
	if len(e.cfg.SubnetIDs) == 0 {
		return "", fmt.Errorf("no subnets configured")
	}
	result, err := e.ec2.DescribeSubnets(ctx, &ec2svc.DescribeSubnetsInput{
		SubnetIds: []string{e.cfg.SubnetIDs[0]},
	})
	if err != nil {
		return "", fmt.Errorf("describe subnet %s: %w", e.cfg.SubnetIDs[0], err)
	}
	if len(result.Subnets) == 0 {
		return "", fmt.Errorf("subnet %s not found", e.cfg.SubnetIDs[0])
	}
	return aws.ToString(result.Subnets[0].VpcId), nil
}

// ensureTargetGroup creates or retrieves an ALB target group for a service.
// Returns the target group ARN.
func (e *ECSFargate) ensureTargetGroup(ctx context.Context, serviceName string, port int32) (string, error) {
	// Target group names: max 32 chars, alphanumeric + hyphens
	tgName := "lk-" + serviceName
	if len(tgName) > 32 {
		tgName = tgName[:32]
	}

	// Check if it already exists
	result, err := e.alb.DescribeTargetGroups(ctx, &elbv2.DescribeTargetGroupsInput{
		Names: []string{tgName},
	})
	if err == nil && len(result.TargetGroups) > 0 {
		return aws.ToString(result.TargetGroups[0].TargetGroupArn), nil
	}

	vpcID, err := e.vpcIDFromSubnet(ctx)
	if err != nil {
		return "", fmt.Errorf("get VPC ID: %w", err)
	}

	created, err := e.alb.CreateTargetGroup(ctx, &elbv2.CreateTargetGroupInput{
		Name:                       aws.String(tgName),
		Protocol:                   elbv2types.ProtocolEnumHttp,
		Port:                       aws.Int32(port),
		VpcId:                      aws.String(vpcID),
		TargetType:                 elbv2types.TargetTypeEnumIp, // Fargate uses awsvpc with IP targets
		HealthCheckPath:            aws.String("/"),
		HealthCheckIntervalSeconds: aws.Int32(30),
		HealthyThresholdCount:      aws.Int32(2),
		UnhealthyThresholdCount:    aws.Int32(3),
	})
	if err != nil {
		return "", fmt.Errorf("create target group: %w", err)
	}
	return aws.ToString(created.TargetGroups[0].TargetGroupArn), nil
}

// ensureListenerRule creates or updates the ALB listener rule routing
// /{serviceName}* to the given target group.
func (e *ECSFargate) ensureListenerRule(ctx context.Context, tgARN, serviceName string) error {
	pathPattern := "/" + serviceName + "*"

	rulesResult, err := e.alb.DescribeRules(ctx, &elbv2.DescribeRulesInput{
		ListenerArn: aws.String(e.cfg.ALBListenerARN),
	})
	if err != nil {
		return fmt.Errorf("describe listener rules: %w", err)
	}

	// Check for existing rule matching this service's path
	for _, rule := range rulesResult.Rules {
		for _, cond := range rule.Conditions {
			if aws.ToString(cond.Field) == "path-pattern" && cond.PathPatternConfig != nil {
				for _, v := range cond.PathPatternConfig.Values {
					if v == pathPattern {
						// Update existing rule to point to new TG
						_, err = e.alb.ModifyRule(ctx, &elbv2.ModifyRuleInput{
							RuleArn: rule.RuleArn,
							Actions: []elbv2types.Action{{
								Type: elbv2types.ActionTypeEnumForward,
								ForwardConfig: &elbv2types.ForwardActionConfig{
									TargetGroups: []elbv2types.TargetGroupTuple{{
										TargetGroupArn: aws.String(tgARN),
									}},
								},
							}},
						})
						return err
					}
				}
			}
		}
	}

	// Find the highest priority to add the new rule after existing ones
	maxPriority := int32(0)
	for _, rule := range rulesResult.Rules {
		if aws.ToString(rule.Priority) == "default" {
			continue
		}
		p, _ := strconv.ParseInt(aws.ToString(rule.Priority), 10, 32)
		if int32(p) > maxPriority {
			maxPriority = int32(p)
		}
	}

	_, err = e.alb.CreateRule(ctx, &elbv2.CreateRuleInput{
		ListenerArn: aws.String(e.cfg.ALBListenerARN),
		Priority:    aws.Int32(maxPriority + 10),
		Conditions: []elbv2types.RuleCondition{{
			Field: aws.String("path-pattern"),
			PathPatternConfig: &elbv2types.PathPatternConditionConfig{
				Values: []string{pathPattern},
			},
		}},
		Actions: []elbv2types.Action{{
			Type: elbv2types.ActionTypeEnumForward,
			ForwardConfig: &elbv2types.ForwardActionConfig{
				TargetGroups: []elbv2types.TargetGroupTuple{{
					TargetGroupArn: aws.String(tgARN),
				}},
			},
		}},
	})
	return err
}

func (e *ECSFargate) CreateService(ctx context.Context, opts DeployOpts) (string, error) {
	family := opts.ServiceName
	logGroup := "/ecs/launchkit/" + family

	// Ensure log group exists
	e.logs.CreateLogGroup(ctx, &cloudwatchlogs.CreateLogGroupInput{
		LogGroupName: aws.String(logGroup),
	}) // ignore AlreadyExists error

	image := "public.ecr.aws/docker/library/httpd:latest"
	if opts.ImageURI != "" {
		image = opts.ImageURI
	}

	port := int32(opts.Port)
	if port == 0 {
		port = 8080
	}
	cpu, mem := fargateResources(opts.CPU, opts.Memory)

	// Register task definition
	taskDef, err := e.ecs.RegisterTaskDefinition(ctx, &ecs.RegisterTaskDefinitionInput{
		Family:                  aws.String(family),
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
		Cpu:                     aws.String(cpu),
		Memory:                  aws.String(mem),
		ExecutionRoleArn:        aws.String(e.cfg.ExecutionRoleARN),
		TaskRoleArn:             nilIfEmpty(e.cfg.TaskRoleARN),
		ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Name:      aws.String("app"),
			Image:     aws.String(image),
			Essential: aws.Bool(true),
			PortMappings: []ecstypes.PortMapping{{
				ContainerPort: aws.Int32(port),
				Protocol:      ecstypes.TransportProtocolTcp,
			}},
			LogConfiguration: &ecstypes.LogConfiguration{
				LogDriver: ecstypes.LogDriverAwslogs,
				Options: map[string]string{
					"awslogs-group":         logGroup,
					"awslogs-region":        e.cfg.Region,
					"awslogs-stream-prefix": "ecs",
				},
			},
		}},
	})
	if err != nil {
		return "", fmt.Errorf("register task definition: %w", err)
	}
	taskDefARN := aws.ToString(taskDef.TaskDefinition.TaskDefinitionArn)

	createInput := &ecs.CreateServiceInput{
		ServiceName:    aws.String(family),
		Cluster:        aws.String(e.cfg.ClusterName),
		TaskDefinition: aws.String(taskDefARN),
		DesiredCount:   aws.Int32(1),
		LaunchType:     ecstypes.LaunchTypeFargate,
		NetworkConfiguration: &ecstypes.NetworkConfiguration{
			AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{
				Subnets:        e.cfg.SubnetIDs,
				SecurityGroups: e.cfg.SecurityGroupIDs,
				AssignPublicIp: ecstypes.AssignPublicIpEnabled,
			},
		},
	}

	// Attach to ALB if configured
	if e.cfg.ALBListenerARN != "" {
		tgARN, tgErr := e.ensureTargetGroup(ctx, family, port)
		if tgErr != nil {
			slog.Warn("ECS Fargate: ALB target group creation failed, deploying without load balancer",
				"service", family, "error", tgErr)
		} else {
			if ruleErr := e.ensureListenerRule(ctx, tgARN, family); ruleErr != nil {
				slog.Warn("ECS Fargate: ALB listener rule creation failed", "service", family, "error", ruleErr)
			}
			createInput.LoadBalancers = []ecstypes.LoadBalancer{{
				ContainerName:  aws.String("app"),
				ContainerPort:  aws.Int32(port),
				TargetGroupArn: aws.String(tgARN),
			}}
			createInput.HealthCheckGracePeriodSeconds = aws.Int32(60)
		}
	}

	_, err = e.ecs.CreateService(ctx, createInput)
	if err != nil && !isECSAlreadyExists(err) {
		return "", fmt.Errorf("create ECS service: %w", err)
	}

	url := e.serviceURL(family)
	slog.Info("ECS service created", "service", family, "url", url)
	return url, nil
}

func (e *ECSFargate) Deploy(ctx context.Context, opts DeployOpts) (string, error) {
	family := opts.ServiceName
	logGroup := "/ecs/launchkit/" + family

	port := int32(opts.Port)
	if port == 0 {
		port = 8080
	}
	cpu, mem := fargateResources(opts.CPU, opts.Memory)

	// Build env vars
	envVars := make([]ecstypes.KeyValuePair, 0, len(opts.EnvVars))
	for k, v := range opts.EnvVars {
		envVars = append(envVars, ecstypes.KeyValuePair{
			Name:  aws.String(k),
			Value: aws.String(v),
		})
	}

	// Register new task definition revision
	taskDef, err := e.ecs.RegisterTaskDefinition(ctx, &ecs.RegisterTaskDefinitionInput{
		Family:                  aws.String(family),
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
		Cpu:                     aws.String(cpu),
		Memory:                  aws.String(mem),
		ExecutionRoleArn:        aws.String(e.cfg.ExecutionRoleARN),
		TaskRoleArn:             nilIfEmpty(e.cfg.TaskRoleARN),
		ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Name:      aws.String("app"),
			Image:     aws.String(opts.ImageURI),
			Essential: aws.Bool(true),
			PortMappings: []ecstypes.PortMapping{{
				ContainerPort: aws.Int32(port),
				Protocol:      ecstypes.TransportProtocolTcp,
			}},
			Environment: envVars,
			LogConfiguration: &ecstypes.LogConfiguration{
				LogDriver: ecstypes.LogDriverAwslogs,
				Options: map[string]string{
					"awslogs-group":         logGroup,
					"awslogs-region":        e.cfg.Region,
					"awslogs-stream-prefix": "ecs",
				},
			},
		}},
	})
	if err != nil {
		return "", fmt.Errorf("register task definition: %w", err)
	}
	taskDefARN := aws.ToString(taskDef.TaskDefinition.TaskDefinitionArn)

	// Ensure ALB listener rule points to the target group (idempotent)
	if e.cfg.ALBListenerARN != "" {
		if tgARN, err := e.ensureTargetGroup(ctx, family, port); err == nil {
			_ = e.ensureListenerRule(ctx, tgARN, family)
		}
	}

	// Update ECS service to use new task definition revision
	_, err = e.ecs.UpdateService(ctx, &ecs.UpdateServiceInput{
		Service:            aws.String(family),
		Cluster:            aws.String(e.cfg.ClusterName),
		TaskDefinition:     aws.String(taskDefARN),
		ForceNewDeployment: true,
	})
	if err != nil {
		return "", fmt.Errorf("update ECS service: %w", err)
	}

	// Wait for service to stabilize (tasks running with new revision)
	waiter := ecs.NewServicesStableWaiter(e.ecs)
	if err := waiter.Wait(ctx, &ecs.DescribeServicesInput{
		Services: []string{family},
		Cluster:  aws.String(e.cfg.ClusterName),
	}, orDefault(e.cfg.StabilizeTimeout, 5*time.Minute)); err != nil {
		slog.Warn("ECS service did not stabilize within timeout", "service", family, "error", err)
	}

	return e.serviceURL(family), nil
}

func (e *ECSFargate) GetStatus(ctx context.Context, serviceName, region string) (*ServiceStatus, error) {
	result, err := e.ecs.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Services: []string{serviceName},
		Cluster:  aws.String(e.cfg.ClusterName),
	})
	if err != nil {
		return nil, fmt.Errorf("describe service: %w", err)
	}

	if len(result.Services) == 0 {
		return &ServiceStatus{Name: serviceName, Status: "not_found"}, nil
	}

	svc := result.Services[0]
	status := "deploying"
	if aws.ToString(svc.Status) == "ACTIVE" && svc.RunningCount == svc.DesiredCount {
		status = "ready"
	} else if aws.ToString(svc.Status) == "INACTIVE" || aws.ToString(svc.Status) == "DRAINING" {
		status = "failed"
	}

	return &ServiceStatus{
		Name:   serviceName,
		URL:    e.serviceURL(serviceName),
		Status: status,
	}, nil
}

func (e *ECSFargate) GetLogs(ctx context.Context, serviceName, region string, limit int) ([]LogEntry, error) {
	logGroup := "/ecs/launchkit/" + serviceName

	result, err := e.logs.FilterLogEvents(ctx, &cloudwatchlogs.FilterLogEventsInput{
		LogGroupName: aws.String(logGroup),
		Limit:        aws.Int32(int32(limit)),
		Interleaved:  aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("filter log events: %w", err)
	}

	entries := make([]LogEntry, 0, len(result.Events))
	for _, event := range result.Events {
		entries = append(entries, LogEntry{
			Timestamp: time.UnixMilli(aws.ToInt64(event.Timestamp)),
			Severity:  "DEFAULT",
			Message:   aws.ToString(event.Message),
		})
	}
	return entries, nil
}

func (e *ECSFargate) DeleteService(ctx context.Context, serviceName, region string) error {
	// Scale to 0 before deleting
	_, err := e.ecs.UpdateService(ctx, &ecs.UpdateServiceInput{
		Service:      aws.String(serviceName),
		Cluster:      aws.String(e.cfg.ClusterName),
		DesiredCount: aws.Int32(0),
	})
	if err != nil {
		slog.Warn("failed to scale down service before deletion", "service", serviceName, "error", err)
	}

	_, err = e.ecs.DeleteService(ctx, &ecs.DeleteServiceInput{
		Service: aws.String(serviceName),
		Cluster: aws.String(e.cfg.ClusterName),
		Force:   aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("delete ECS service: %w", err)
	}
	return nil
}

// fargateResources rounds CPU and memory to the nearest valid Fargate combination.
// See: https://docs.aws.amazon.com/AmazonECS/latest/developerguide/task-cpu-memory-error.html
func fargateResources(cpu, memory string) (string, string) {
	if cpu == "" {
		cpu = "256"
	}
	if memory == "" {
		memory = "512"
	}

	// Normalize common formats
	switch {
	case cpu == "1" || cpu == "1000m":
		cpu = "1024"
	case cpu == "0.25" || cpu == "250m":
		cpu = "256"
	case cpu == "0.5" || cpu == "500m":
		cpu = "512"
	case cpu == "2" || cpu == "2000m":
		cpu = "2048"
	case cpu == "4" || cpu == "4000m":
		cpu = "4096"
	}

	// Strip "Mi" suffix from memory (e.g. "512Mi" → "512")
	memory = strings.TrimSuffix(memory, "Mi")

	return cpu, memory
}

func isECSAlreadyExists(err error) bool {
	return strings.Contains(err.Error(), "already exists")
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
