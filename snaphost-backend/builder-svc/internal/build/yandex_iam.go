package build

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/docker/cli/cli/config/types"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	ycsdk "github.com/yandex-cloud/go-sdk"

	"snaphost/shared/yandexauth"
)

// newYandexIAMAuth uses BuildKit's standard registry auth provider so public
// registries retain their anonymous OAuth/token-authority flow. IAM credentials
// are returned only for the exact host configured by REGISTRY_URL.
func newYandexIAMAuth(sdk *ycsdk.SDK, registryURL string) (session.Attachable, error) {
	if sdk == nil {
		return nil, errors.New("yandex iam auth: sdk is nil")
	}

	host, err := registryHost(registryURL)
	if err != nil {
		return nil, err
	}

	return newYandexIAMAuthProvider(host, func(ctx context.Context) (string, error) {
		return yandexauth.IAMToken(ctx, sdk)
	}), nil
}

func newYandexIAMAuthProvider(registryHost string, token func(context.Context) (string, error)) session.Attachable {
	return authprovider.NewDockerAuthProvider(authprovider.DockerAuthProviderConfig{
		AuthConfigProvider: func(ctx context.Context, host string, _ []string, _ authprovider.ExpireCachedAuthCheck) (types.AuthConfig, error) {
			if host != registryHost {
				return types.AuthConfig{}, nil
			}

			iamToken, err := token(ctx)
			if err != nil {
				return types.AuthConfig{}, err
			}
			return types.AuthConfig{Username: "iam", Password: iamToken, ServerAddress: registryHost}, nil
		},
	})
}

func registryHost(registryURL string) (string, error) {
	value := strings.TrimSpace(registryURL)
	if value == "" {
		return "", errors.New("yandex iam auth: registry URL is empty")
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return "", errors.New("yandex iam auth: invalid registry URL")
	}
	return parsed.Host, nil
}
