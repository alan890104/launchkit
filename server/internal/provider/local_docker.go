package provider

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Compile-time interface guard.
var _ Builder = (*LocalDocker)(nil)

// LocalDocker builds container images using the Docker daemon running on the
// same machine as the LaunchKit MCP server. This is the open-source tier builder:
// no remote VM, no cloud build service — just the local Docker socket.
//
// Cross-compilation to linux/amd64 is handled by docker buildx (QEMU emulation
// on Apple Silicon or native on x86 Linux).
//
// Prerequisite: Docker (with buildx) must be installed and running.
// Optional: railpack installed for auto-Dockerfile generation when no Dockerfile exists.
type LocalDocker struct {
	socketAddr string // e.g. "unix:///var/run/docker.sock"
}

// NewLocalDocker creates a LocalDocker builder.
// socketAddr defaults to "unix:///var/run/docker.sock" if empty.
func NewLocalDocker(socketAddr string) *LocalDocker {
	if socketAddr == "" {
		socketAddr = "unix:///var/run/docker.sock"
	}
	return &LocalDocker{socketAddr: socketAddr}
}

// Build builds a Docker image from local source and pushes it to the registry.
// For backend services: builds image, pushes to registry, returns ImageURI.
// For frontend services: runs the build command, returns LocalDistPath.
func (b *LocalDocker) Build(ctx context.Context, opts BuildOpts) (*BuildResult, error) {
	if opts.LocalSourcePath == "" {
		return nil, fmt.Errorf("LocalDocker requires LocalSourcePath (local build mode only)")
	}

	// For frontend: no image needed, just run the build and return the dist dir.
	if opts.ImageTag == "" {
		return b.buildFrontend(ctx, opts)
	}

	return b.buildBackend(ctx, opts)
}

// buildBackend builds a Docker image and pushes it to the registry.
func (b *LocalDocker) buildBackend(ctx context.Context, opts BuildOpts) (*BuildResult, error) {
	sourceDir := opts.LocalSourcePath // LocalSourcePath already includes SourceDir (joined by orchestrator)

	slog.Info("local docker build starting",
		"source_dir", sourceDir,
		"image_tag", opts.ImageTag,
	)

	// Ensure Dockerfile exists; if not, try railpack to generate one.
	dockerfilePath := filepath.Join(sourceDir, "Dockerfile")
	if _, err := os.Stat(dockerfilePath); os.IsNotExist(err) {
		slog.Info("no Dockerfile found, attempting railpack generate", "source_dir", sourceDir)
		if err := b.runRailpack(ctx, sourceDir); err != nil {
			return nil, fmt.Errorf("no Dockerfile found and railpack failed: %w\n"+
				"Solution: add a Dockerfile to %s or install railpack (https://railpack.io)", err, sourceDir)
		}
		slog.Info("railpack generated Dockerfile", "source_dir", sourceDir)
	}

	// Build the image with cross-compilation to linux/amd64.
	buildArgs := b.buildArgFlags(opts.BuildArgs)
	buildCmd := append([]string{
		"buildx", "build",
		"--platform", "linux/amd64",
		"--push", // push directly during build (requires buildx with registry access)
		"-t", opts.ImageTag,
	}, buildArgs...)
	buildCmd = append(buildCmd, sourceDir)

	slog.Info("running docker buildx build", "image", opts.ImageTag)
	if err := b.run(ctx, "docker", buildCmd...); err != nil {
		// Fallback: try without buildx (plain docker build + separate push)
		slog.Warn("docker buildx build failed, falling back to docker build", "error", err)
		if err2 := b.buildAndPushFallback(ctx, opts.ImageTag, sourceDir, buildArgs); err2 != nil {
			return nil, fmt.Errorf("docker build failed: %w (buildx error: %v)", err2, err)
		}
	}

	slog.Info("local docker build complete", "image", opts.ImageTag)
	return &BuildResult{
		ImageURI: opts.ImageTag,
		LogsURL:  "", // logs printed to server stdout
	}, nil
}

// buildAndPushFallback uses plain docker build + docker push when buildx is unavailable.
func (b *LocalDocker) buildAndPushFallback(ctx context.Context, imageTag, sourceDir string, buildArgs []string) error {
	buildCmd := append([]string{"build", "--platform", "linux/amd64", "-t", imageTag}, buildArgs...)
	buildCmd = append(buildCmd, sourceDir)
	if err := b.run(ctx, "docker", buildCmd...); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	if err := b.run(ctx, "docker", "push", imageTag); err != nil {
		return fmt.Errorf("docker push: %w", err)
	}
	return nil
}

// buildFrontend runs the frontend build command and tars the output dist directory.
func (b *LocalDocker) buildFrontend(ctx context.Context, opts BuildOpts) (*BuildResult, error) {
	sourceDir := opts.LocalSourcePath

	slog.Info("local frontend build starting", "source_dir", sourceDir)

	// Determine the build command (default: npm run build).
	buildCmd := "npm run build"
	if opts.BuildArgs != nil {
		if cmd, ok := opts.BuildArgs["BUILD_CMD"]; ok && cmd != "" {
			buildCmd = cmd
		}
	}

	// Run the build command with BuildArgs as env vars.
	env := os.Environ()
	for k, v := range opts.BuildArgs {
		env = append(env, k+"="+v)
	}

	parts := strings.Fields(buildCmd)
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...) //nolint:gosec
	cmd.Dir = sourceDir
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("frontend build command %q failed: %w", buildCmd, err)
	}

	// Find the dist directory (common names: dist, build, .next/static, out).
	distDir := b.findDistDir(sourceDir)
	if distDir == "" {
		return nil, fmt.Errorf("frontend build completed but no dist directory found in %s"+
			" (checked: dist, build, out, public/build)", sourceDir)
	}

	slog.Info("local frontend build complete", "dist_dir", distDir)
	return &BuildResult{
		LocalDistPath: distDir,
	}, nil
}

// findDistDir locates the output directory after a frontend build.
func (b *LocalDocker) findDistDir(sourceDir string) string {
	candidates := []string{"dist", "build", "out", ".next", "public/build"}
	for _, name := range candidates {
		full := filepath.Join(sourceDir, name)
		if info, err := os.Stat(full); err == nil && info.IsDir() {
			return full
		}
	}
	return ""
}

// runRailpack invokes railpack to auto-generate a Dockerfile.
func (b *LocalDocker) runRailpack(ctx context.Context, sourceDir string) error {
	return b.run(ctx, "railpack", "build", "--generate-dockerfile", sourceDir)
}

// buildArgFlags converts a map to --build-arg flags for docker build.
func (b *LocalDocker) buildArgFlags(args map[string]string) []string {
	var flags []string
	for k, v := range args {
		flags = append(flags, "--build-arg", k+"="+v)
	}
	return flags
}

// run executes a command, streaming its output to the server logger.
func (b *LocalDocker) run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// CreateDistTar creates a tar.gz of a directory (utility for debugging / cloud upload).
func CreateDistTar(sourceDir string, w io.Writer) error {
	gw := gzip.NewWriter(w)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		hdr := &tar.Header{
			Name:    rel,
			Size:    info.Size(),
			Mode:    int64(info.Mode()),
			ModTime: info.ModTime(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(path) //nolint:gosec
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}
