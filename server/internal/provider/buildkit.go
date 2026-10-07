package provider

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"github.com/alan890104/launchkit/server/internal/tarutil"
	bkclient "github.com/moby/buildkit/client"
	bksession "github.com/moby/buildkit/session"
)

// Compile-time interface guard.
var _ Builder = (*BuildKit)(nil)

// buildKitTCPTimeout is the TCP dial timeout for liveness checks.
// Not operator-tunable — 5s is the right value for a local-network check.
const buildKitTCPTimeout = 5 * time.Second

// BuildKit implements Builder using a persistent BuildKit VM with gVisor isolation.
// Primary build path: connects to remote buildkitd directly via the buildkit Go client.
// The VM runs buildkitd with containerd worker + runsc (gVisor) runtime and CNI network isolation.
type BuildKit struct {
	addr     string // e.g. tcp://10.128.0.x:1234
	project  string
	zone     string
	instance string

	computeClient *compute.InstancesClient

	// fallback is used when the BuildKit VM is unhealthy.
	fallback     Builder
	buildStorage BuildStorage

	startTimeout  time.Duration
	idleTimeout   time.Duration
	checkInterval time.Duration

	mu      sync.Mutex
	vmState string // "running", "stopped", "unknown"
}

// BuildKitConfig holds configuration for the BuildKit provider.
type BuildKitConfig struct {
	Addr         string       // tcp://10.128.0.x:1234
	ProjectID    string       // GCP project
	Zone         string       // GCE zone
	Instance     string       // GCE instance name
	Fallback     Builder      // Cloud Build fallback
	BuildStorage BuildStorage // GCS access for downloading source tarballs

	// Tunable timeouts — zero values use defaults (90s, 30m, 5m).
	StartTimeout  time.Duration
	IdleTimeout   time.Duration
	CheckInterval time.Duration
}

func NewBuildKit(ctx context.Context, cfg BuildKitConfig) (*BuildKit, error) {
	client, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create compute client: %w", err)
	}

	bk := &BuildKit{
		addr:          cfg.Addr,
		project:       cfg.ProjectID,
		zone:          cfg.Zone,
		instance:      cfg.Instance,
		computeClient: client,
		fallback:      cfg.Fallback,
		buildStorage:  cfg.BuildStorage,
		vmState:       "unknown",
		startTimeout:  orDefault(cfg.StartTimeout, 90*time.Second),
		idleTimeout:   orDefault(cfg.IdleTimeout, 30*time.Minute),
		checkInterval: orDefault(cfg.CheckInterval, 5*time.Minute),
	}

	return bk, nil
}

// Build executes a build on the BuildKit VM. Falls back to Cloud Build if VM is unhealthy.
func (bk *BuildKit) Build(ctx context.Context, opts BuildOpts) (*BuildResult, error) {
	if err := bk.ensureRunning(ctx); err != nil {
		slog.Warn("buildkit VM unavailable, falling back to Cloud Build",
			"error", err,
			"image_tag", opts.ImageTag,
		)
		if bk.fallback == nil {
			return nil, fmt.Errorf("buildkit VM unavailable and no fallback configured: %w", err)
		}
		return bk.fallback.Build(ctx, opts)
	}

	result, err := bk.buildWithVM(ctx, opts)
	if err != nil {
		slog.Warn("buildkit build failed, falling back to Cloud Build",
			"error", err,
			"image_tag", opts.ImageTag,
		)
		if bk.fallback == nil {
			return nil, fmt.Errorf("buildkit build failed and no fallback configured: %w", err)
		}
		return bk.fallback.Build(ctx, opts)
	}

	return result, nil
}

// buildWithVM performs the actual build on the remote BuildKit VM using the
// buildkit Go client directly — no Docker daemon required.
func (bk *BuildKit) buildWithVM(ctx context.Context, opts BuildOpts) (*BuildResult, error) {
	// Download source tarball from GCS to local temp dir
	srcDir, cleanup, err := bk.downloadAndExtract(ctx, opts.SourceKey)
	if err != nil {
		return nil, fmt.Errorf("download source: %w", err)
	}
	defer cleanup()

	// Resolve build context: source subdir within the extracted tarball
	buildCtx := srcDir
	if opts.SourceDir != "" {
		buildCtx = srcDir + "/" + opts.SourceDir
	}

	// Connect to buildkitd directly — no docker daemon needed.
	c, err := bkclient.New(ctx, bk.addr)
	if err != nil {
		return nil, fmt.Errorf("connect to buildkitd: %w", err)
	}
	defer c.Close()

	// Compose frontend attrs (build-args + cache prefix).
	frontendAttrs := make(map[string]string)
	for k, v := range opts.BuildArgs {
		frontendAttrs["build-arg:"+k] = v
	}
	if opts.ProjectID != "" {
		frontendAttrs["build-arg:CACHE_PREFIX"] = "proj_" + opts.ProjectID
	}

	// Auto-detect: no Dockerfile → use Railpack gateway frontend.
	var frontend string
	localDirs := map[string]string{"context": buildCtx}

	if _, err := os.Stat(filepath.Join(buildCtx, "Dockerfile")); os.IsNotExist(err) {
		slog.Info("no Dockerfile found, using railpack gateway frontend", "context", buildCtx)
		frontend = "gateway.v0"
		frontendAttrs["source"] = "ghcr.io/railwayapp/railpack-frontend"
	} else {
		frontend = "dockerfile.v0"
		localDirs["dockerfile"] = buildCtx
	}

	solveOpt := bkclient.SolveOpt{
		Frontend:      frontend,
		FrontendAttrs: frontendAttrs,
		LocalDirs:     localDirs,
		Session:       []bksession.Attachable{gcpRegistryAuth(ctx)},
	}

	if opts.ImageTag != "" {
		// Backend: push image to Artifact Registry.
		solveOpt.Exports = []bkclient.ExportEntry{{
			Type: bkclient.ExporterImage,
			Attrs: map[string]string{
				"name": opts.ImageTag,
				"push": "true",
			},
		}}
	} else if opts.DistOutputKey != "" {
		// Frontend: export dist to local dir, then upload to GCS.
		distOut := srcDir + "/__dist_output"
		os.MkdirAll(distOut, 0o755)
		solveOpt.Exports = []bkclient.ExportEntry{{
			Type:      bkclient.ExporterLocal,
			OutputDir: distOut,
		}}
	}

	slog.Info("buildkit build starting",
		"addr", bk.addr,
		"image_tag", opts.ImageTag,
		"context", buildCtx,
		"project_id", opts.ProjectID,
	)

	if _, err := c.Solve(ctx, nil, solveOpt, nil); err != nil {
		return nil, classifyBuildError(err, nil)
	}

	// For frontend builds, upload the dist output to GCS.
	if opts.DistOutputKey != "" && opts.ImageTag == "" {
		distOut := srcDir + "/__dist_output"
		if err := bk.uploadDistToGCS(ctx, distOut, opts.SourceBucket, opts.DistOutputKey); err != nil {
			return nil, fmt.Errorf("upload dist: %w", err)
		}
	}

	slog.Info("buildkit build completed", "image_tag", opts.ImageTag)

	return &BuildResult{
		ImageURI: opts.ImageTag,
	}, nil
}

// downloadAndExtract downloads a source archive from storage and extracts it to a temp dir.
// Supports both tar.gz and zip formats via magic-byte auto-detection.
func (bk *BuildKit) downloadAndExtract(ctx context.Context, sourceKey string) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "lk-buildkit-src-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	r, err := bk.buildStorage.Download(ctx, sourceKey)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("download from storage: %w", err)
	}
	defer r.Close()

	// Save to disk first — zip extraction requires random access (seek).
	archivePath := filepath.Join(tmpDir, "source.archive")
	f, err := os.Create(archivePath)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("create temp archive: %w", err)
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		cleanup()
		return "", nil, fmt.Errorf("write archive: %w", err)
	}
	f.Close()

	srcDir := filepath.Join(tmpDir, "src")
	if err := tarutil.Extract(archivePath, srcDir); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("extract source: %w", err)
	}

	return srcDir, cleanup, nil
}

// uploadDistToGCS creates a tar.gz of the dist output and uploads it to GCS.
func (bk *BuildKit) uploadDistToGCS(ctx context.Context, distDir, bucket, key string) error {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	err := filepath.Walk(distDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(distDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.IsDir() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("create dist tarball: %w", err)
	}
	tw.Close()
	gw.Close()

	return bk.buildStorage.Upload(ctx, key, &buf)
}

// ensureRunning ensures the BuildKit VM is running and TCP :1234 is reachable.
// Idempotent: already-running VMs return immediately.
func (bk *BuildKit) ensureRunning(ctx context.Context) error {
	if bk.isTCPReady(ctx) {
		bk.mu.Lock()
		bk.vmState = "running"
		bk.mu.Unlock()
		return nil
	}

	// VM might be stopped — try to start it
	slog.Info("starting BuildKit VM", "instance", bk.instance)

	op, err := bk.computeClient.Start(ctx, &computepb.StartInstanceRequest{
		Project:  bk.project,
		Zone:     bk.zone,
		Instance: bk.instance,
	})
	if err != nil {
		// Already running or transitioning — not necessarily an error
		if !strings.Contains(err.Error(), "already") {
			return fmt.Errorf("start VM: %w", err)
		}
	} else {
		if err := op.Wait(ctx); err != nil {
			return fmt.Errorf("wait for VM start: %w", err)
		}
	}

	// Wait for buildkitd TCP :1234 to become ready, respecting context cancellation.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	deadline := time.NewTimer(bk.startTimeout)
	defer deadline.Stop()

	for {
		if bk.isTCPReady(ctx) {
			bk.mu.Lock()
			bk.vmState = "running"
			bk.mu.Unlock()
			slog.Info("BuildKit VM ready", "instance", bk.instance)
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for BuildKit VM: %w", ctx.Err())
		case <-deadline.C:
			return fmt.Errorf("BuildKit VM start timeout after %s", bk.startTimeout)
		case <-ticker.C:
			// retry TCP check
		}
	}
}

// isTCPReady checks if buildkitd is reachable on TCP :1234.
func (bk *BuildKit) isTCPReady(ctx context.Context) bool {
	// Extract host:port from addr (e.g. tcp://10.128.0.x:1234 → 10.128.0.x:1234)
	hostPort := strings.TrimPrefix(bk.addr, "tcp://")
	conn, err := net.DialTimeout("tcp", hostPort, buildKitTCPTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// IsHealthy returns whether the BuildKit VM is currently reachable.
func (bk *BuildKit) IsHealthy(ctx context.Context) bool {
	return bk.isTCPReady(ctx)
}

// StopIfIdle stops the VM if no builds have run recently.
// Called by the idle check periodic job.
func (bk *BuildKit) StopIfIdle(ctx context.Context, lastBuildFinished time.Time) error {
	bk.mu.Lock()
	state := bk.vmState
	bk.mu.Unlock()

	if state != "running" {
		return nil
	}

	idle := time.Since(lastBuildFinished)
	if idle < bk.idleTimeout {
		return nil
	}

	slog.Info("BuildKit VM idle, stopping",
		"idle_minutes", int(idle.Minutes()),
		"threshold_minutes", int(bk.idleTimeout.Minutes()),
	)

	op, err := bk.computeClient.Stop(ctx, &computepb.StopInstanceRequest{
		Project:  bk.project,
		Zone:     bk.zone,
		Instance: bk.instance,
	})
	if err != nil {
		return fmt.Errorf("stop VM: %w", err)
	}
	if err := op.Wait(ctx); err != nil {
		return fmt.Errorf("wait for VM stop: %w", err)
	}

	bk.mu.Lock()
	bk.vmState = "stopped"
	bk.mu.Unlock()

	return nil
}

func (bk *BuildKit) Close() error {
	bk.computeClient.Close()
	return nil
}

// classifyBuildError inspects the error and build output to produce a human-readable error.
// Exit code 137 = OOM kill (SIGKILL from kernel OOM killer).
// Exit code 1 with "no space left" = disk full.
func classifyBuildError(err error, output []byte) error {
	out := strings.ToLower(string(output))

	// OOM: kernel killed the build process (exit 137 = 128 + SIGKILL).
	if strings.Contains(err.Error(), "exit status 137") ||
		strings.Contains(out, "out of memory") ||
		strings.Contains(out, "oom") ||
		strings.Contains(out, "killed") ||
		strings.Contains(err.Error(), "oom") {
		return fmt.Errorf("build killed by OOM — the build ran out of memory.\n"+
			"Try: reduce dependencies, use multi-stage Dockerfile, or increase builder VM memory.\n"+
			"Error: %w", err)
	}

	// Disk full
	if strings.Contains(out, "no space left") || strings.Contains(out, "disk quota exceeded") ||
		strings.Contains(err.Error(), "no space left") {
		return fmt.Errorf("build failed — builder disk full.\nError: %w", err)
	}

	return fmt.Errorf("buildkit: %w", err)
}
