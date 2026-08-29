//go:build yandex
// +build yandex

package yandex

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Spec is a loose representation of the API Gateway OpenAPI document.
// We model paths as a generic map because Yandex's x-yc extensions don't fit
// a strict struct cleanly and we only need to add/remove top-level path keys.
type Spec struct {
	root map[string]interface{}
}

// LoadSpec parses YAML bytes into a mutable Spec.
func LoadSpec(raw []byte) (*Spec, error) {
	m := map[string]interface{}{}
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("unmarshal gateway spec: %w", err)
	}
	if _, ok := m["paths"]; !ok {
		m["paths"] = map[string]interface{}{}
	}
	return &Spec{root: m}, nil
}

// Marshal serializes the spec back to YAML.
func Marshal(s *Spec) ([]byte, error) {
	return yaml.Marshal(s.root)
}

const gatewayProxyPath = "/{path+}"

func legacyRouteKey(hostname string) string {
	return "/__snaphost__/" + hostname
}

// AddRoute inserts (or overwrites) a route that proxies all methods on the
// given hostname to the named serverless container. The host match is
// expressed via the `x-yc-apigateway-host` extension on the path.
func AddRoute(s *Spec, hostname, containerID, saID string) error {
	if hostname == "" || containerID == "" || saID == "" {
		return errors.New("AddRoute: hostname, containerID, saID required")
	}
	paths, ok := s.root["paths"].(map[string]interface{})
	if !ok {
		paths = map[string]interface{}{}
		s.root["paths"] = paths
	}
	delete(paths, legacyRouteKey(hostname))
	paths[gatewayProxyPath] = map[string]interface{}{
		"x-snaphost-host": hostname,
		"x-yc-apigateway-any-method": map[string]interface{}{
			"x-yc-apigateway-integration": map[string]interface{}{
				"type":               "serverless_containers",
				"container_id":       containerID,
				"service_account_id": saID,
			},
			"parameters": []interface{}{
				map[string]interface{}{
					"explode":  false,
					"in":       "path",
					"name":     "path",
					"required": false,
					"schema": map[string]interface{}{
						"default": "-",
						"type":    "string",
					},
					"style": "simple",
				},
			},
		},
	}
	return nil
}

// RemoveRoute deletes the route for the given hostname, if present.
func RemoveRoute(s *Spec, hostname string) {
	paths, ok := s.root["paths"].(map[string]interface{})
	if !ok {
		return
	}
	delete(paths, legacyRouteKey(hostname))
	entry, ok := paths[gatewayProxyPath].(map[string]interface{})
	if !ok {
		return
	}
	if host, _ := entry["x-snaphost-host"].(string); host != hostname {
		return
	}
	paths[gatewayProxyPath] = defaultNotFoundRoute()
}

func defaultNotFoundRoute() map[string]interface{} {
	return map[string]interface{}{
		"x-yc-apigateway-any-method": map[string]interface{}{
			"x-yc-apigateway-integration": map[string]interface{}{
				"type":      "dummy",
				"http_code": 404,
				"http_headers": map[string]interface{}{
					"Content-Type": "text/plain",
				},
				"content": map[string]interface{}{
					"*": "Deployment not found",
				},
			},
			"parameters": []interface{}{
				map[string]interface{}{
					"name":     "path",
					"in":       "path",
					"required": true,
					"schema": map[string]interface{}{
						"type": "string",
					},
				},
			},
		},
	}
}
