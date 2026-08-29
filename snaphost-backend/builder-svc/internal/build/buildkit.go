// Package build provides a BuildKit gRPC client for building and pushing
// container images without using the Docker daemon or CLI.
package build

import (
	"context"
	"fmt"
	"time"

	"github.com/docker/cli/cli/config"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"go.uber.org/zap"

	bcfg "snaphost/builder-svc/config"
	"snaphost/builder-svc/internal/logs"
	"snaphost/shared/yandexauth"
)

// Builder wraps a BuildKit client for building and pushing container images.
type Builder struct {
	bkClient     *client.Client
	publisher    logs.Publisher
	registryAuth []session.Attachable
	log          *zap.Logger
}

// NewBuilder creates a new Builder connected to the BuildKit daemon at the
// given host address. The registry credential source is selected by
// cfg.RegistryAuthMode: "static" reads the docker config file; "yandex_iam"
// signs JWTs against a Yandex authorized key and uses the resulting IAM
// token as the registry password.
func NewBuilder(buildkitHost string, dockerConfigDir string, cfg *bcfg.Config, publisher logs.Publisher, log *zap.Logger) (*Builder, error) {
	bkClient, err := client.New(context.Background(), buildkitHost)
	if err != nil {
		return nil, fmt.Errorf("connect to buildkit at %s: %w", buildkitHost, err)
	}

	var attachables []session.Attachable
	switch cfg.RegistryAuthMode {
	case "static", "":
		if dockerConfigDir != "" {
			dockerCfg, err := config.Load(dockerConfigDir)
			if err != nil {
				return nil, fmt.Errorf("load docker config from %s: %w", dockerConfigDir, err)
			}
			ap := authprovider.NewDockerAuthProvider(authprovider.DockerAuthProviderConfig{
				AuthConfigProvider: authprovider.LoadAuthConfig(dockerCfg),
			})
			attachables = append(attachables, ap)
		}
	case "yandex_iam":
		sdk, err := yandexauth.NewSDK(context.Background(), cfg.YandexSAKeyPath)
		if err != nil {
			return nil, fmt.Errorf("yandex iam auth: build sdk: %w", err)
		}
		yi, err := newYandexIAMAuth(sdk, cfg.RegistryURL)
		if err != nil {
			return nil, fmt.Errorf("yandex iam auth: %w", err)
		}
		attachables = append(attachables, yi)
	default:
		return nil, fmt.Errorf("unknown REGISTRY_AUTH_MODE: %s", cfg.RegistryAuthMode)
	}

	return &Builder{
		bkClient:     bkClient,
		publisher:    publisher,
		registryAuth: attachables,
		log:          log,
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
	// ImageName is the full image reference to tag and push (e.g. registry:5000/proj-abc:deploy123).
	ImageName string
	// BuildArgs is a map of build arguments passed to the Dockerfile.
	BuildArgs map[string]string
	// Timeout is the maximum duration for the build + push.
	Timeout time.Duration
}

// BuildResult holds the outcome of a successful build.
type BuildResult struct {
	// ImageRef is the full pushed image reference.
	ImageRef string
	// Duration is how long the build took.
	Duration time.Duration
}

// Build executes a container image build via BuildKit and pushes the image
// to the configured registry. Build progress is streamed to the log publisher.
func (b *Builder) Build(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
	buildCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	start := time.Now()

	_ = b.publisher.Publish(opts.DeployID, logs.LogLine{
		Stage:     "build",
		Text:      fmt.Sprintf("pushing image to registry: image=%s dockerfile=%s", opts.ImageName, opts.DockerfileName),
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

	exportEntry := client.ExportEntry{
		Type: client.ExporterImage,
		Attrs: map[string]string{
			"name": opts.ImageName,
			"push": "true",
		},
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
		Session:       b.registryAuth,
	}, statusCh)

	// Wait for the progress goroutine to finish.
	<-done

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
