// Package deployments defines the runtime's narrow view of durable deploy
// state. Its implementation reads the control-plane repository directly.
package deployments

import (
	"errors"
	"time"
)

var ErrNotFound = errors.New("deploy not found")

type SetRunningRequest struct {
	ImageRef     string
	EndpointURL  string
	Subdomain    string
	ContainerID  string
	TTLExpiresAt time.Time
}

type Info struct {
	DeployID    string
	UserID      string
	Status      string
	ImageRef    string
	ContainerID string
}

type Expired struct {
	ID          string
	UserID      string
	ContainerID string
}

type ImageCleanup struct {
	ID       string
	ImageRef string
}
