package provider

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	cloudbuild "cloud.google.com/go/cloudbuild/apiv1"
	cloudbuildpb "cloud.google.com/go/cloudbuild/apiv1/v2/cloudbuildpb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

// CloudBuild implements Builder using Google Cloud Build.
type CloudBuild struct {
	client       *cloudbuild.Client
	projectID    string
	region       string
	buildTimeout time.Duration
}

func NewCloudBuild(ctx context.Context, projectID, region string, buildTimeout time.Duration) (*CloudBuild, error) {
	client, err := cloudbuild.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create Cloud Build client: %w", err)
	}
	return &CloudBuild{
		client:       client,
		projectID:    projectID,
		region:       region,
		buildTimeout: orDefault(buildTimeout, 10*time.Minute),
	}, nil
}

func (cb *CloudBuild) Build(ctx context.Context, opts BuildOpts) (*BuildResult, error) {
	steps, images := cb.buildSteps(opts)

	build := &cloudbuildpb.Build{
		Source: &cloudbuildpb.Source{
			Source: &cloudbuildpb.Source_StorageSource{
				StorageSource: &cloudbuildpb.StorageSource{
					Bucket: opts.SourceBucket,
					Object: opts.SourceKey,
				},
			},
		},
		Steps:  steps,
		Images: images,
		Options: &cloudbuildpb.BuildOptions{
			MachineType: cloudbuildpb.BuildOptions_E2_HIGHCPU_8,
			Logging:     cloudbuildpb.BuildOptions_CLOUD_LOGGING_ONLY,
		},
		Timeout: durationpb.New(cb.buildTimeout),
	}

	op, err := cb.client.CreateBuild(ctx, &cloudbuildpb.CreateBuildRequest{
		ProjectId: cb.projectID,
		Build:     build,
	})
	if err != nil {
		return nil, fmt.Errorf("submit build: %w", err)
	}

	// Extract build ID from operation metadata
	meta := &cloudbuildpb.BuildOperationMetadata{}
	if err := proto.Unmarshal(op.GetMetadata().GetValue(), meta); err != nil {
		return nil, fmt.Errorf("parse build metadata: %w", err)
	}

	buildID := meta.GetBuild().GetId()
	slog.Info("cloud build submitted", "build_id", buildID, "project", cb.projectID)

	// Poll until complete
	return cb.pollBuild(ctx, buildID)
}

func (cb *CloudBuild) pollBuild(ctx context.Context, buildID string) (*BuildResult, error) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			b, err := cb.client.GetBuild(ctx, &cloudbuildpb.GetBuildRequest{
				ProjectId: cb.projectID,
				Id:        buildID,
			})
			if err != nil {
				return nil, fmt.Errorf("get build status: %w", err)
			}

			switch b.GetStatus() {
			case cloudbuildpb.Build_SUCCESS:
				imageURI := ""
				if images := b.GetImages(); len(images) > 0 {
					imageURI = images[0]
				}
				return &BuildResult{
					ImageURI: imageURI,
					LogsURL:  b.GetLogUrl(),
				}, nil
			case cloudbuildpb.Build_FAILURE, cloudbuildpb.Build_INTERNAL_ERROR, cloudbuildpb.Build_TIMEOUT, cloudbuildpb.Build_CANCELLED:
				return nil, fmt.Errorf("build %s: %s: %s", buildID, b.GetStatus(), b.GetStatusDetail())
			default:
				slog.Debug("build in progress", "build_id", buildID, "status", b.GetStatus())
			}
		}
	}
}

// shellEscape escapes a string for safe interpolation inside single-quoted shell arguments.
// It replaces every single quote with: '"'"' (end quote, literal quote, reopen quote).
func shellEscape(s string) string {
	return strings.ReplaceAll(s, "'", "'\\''")
}

// buildSteps generates the Cloud Build steps based on the build options.
// Backend (ImageTag set):
//   - If Dockerfile exists: docker build + push
//   - If no Dockerfile: railpack prepare → buildx build with railpack-frontend
//
// Frontend (DistOutputKey set): npm install + build + upload dist.tar.gz to GCS.
func (cb *CloudBuild) buildSteps(opts BuildOpts) ([]*cloudbuildpb.BuildStep, []string) {
	if opts.ImageTag != "" {
		// Backend build: try Dockerfile first, fall back to Railpack auto-detect.
		// Cloud Build checks for Dockerfile at runtime; if missing, uses railpack.
		return []*cloudbuildpb.BuildStep{
			{
				Name:       "gcr.io/cloud-builders/docker",
				Entrypoint: "bash",
				Args: []string{
					"-c",
					fmt.Sprintf(`cd '%s' && if [ -f Dockerfile ]; then
  echo "[launchkit] Dockerfile found, building directly"
  docker build -t %s . && docker push %s
else
  echo "[launchkit] No Dockerfile, using Railpack auto-detect"
  curl -fsSL "https://github.com/railwayapp/railpack/releases/download/v0.23.0/railpack-v0.23.0-x86_64-unknown-linux-musl.tar.gz" | tar xz -C /usr/local/bin/
  railpack prepare . --plan-out railpack-plan.json
  docker buildx create --name rb --driver docker-container --use
  docker buildx build \
    --build-arg BUILDKIT_SYNTAX="ghcr.io/railwayapp/railpack-frontend" \
    -f railpack-plan.json \
    --push -t %s .
fi`, shellEscape(opts.SourceDir), opts.ImageTag, opts.ImageTag, opts.ImageTag),
				},
			},
		}, []string{opts.ImageTag}
	}

	// Frontend: node build + dist upload
	envExports := ""
	for k, v := range opts.BuildArgs {
		envExports += fmt.Sprintf("export %s='%s' && ", k, shellEscape(v))
	}
	return []*cloudbuildpb.BuildStep{
		{
			Name:       "node:22-slim",
			Entrypoint: "bash",
			Args: []string{
				"-c",
				fmt.Sprintf("%snpm install && npm run build", envExports),
			},
			Dir: opts.SourceDir,
		},
		{
			Name:       "gcr.io/cloud-builders/gsutil",
			Entrypoint: "bash",
			Args: []string{
				"-c",
				fmt.Sprintf("tar czf /tmp/dist.tar.gz -C '%s' . && gsutil cp /tmp/dist.tar.gz gs://%s/%s",
					shellEscape(path.Join(opts.SourceDir, "dist")), opts.SourceBucket, opts.DistOutputKey),
			},
		},
	}, nil
}

func (cb *CloudBuild) Close() error {
	return cb.client.Close()
}
