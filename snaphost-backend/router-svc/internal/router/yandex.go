package router

import (
	"context"
	"fmt"
	"sync"
	"time"

	containerspb "github.com/yandex-cloud/go-genproto/yandex/cloud/serverless/containers/v1"
	ycsdk "github.com/yandex-cloud/go-sdk"

	"snaphost/shared/yandexauth"
)

type YandexResolver struct {
	SDK *ycsdk.SDK
}

func (r *YandexResolver) ResolveURL(ctx context.Context, containerID string) (string, error) {
	c, err := r.SDK.Serverless().Containers().Container().Get(ctx, &containerspb.GetContainerRequest{ContainerId: containerID})
	if err != nil {
		return "", fmt.Errorf("get container: %w", err)
	}
	if c.GetUrl() == "" {
		return "", fmt.Errorf("container %s has empty invocation url", containerID)
	}
	return c.GetUrl(), nil
}

type CachedTokenSource struct {
	SDK *ycsdk.SDK
	TTL time.Duration

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func (s *CachedTokenSource) Token(ctx context.Context) (string, error) {
	now := time.Now()
	s.mu.Lock()
	if s.token != "" && now.Before(s.expiresAt) {
		token := s.token
		s.mu.Unlock()
		return token, nil
	}
	s.mu.Unlock()

	token, err := yandexauth.IAMToken(ctx, s.SDK)
	if err != nil {
		return "", err
	}
	ttl := s.TTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}

	s.mu.Lock()
	s.token = token
	s.expiresAt = now.Add(ttl)
	s.mu.Unlock()
	return token, nil
}
