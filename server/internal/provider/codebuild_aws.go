package provider

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
)

// Compile-time interface guard.
var _ Builder = (*AWSCodeBuild)(nil)

// AWSCodeBuild implements Builder using AWS CodeBuild as a fallback for BuildKit.
type AWSCodeBuild struct {
	client      *codebuild.Client
	region      string
	ecrRepoBase string // e.g. "123456789.dkr.ecr.us-east-1.amazonaws.com/launchkit"
}

func NewAWSCodeBuild(ctx context.Context, region, ecrRepoBase string) (*AWSCodeBuild, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return &AWSCodeBuild{
		client:      codebuild.NewFromConfig(cfg),
		region:      region,
		ecrRepoBase: ecrRepoBase,
	}, nil
}

func (cb *AWSCodeBuild) Build(ctx context.Context, opts BuildOpts) (*BuildResult, error) {
	projectName := "launchkit-build"

	// Build environment variables
	envVars := []cbtypes.EnvironmentVariable{
		{Name: aws.String("SOURCE_DIR"), Value: aws.String(opts.SourceDir)},
		{Name: aws.String("IMAGE_TAG"), Value: aws.String(opts.ImageTag)},
		{Name: aws.String("DIST_OUTPUT_KEY"), Value: aws.String(opts.DistOutputKey)},
		{Name: aws.String("PROJECT_ID"), Value: aws.String(opts.ProjectID)},
		{Name: aws.String("S3_BUCKET"), Value: aws.String(opts.SourceBucket)},
	}
	for k, v := range opts.BuildArgs {
		envVars = append(envVars, cbtypes.EnvironmentVariable{
			Name:  aws.String("BUILD_ARG_" + k),
			Value: aws.String(v),
		})
	}

	// Determine buildspec based on whether this is a backend or frontend build
	var buildspec string
	if opts.ImageTag != "" {
		buildspec = cb.backendBuildspec(opts)
	} else {
		buildspec = cb.frontendBuildspec(opts)
	}

	// Start build
	result, err := cb.client.StartBuild(ctx, &codebuild.StartBuildInput{
		ProjectName:                  aws.String(projectName),
		SourceTypeOverride:           cbtypes.SourceTypeS3,
		SourceLocationOverride:       aws.String(opts.SourceBucket + "/" + opts.SourceKey),
		BuildspecOverride:            aws.String(buildspec),
		EnvironmentVariablesOverride: envVars,
		TimeoutInMinutesOverride:     aws.Int32(10),
	})
	if err != nil {
		return nil, fmt.Errorf("start CodeBuild: %w", err)
	}

	buildID := aws.ToString(result.Build.Id)
	slog.Info("CodeBuild started", "build_id", buildID)

	// Poll for completion
	return cb.pollBuild(ctx, buildID)
}

func (cb *AWSCodeBuild) pollBuild(ctx context.Context, buildID string) (*BuildResult, error) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	timeout := time.After(10 * time.Minute)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout:
			return nil, fmt.Errorf("CodeBuild timeout after 10 minutes")
		case <-ticker.C:
			result, err := cb.client.BatchGetBuilds(ctx, &codebuild.BatchGetBuildsInput{
				Ids: []string{buildID},
			})
			if err != nil {
				return nil, fmt.Errorf("get build status: %w", err)
			}
			if len(result.Builds) == 0 {
				continue
			}

			build := result.Builds[0]
			switch build.BuildStatus {
			case cbtypes.StatusTypeSucceeded:
				imageURI := ""
				// Extract image URI from environment variables
				for _, env := range build.Environment.EnvironmentVariables {
					if aws.ToString(env.Name) == "IMAGE_TAG" {
						imageURI = aws.ToString(env.Value)
					}
				}
				logsURL := ""
				if build.Logs != nil {
					logsURL = aws.ToString(build.Logs.DeepLink)
				}
				return &BuildResult{
					ImageURI: imageURI,
					LogsURL:  logsURL,
				}, nil

			case cbtypes.StatusTypeFailed, cbtypes.StatusTypeFault, cbtypes.StatusTypeStopped, cbtypes.StatusTypeTimedOut:
				logsURL := ""
				if build.Logs != nil {
					logsURL = aws.ToString(build.Logs.DeepLink)
				}
				return nil, fmt.Errorf("CodeBuild %s (logs: %s)", build.BuildStatus, logsURL)
			}
		}
	}
}

func (cb *AWSCodeBuild) backendBuildspec(opts BuildOpts) string {
	// Inline buildspec for building and pushing a Docker image
	buildArgs := ""
	for k := range opts.BuildArgs {
		buildArgs += fmt.Sprintf(" --build-arg %s=$BUILD_ARG_%s", k, k)
	}

	sourceDir := "."
	if opts.SourceDir != "" {
		sourceDir = opts.SourceDir
	}

	return fmt.Sprintf(`version: 0.2
phases:
  pre_build:
    commands:
      - echo Logging in to ECR...
      - aws ecr get-login-password --region %s | docker login --username AWS --password-stdin %s
  build:
    commands:
      - cd %s
      - |
        if [ -f Dockerfile ]; then
          docker build -t %s%s .
        else
          curl -sL https://github.com/railwayapp/railpack/releases/download/v0.23.0/railpack-v0.23.0-x86_64-unknown-linux-musl.tar.gz | tar xz -C /usr/local/bin
          docker buildx create --name rb --driver docker-container --use
          docker buildx build --frontend gateway.v0 --opt source=ghcr.io/railwayapp/railpack-frontend -t %s --push%s .
        fi
  post_build:
    commands:
      - docker push %s
`, cb.region, strings.Split(cb.ecrRepoBase, "/")[0],
		sourceDir,
		opts.ImageTag, buildArgs,
		opts.ImageTag, buildArgs,
		opts.ImageTag)
}

func (cb *AWSCodeBuild) frontendBuildspec(opts BuildOpts) string {
	sourceDir := "."
	if opts.SourceDir != "" {
		sourceDir = opts.SourceDir
	}

	buildArgs := ""
	for k := range opts.BuildArgs {
		buildArgs += fmt.Sprintf("export %s=$BUILD_ARG_%s\n      - ", k, k)
	}

	return fmt.Sprintf(`version: 0.2
phases:
  install:
    runtime-versions:
      nodejs: 22
  build:
    commands:
      - cd %s
      - %snpm install
      - npm run build
  post_build:
    commands:
      - cd %s
      - tar czf /tmp/dist.tar.gz -C dist .
      - aws s3 cp /tmp/dist.tar.gz s3://%s/%s
`, sourceDir, buildArgs, sourceDir, opts.SourceBucket, opts.DistOutputKey)
}
