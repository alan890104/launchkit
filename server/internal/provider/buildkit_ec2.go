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

	"github.com/alan890104/launchkit/server/internal/tarutil"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	bkclient "github.com/moby/buildkit/client"
	bksession "github.com/moby/buildkit/session"
)

// Compile-time interface guard.
var _ Builder = (*BuildKitEC2)(nil)

// ec2TCPTimeout is the TCP dial timeout for liveness checks.
// Not operator-tunable — 5s is the right value for a local-network check.
const ec2TCPTimeout = 5 * time.Second

// BuildKitEC2Config holds configuration for the EC2-hosted BuildKit provider.
type BuildKitEC2Config struct {
	Addr         string // e.g. tcp://10.0.1.x:1234
	InstanceID   string
	Region       string
	Fallback     Builder
	BuildStorage BuildStorage

	// Tunable timeouts — zero values use defaults (90s, 30m).
	StartTimeout time.Duration
	IdleTimeout  time.Duration
}

// BuildKitEC2 implements Builder using a persistent BuildKit VM on EC2.
type BuildKitEC2 struct {
	addr          string
	instanceID    string
	region        string
	ec2Client     *ec2.Client
	fallback      Builder
	buildStorage  BuildStorage
	startTimeout  time.Duration
	idleTimeout   time.Duration
	mu            sync.Mutex
	vmState       string // "running", "stopped", "unknown"
	lastBuildTime time.Time
}

func NewBuildKitEC2(ctx context.Context, cfg BuildKitEC2Config) (*BuildKitEC2, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	bk := &BuildKitEC2{
		addr:         cfg.Addr,
		instanceID:   cfg.InstanceID,
		region:       cfg.Region,
		ec2Client:    ec2.NewFromConfig(awsCfg),
		fallback:     cfg.Fallback,
		buildStorage: cfg.BuildStorage,
		vmState:      "unknown",
		startTimeout: orDefault(cfg.StartTimeout, 90*time.Second),
		idleTimeout:  orDefault(cfg.IdleTimeout, 30*time.Minute),
	}

	// Check if VM is healthy on startup
	if bk.isTCPReady() {
		bk.vmState = "running"
		slog.Info("EC2 BuildKit VM is healthy", "addr", bk.addr)
	}
	return bk, nil
}

func (bk *BuildKitEC2) Build(ctx context.Context, opts BuildOpts) (*BuildResult, error) {
	if err := bk.ensureRunning(ctx); err != nil {
		slog.Warn("EC2 BuildKit VM not available, falling back", "error", err)
		if bk.fallback != nil {
			return bk.fallback.Build(ctx, opts)
		}
		return nil, fmt.Errorf("BuildKit VM not available and no fallback configured: %w", err)
	}

	result, err := bk.buildWithVM(ctx, opts)
	if err != nil && bk.fallback != nil {
		slog.Warn("BuildKit build failed, falling back", "error", err)
		return bk.fallback.Build(ctx, opts)
	}
	return result, err
}

// buildWithVM performs the actual build on the remote BuildKit VM using the
// buildkit Go client directly — no Docker daemon required.
func (bk *BuildKitEC2) buildWithVM(ctx context.Context, opts BuildOpts) (*BuildResult, error) {
	bk.mu.Lock()
	bk.lastBuildTime = time.Now()
	bk.mu.Unlock()

	// Download source tarball from S3
	tmpDir, err := os.MkdirTemp("", "lk-build-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tarPath := filepath.Join(tmpDir, "source.tar.gz")
	rc, err := bk.buildStorage.Download(ctx, opts.SourceKey)
	if err != nil {
		return nil, fmt.Errorf("download source: %w", err)
	}
	f, err := os.Create(tarPath)
	if err != nil {
		rc.Close()
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		rc.Close()
		return nil, fmt.Errorf("write source tarball: %w", err)
	}
	f.Close()
	rc.Close()

	// Extract (auto-detects tar.gz or zip by magic bytes)
	srcDir := filepath.Join(tmpDir, "src")
	if err := tarutil.Extract(tarPath, srcDir); err != nil {
		return nil, fmt.Errorf("extract source: %w", err)
	}

	buildContext := srcDir
	if opts.SourceDir != "" {
		buildContext = filepath.Join(srcDir, opts.SourceDir)
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
		frontendAttrs["build-arg:CACHE_PREFIX"] = opts.ProjectID
	}

	// Auto-detect: no Dockerfile → use Railpack gateway frontend.
	var frontend string
	localDirs := map[string]string{"context": buildContext}

	if _, err := os.Stat(filepath.Join(buildContext, "Dockerfile")); os.IsNotExist(err) {
		frontend = "gateway.v0"
		frontendAttrs["source"] = "ghcr.io/railwayapp/railpack-frontend"
	} else {
		frontend = "dockerfile.v0"
		localDirs["dockerfile"] = buildContext
	}

	solveOpt := bkclient.SolveOpt{
		Frontend:      frontend,
		FrontendAttrs: frontendAttrs,
		LocalDirs:     localDirs,
		Session:       []bksession.Attachable{ecrRegistryAuth(bk.region)},
	}

	if opts.ImageTag != "" {
		// Backend build: push image to ECR.
		solveOpt.Exports = []bkclient.ExportEntry{{
			Type: bkclient.ExporterImage,
			Attrs: map[string]string{
				"name": opts.ImageTag,
				"push": "true",
			},
		}}
	} else {
		// Frontend build: export dist to local dir, then upload to S3.
		distOutput := filepath.Join(tmpDir, "__dist_output")
		os.MkdirAll(distOutput, 0o755)
		solveOpt.Exports = []bkclient.ExportEntry{{
			Type:      bkclient.ExporterLocal,
			OutputDir: distOutput,
		}}
	}

	if _, err := c.Solve(ctx, nil, solveOpt, nil); err != nil {
		return nil, classifyBuildError(err, nil)
	}

	// For frontend builds, tar the dist output and upload to S3.
	if opts.DistOutputKey != "" && opts.ImageTag == "" {
		distOutput := filepath.Join(tmpDir, "__dist_output")
		if err := bk.uploadDistToStorage(ctx, distOutput, opts.DistOutputKey); err != nil {
			return nil, fmt.Errorf("upload dist: %w", err)
		}
	}

	return &BuildResult{ImageURI: opts.ImageTag}, nil
}

func (bk *BuildKitEC2) ensureRunning(ctx context.Context) error {
	if bk.isTCPReady() {
		bk.mu.Lock()
		bk.vmState = "running"
		bk.mu.Unlock()
		return nil
	}

	slog.Info("starting EC2 BuildKit VM", "instance", bk.instanceID)
	_, err := bk.ec2Client.StartInstances(ctx, &ec2.StartInstancesInput{
		InstanceIds: []string{bk.instanceID},
	})
	if err != nil {
		// Instance might already be running
		if !strings.Contains(err.Error(), "IncorrectInstanceState") {
			return fmt.Errorf("start EC2 instance: %w", err)
		}
	}

	// Wait for TCP readiness
	deadline := time.After(bk.startTimeout)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return fmt.Errorf("EC2 BuildKit VM start timeout after %s", bk.startTimeout)
		case <-ticker.C:
			if bk.isTCPReady() {
				bk.mu.Lock()
				bk.vmState = "running"
				bk.mu.Unlock()
				slog.Info("EC2 BuildKit VM ready", "instance", bk.instanceID)
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (bk *BuildKitEC2) isTCPReady() bool {
	hostPort := strings.TrimPrefix(bk.addr, "tcp://")
	conn, err := net.DialTimeout("tcp", hostPort, ec2TCPTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// IsHealthy returns true if the BuildKit daemon is reachable.
func (bk *BuildKitEC2) IsHealthy() bool {
	return bk.isTCPReady()
}

// StopIfIdle stops the EC2 instance if no builds have run recently.
func (bk *BuildKitEC2) StopIfIdle(ctx context.Context) error {
	bk.mu.Lock()
	idle := time.Since(bk.lastBuildTime) > bk.idleTimeout
	state := bk.vmState
	bk.mu.Unlock()

	if !idle || state != "running" {
		return nil
	}

	slog.Info("stopping idle EC2 BuildKit VM", "instance", bk.instanceID)
	_, err := bk.ec2Client.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{bk.instanceID},
	})
	if err != nil {
		return fmt.Errorf("stop EC2 instance: %w", err)
	}

	bk.mu.Lock()
	bk.vmState = "stopped"
	bk.mu.Unlock()
	return nil
}

func (bk *BuildKitEC2) uploadDistToStorage(ctx context.Context, distDir, objectKey string) error {
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

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return fmt.Errorf("tar dist: %w", err)
	}

	if err := tw.Close(); err != nil {
		return err
	}
	if err := gw.Close(); err != nil {
		return err
	}

	return bk.buildStorage.Upload(ctx, objectKey, &buf)
}
