package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type Route struct {
	DeployID    string `json:"deploy_id"`
	ContainerID string `json:"container_id"`
	Status      string `json:"status"`
	Host        string `json:"host"`
}

type LookupClient struct {
	BaseURL    string
	Secret     string
	HTTPClient *http.Client
}

func (c *LookupClient) Lookup(ctx context.Context, host string) (*Route, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	u := base + "/internal/routes?host=" + url.QueryEscape(host)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Webhook-Secret", c.Secret)

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lookup route: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrRouteNotFound
	default:
		return nil, fmt.Errorf("lookup route: unexpected status %d", resp.StatusCode)
	}

	var route Route
	if err := json.NewDecoder(resp.Body).Decode(&route); err != nil {
		return nil, fmt.Errorf("decode route: %w", err)
	}
	if route.Status != "running" || route.ContainerID == "" {
		return nil, ErrRouteNotFound
	}
	return &route, nil
}
