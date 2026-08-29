//go:build yandex
// +build yandex

package yandex

import (
	"context"
	"fmt"
	"net/http"
	"time"

	containerspb "github.com/yandex-cloud/go-genproto/yandex/cloud/serverless/containers/v1"

	"snaphost/runner-svc/internal/backend"
	"snaphost/shared/yandexauth"
)

// probeInterval paces retries while the container cold-starts. A first
// invocation of a scale-to-zero container routinely takes several seconds and
// can return 502/503 on the way up, so the probe must be patient rather than
// decisive on the first answer.
const probeInterval = time.Second

// Probe invokes the container on its own signed URL and reports whether
// anything answered. Serverless Containers run the image with PORT set and
// forward requests there; if the application bound a different port, Yandex
// answers with UserCodeError instead of the app's response — which is exactly
// the failure this exists to catch before the deploy is called running.
func (b *YandexBackend) Probe(ctx context.Context, req backend.ProbeRequest) error {
	container, err := b.sdk.Serverless().Containers().Container().Get(ctx, &containerspb.GetContainerRequest{
		ContainerId: req.ContainerID,
	})
	if err != nil {
		return fmt.Errorf("get container for probe: %w", err)
	}
	if container.GetUrl() == "" {
		// Nothing to invoke; do not fail a deploy over the probe's blind spot.
		b.publish(req.DeployID, "probe skipped: container has no invocation URL", "warn")
		return nil
	}

	client := &http.Client{Timeout: 10 * time.Second}
	ticker := time.NewTicker(probeInterval)
	defer ticker.Stop()

	var lastReason string
	for {
		lastReason = b.probeOnce(ctx, client, container.GetUrl())
		if lastReason == "" {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("%w on port %d: %s", backend.ErrProbeFailed, req.Port, lastReason)
		case <-ticker.C:
		}
	}
}

// probeOnce returns an empty string when the container answered, or a short
// reason to retry on.
func (b *YandexBackend) probeOnce(ctx context.Context, client *http.Client, url string) string {
	token, err := yandexauth.IAMToken(ctx, b.sdk)
	if err != nil {
		return "iam token: " + err.Error()
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "build request: " + err.Error()
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(httpReq)
	if err != nil {
		return err.Error()
	}
	defer resp.Body.Close()

	// The gateway reports a container that failed to serve as 502/503 — during
	// a cold start that is transient, which is why this is a retry rather than
	// a verdict. Any other status means the user's server answered, including
	// its own 500s: judging application responses is not the runtime's job.
	if resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable {
		return fmt.Sprintf("container returned %d", resp.StatusCode)
	}
	return ""
}
