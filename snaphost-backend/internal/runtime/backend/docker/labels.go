package docker

import (
	"fmt"

	"snaphost/internal/runtime/config"
)

// BuildTraefikLabels always records ownership. It adds Traefik routing labels
// only when local development explicitly supplies a domain suffix; production
// routing uses verified domains through Caddy.
func BuildTraefikLabels(deployID, userID, subdomain, domainSuffix string, port int) map[string]string {
	labels := map[string]string{
		"snaphost.deploy.id":         deployID,
		"snaphost.deploy.user_id":    userID,
		"snaphost.deploy.managed_by": "snaphost",
	}
	if domainSuffix == "" {
		return labels
	}
	labels["traefik.enable"] = "true"
	labels["traefik.docker.network"] = config.RoutingNetwork
	labels[fmt.Sprintf("traefik.http.routers.%s.rule", deployID)] = fmt.Sprintf("Host(`%s.%s`)", subdomain, domainSuffix)
	labels[fmt.Sprintf("traefik.http.routers.%s.entrypoints", deployID)] = "web"
	labels[fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port", deployID)] = fmt.Sprintf("%d", port)
	return labels
}
