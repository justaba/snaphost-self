//go:build yandex
// +build yandex

package yandex

import "testing"

func TestRemoveRouteRestoresDummyWhenProxyHostMatches(t *testing.T) {
	spec, err := LoadSpec([]byte(`openapi: 3.0.0
paths:
  /__snaphost__/smoke.example:
    x-yc-apigateway-any-method:
      x-yc-apigateway-integration:
        type: serverless_containers
        container_id: legacy-container
  /{path+}:
    x-snaphost-host: smoke.example
    x-yc-apigateway-any-method:
      x-yc-apigateway-integration:
        type: serverless_containers
        container_id: current-container
        service_account_id: runner-sa
`))
	if err != nil {
		t.Fatal(err)
	}

	RemoveRoute(spec, "smoke.example")

	paths := spec.root["paths"].(map[string]interface{})
	if _, ok := paths[legacyRouteKey("smoke.example")]; ok {
		t.Fatalf("legacy route was not removed")
	}
	integration := proxyIntegration(t, spec)
	if got := integration["type"]; got != "dummy" {
		t.Fatalf("proxy integration type = %v, want dummy", got)
	}
	if got := integration["http_code"]; got != 404 {
		t.Fatalf("proxy http_code = %v, want 404", got)
	}
}

func TestRemoveRouteKeepsProxyWhenHostDiffers(t *testing.T) {
	spec, err := LoadSpec([]byte(`openapi: 3.0.0
paths:
  /{path+}:
    x-snaphost-host: other.example
    x-yc-apigateway-any-method:
      x-yc-apigateway-integration:
        type: serverless_containers
        container_id: other-container
        service_account_id: runner-sa
`))
	if err != nil {
		t.Fatal(err)
	}

	RemoveRoute(spec, "smoke.example")

	integration := proxyIntegration(t, spec)
	if got := integration["type"]; got != "serverless_containers" {
		t.Fatalf("proxy integration type = %v, want serverless_containers", got)
	}
	if got := integration["container_id"]; got != "other-container" {
		t.Fatalf("proxy container_id = %v, want other-container", got)
	}
}

func TestRemoveRouteDeletesLegacyRoute(t *testing.T) {
	spec, err := LoadSpec([]byte(`openapi: 3.0.0
paths:
  /__snaphost__/smoke.example:
    x-yc-apigateway-any-method:
      x-yc-apigateway-integration:
        type: serverless_containers
        container_id: legacy-container
`))
	if err != nil {
		t.Fatal(err)
	}

	RemoveRoute(spec, "smoke.example")

	paths := spec.root["paths"].(map[string]interface{})
	if _, ok := paths[legacyRouteKey("smoke.example")]; ok {
		t.Fatalf("legacy route was not removed")
	}
}

func TestHostnameForContainerInSpec(t *testing.T) {
	spec, err := LoadSpec([]byte(`openapi: 3.0.0
paths:
  /{path+}:
    x-snaphost-host: smoke.example
    x-yc-apigateway-any-method:
      x-yc-apigateway-integration:
        type: serverless_containers
        container_id: current-container
        service_account_id: runner-sa
`))
	if err != nil {
		t.Fatal(err)
	}

	if got := hostnameForContainerInSpec(spec, "current-container"); got != "smoke.example" {
		t.Fatalf("hostnameForContainerInSpec() = %q, want smoke.example", got)
	}
	if got := hostnameForContainerInSpec(spec, "missing-container"); got != "" {
		t.Fatalf("hostnameForContainerInSpec() = %q, want empty", got)
	}
}

func proxyIntegration(t *testing.T, spec *Spec) map[string]interface{} {
	t.Helper()
	paths, ok := spec.root["paths"].(map[string]interface{})
	if !ok {
		t.Fatalf("paths missing or invalid")
	}
	entry, ok := paths[gatewayProxyPath].(map[string]interface{})
	if !ok {
		t.Fatalf("proxy path missing or invalid")
	}
	method, ok := entry["x-yc-apigateway-any-method"].(map[string]interface{})
	if !ok {
		t.Fatalf("proxy method missing or invalid")
	}
	integration, ok := method["x-yc-apigateway-integration"].(map[string]interface{})
	if !ok {
		t.Fatalf("proxy integration missing or invalid")
	}
	return integration
}
