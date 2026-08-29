package docker

import (
	"fmt"

	"snaphost/runner-svc/config"
)

// BuildTraefikLabels returns Docker labels that configure Traefik v3 HTTP routing
// for a deployment container. The labels instruct Traefik to route requests for
// {subdomain}.{domainSuffix} to the container on the specified port.
func BuildTraefikLabels(deployID, userID, subdomain, domainSuffix string, port int) map[string]string {
	return map[string]string{
		// Traefik v3 routing labels.
		"traefik.enable":         "true",
		"traefik.docker.network": config.TraefikNetwork,
		fmt.Sprintf("traefik.http.routers.%s.rule", deployID):                      fmt.Sprintf("Host(`%s.%s`)", subdomain, domainSuffix),
		fmt.Sprintf("traefik.http.routers.%s.entrypoints", deployID):               "web",
		fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port", deployID): fmt.Sprintf("%d", port),

		// SnapHost-specific labels used by the watchdog and operational tooling.
		"snaphost.deploy.id":         deployID,
		"snaphost.deploy.user_id":    userID,
		"snaphost.deploy.managed_by": "snaphost",
	}
}
