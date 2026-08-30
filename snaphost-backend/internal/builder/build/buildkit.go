// Package build builds container images with BuildKit and hands the result to
// the local Docker daemon.
//
// It used to push to a registry, and on a single host that was two network hops
// and a daemon to move an image between two processes that share a filesystem.
// The registry existed to reach a cloud runtime that no longer exists.
//
// Locally it could not work at all, which is how this was found: the builder
// pushed to a Compose service name resolvable only inside the Docker network,
// and the pull was performed by the host daemon, which is not on that network.
// No value of REGISTRY_URL satisfies both, because the two clients resolve
// names in different places.
package build

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/moby/buildkit/client"
	"go.uber.org/zap"

	"snaphost/internal/builder/logs"
)

// ImageLoader takes a Docker image tarball into the local daemon's image store.
//
// An interface because internal/runtime/backend/docker declares itself the only
// package that may import the Docker SDK, and that invariant is worth more than
// the convenience of a second client here.
type ImageLoader interface {
	LoadImage(ctx context.Context, r io.Reader) error
}

// Builder wraps a BuildKit client for building container images.
type Builder struct {
	bkClient  *client.Client
	publisher logs.Publisher
	loader    ImageLoader
	log       *zap.Logger
}

// NewBuilder connects to the BuildKit daemon at the given host address.
//
// The Docker config directory and the registry auth mode went with the push:
// there is no registry to authenticate to, and credentials for one were the
// only thing that configuration selected.
func NewBuilder(buildkitHost string, loader ImageLoader, publisher logs.Publisher, log *zap.Logger) (*Builder, error) {
	bkClient, err := client.New(context.Background(), buildkitHost)
	if err != nil {
		return nil, fmt.Errorf("connect to buildkit at %s: %w", buildkitHost, err)
	}
	if loader == nil {
		return nil, fmt.Errorf("build: an image loader is required")
	}

	return &Builder{
		bkClient:  bkClient,
		publisher: publisher,
		loader:    loader,
		log:       log,
	}, nil
}

// BuildOptions contains all parameters for a container image build.
type BuildOptions struct {
	// DeployID is used for log publishing.
	DeployID string
	// ContextDir is the build context directory.
	ContextDir string
	// DockerfileName is the Dockerfile filename within ContextDir.
	DockerfileName string
	// ImageName is the reference the built image is tagged with in the local
	// daemon (e.g. snaphost/proj-abc:deploy123).
	ImageName string
	// BuildArgs is a map of build arguments passed to the Dockerfile.
	BuildArgs map[string]string
	// Timeout is the maximum duration for the build and the load that follows.
	Timeout time.Duration
}

// BuildResult holds the outcome of a successful build.
type BuildResult struct {
	// ImageRef is the reference the image was tagged with.
	ImageRef string
	// Duration is how long the build took.
	Duration time.Duration
}

// Build executes a container image build via BuildKit and loads the result
// into the local Docker daemon. Progress is streamed to the log publisher.
func (b *Builder) Build(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
	buildCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	start := time.Now()

	_ = b.publisher.Publish(opts.DeployID, logs.LogLine{
		Stage:     "build",
		Text:      fmt.Sprintf("building image: image=%s dockerfile=%s", opts.ImageName, opts.DockerfileName),
		Level:     "info",
		Timestamp: time.Now().UTC(),
	})

	// Status channel for build progress.
	statusCh := make(chan *client.SolveStatus)

	// Goroutine to stream build progress to the log publisher.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for status := range statusCh {
			for _, v := range status.Vertexes {
				if v.Started != nil && v.Completed == nil {
					_ = b.publisher.Publish(opts.DeployID, logs.LogLine{
						Stage:     "build",
						Text:      fmt.Sprintf("[started] %s", v.Name),
						Level:     "info",
						Timestamp: time.Now().UTC(),
					})
				}
				if v.Completed != nil {
					_ = b.publisher.Publish(opts.DeployID, logs.LogLine{
						Stage:     "build",
						Text:      fmt.Sprintf("[completed] %s", v.Name),
						Level:     "info",
						Timestamp: time.Now().UTC(),
					})
				}
				if v.Error != "" {
					_ = b.publisher.Publish(opts.DeployID, logs.LogLine{
						Stage:     "build",
						Text:      fmt.Sprintf("[error] %s: %s", v.Name, v.Error),
						Level:     "error",
						Timestamp: time.Now().UTC(),
					})
				}
			}
			for _, l := range status.Logs {
				_ = b.publisher.Publish(opts.DeployID, logs.LogLine{
					Stage:     "build",
					Text:      string(l.Data),
					Level:     "info",
					Timestamp: l.Timestamp,
				})
			}
		}
	}()

	// Prepare frontend attrs.
	frontendAttrs := map[string]string{
		"filename": opts.DockerfileName,
	}
	for k, v := range opts.BuildArgs {
		frontendAttrs["build-arg:"+k] = v
	}

	// BuildKit writes a Docker image tarball to this pipe and the daemon reads
	// it from the other end. Neither side buffers the image: a Node project's
	// layers are hundreds of megabytes, and holding them in this process would
	// undo the reason uploads stopped being held in it.
	pr, pw := io.Pipe()

	loadErr := make(chan error, 1)
	go func() {
		// Any error here also has to close the read end, or Solve blocks
		// forever writing into a pipe nobody is draining.
		err := b.loader.LoadImage(buildCtx, pr)
		if err != nil {
			pr.CloseWithError(err)
		} else {
			pr.Close()
		}
		loadErr <- err
	}()

	exportEntry := client.ExportEntry{
		Type: client.ExporterDocker,
		Attrs: map[string]string{
			"name": opts.ImageName,
		},
		Output: func(map[string]string) (io.WriteCloser, error) { return pw, nil },
	}

	// Execute the build via BuildKit.
	_, err := b.bkClient.Solve(buildCtx, nil, client.SolveOpt{
		Exports: []client.ExportEntry{exportEntry},
		LocalDirs: map[string]string{
			"context":    opts.ContextDir,
			"dockerfile": opts.ContextDir,
		},
		Frontend:      "dockerfile.v0",
		FrontendAttrs: frontendAttrs,
	}, statusCh)

	// Closing the write end is what tells the daemon the tarball is complete.
	// On a failed build it is what stops the loader waiting for one.
	if err != nil {
		pw.CloseWithError(err)
	} else {
		pw.Close()
	}

	// Wait for the progress goroutine to finish.
	<-done

	// A build that succeeded and a load that failed is still a failure: there
	// is no image for the runtime to start.
	if loadFailed := <-loadErr; err == nil && loadFailed != nil {
		err = loadFailed
	}

	if err != nil {
		return nil, fmt.Errorf("buildkit solve: %w", err)
	}

	duration := time.Since(start)

	_ = b.publisher.Publish(opts.DeployID, logs.LogLine{
		Stage:     "build",
		Text:      fmt.Sprintf("build complete in %s", duration.Round(time.Second)),
		Level:     "info",
		Timestamp: time.Now().UTC(),
	})

	return &BuildResult{
		ImageRef: opts.ImageName,
		Duration: duration,
	}, nil
}

// Close releases the BuildKit client connection.
func (b *Builder) Close() error {
	return b.bkClient.Close()
}
