// Package edge resolves public hostnames to running local deploys.
//
// It is deliberately a data-plane package, not a resurrection of the former
// service-to-service HTTP API. The only persisted state it reads is the
// deploy/domain alias model, and the only result it exposes is a proxy target.
package edge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ErrRouteNotFound means the hostname has no verified, running target.
var ErrRouteNotFound = errors.New("edge route not found")

// Route is the minimal mapping the data-plane proxy needs. Target is built
// from an application-owned container name and a validated saga port; neither
// value comes from the request.
type Route struct {
	DeployID string
	Target   string
}

// Resolver is shared by the proxy and the Caddy TLS ask handler. Requiring a
// live route for both means a certificate is never issued for a verified alias
// that currently points at nothing.
type Resolver interface {
	Resolve(ctx context.Context, host string) (*Route, error)
}

// Repository resolves routes directly from the monolith's SQLite store.
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

const customRouteSQL = `
SELECT d.id, d.container_id, s.app_port
FROM custom_domains cd
JOIN deploys d ON d.id = cd.target_deploy_id
JOIN deploy_sagas s ON s.deploy_id = d.id
WHERE cd.domain = ?
  AND cd.status = 'verified'
  AND d.status = 'running'
  AND d.container_id IS NOT NULL
  AND d.container_id <> ''
  AND s.app_port IS NOT NULL
LIMIT 1`

// Resolve maps a verified custom-domain alias to a running container. Unknown,
// pending, stopped and targetless rows are deliberately indistinguishable.
func (r *Repository) Resolve(ctx context.Context, rawHost string) (*Route, error) {
	host := NormalizeHost(rawHost)
	if host == "" {
		return nil, ErrRouteNotFound
	}

	var deployID, containerID string
	var port int
	if err := r.db.QueryRowContext(ctx, customRouteSQL, host).Scan(&deployID, &containerID, &port); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRouteNotFound
		}
		return nil, fmt.Errorf("resolve edge route: %w", err)
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("resolve edge route: invalid stored port %d", port)
	}

	// Docker registers the container name in the embedded DNS of the shared
	// routing network. Constructing it from the database UUID avoids proxying an
	// arbitrary endpoint_url and keeps request input out of the upstream address.
	targetHost := "snaphost-deploy-" + deployID
	return &Route{
		DeployID: deployID,
		Target:   "http://" + net.JoinHostPort(targetHost, strconv.Itoa(port)),
	}, nil
}

// NormalizeHost applies the same normalization used by domain attachment and
// by Caddy's SNI ask path.
func NormalizeHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if split, _, err := net.SplitHostPort(host); err == nil {
		host = split
	}
	return strings.TrimSuffix(host, ".")
}
