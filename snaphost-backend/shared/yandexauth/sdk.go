// Package yandexauth provides Yandex Cloud authentication helpers
// shared between services that need to talk to Yandex APIs.
//
// The SDK manages IAM token lifecycle internally for its own gRPC calls
// (signs JWT with the authorized key, exchanges for a 12h IAM token,
// refreshes before expiry). Callers should construct one *ycsdk.SDK at
// startup and pass it down — do not create per-request.
package yandexauth

import (
	"context"
	"fmt"

	ycsdk "github.com/yandex-cloud/go-sdk"
	"github.com/yandex-cloud/go-sdk/iamkey"
)

// NewSDK builds a Yandex Cloud SDK authenticated with a service account
// authorized key from the given JSON file path. The returned SDK is lazy:
// no network call is made until the caller invokes an SDK method.
func NewSDK(ctx context.Context, keyPath string) (*ycsdk.SDK, error) {
	key, err := iamkey.ReadFromJSONFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("yandexauth: read SA key %q: %w", keyPath, err)
	}
	creds, err := ycsdk.ServiceAccountKey(key)
	if err != nil {
		return nil, fmt.Errorf("yandexauth: build credentials: %w", err)
	}
	sdk, err := ycsdk.Build(ctx, ycsdk.Config{Credentials: creds})
	if err != nil {
		return nil, fmt.Errorf("yandexauth: build sdk: %w", err)
	}
	return sdk, nil
}

// NewSDKFromMetadata builds a Yandex Cloud SDK authenticated with the service
// account attached to the current Yandex runtime. This is intended for managed
// runtimes such as Serverless Containers where mounting authorized-key files is
// unnecessary and would broaden secret handling.
func NewSDKFromMetadata(ctx context.Context) (*ycsdk.SDK, error) {
	sdk, err := ycsdk.Build(ctx, ycsdk.Config{Credentials: ycsdk.InstanceServiceAccount()})
	if err != nil {
		return nil, fmt.Errorf("yandexauth: build metadata sdk: %w", err)
	}
	return sdk, nil
}

// IAMToken returns the current valid IAM bearer token.
//
// On the v0.31.0 SDK this method does NOT cache the token — every call
// performs a fresh HTTP exchange with the IAM API. That is acceptable
// for the current usage (builder-svc requests one token per registry
// push, which is infrequent). If call frequency grows, add an external
// cache in this package.
//
// The SDK caches the token INTERNALLY for its own gRPC calls via an
// interceptor, so the typical flow `sdk.Serverless().Containers().Create()`
// is unaffected.
func IAMToken(ctx context.Context, sdk *ycsdk.SDK) (string, error) {
	resp, err := sdk.CreateIAMToken(ctx)
	if err != nil {
		return "", fmt.Errorf("yandexauth: get iam token: %w", err)
	}
	return resp.IamToken, nil
}
