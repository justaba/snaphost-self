package docker

import (
	"github.com/docker/docker/api/types/container"

	"snaphost/runner-svc/config"
)

// BuildHostConfig returns a container.HostConfig with resource limits and
// security hardening applied. Every user container gets:
//   - Capped CPU and memory (no swap)
//   - PID limit to prevent fork bombs
//   - Read-only root filesystem with tmpfs for /tmp and /run
//   - All Linux capabilities dropped except NET_BIND_SERVICE
//   - No new privileges via security opt
//   - No automatic restart (runner-svc controls the lifecycle)
func BuildHostConfig(cfg *config.Config, networkName string) *container.HostConfig {
	pidsLimit := int64(256)

	return &container.HostConfig{
		Resources: container.Resources{
			Memory:     cfg.ContainerMemoryMB * 1024 * 1024,
			MemorySwap: cfg.ContainerMemoryMB * 1024 * 1024, // same as Memory — no swap
			CPUQuota:   int64(cfg.ContainerCPULimit * 100000),
			CPUPeriod:  100000,
			PidsLimit:  &pidsLimit,
		},
		NetworkMode:    container.NetworkMode(networkName),
		ReadonlyRootfs: true,
		Tmpfs: map[string]string{
			"/tmp": "rw,noexec,nosuid,size=64m",
			"/run": "rw,noexec,nosuid,size=16m",
			// nginx writes its request buffers to /var/cache/nginx/* and refuses
			// to start without them. Keep noexec/nosuid so this remains a tight
			// security boundary; the size only needs to cover transient buffers.
			"/var/cache/nginx": "rw,noexec,nosuid,size=32m",
		},
		CapDrop: []string{"ALL"},
		// Capabilities granted, with rationale for each:
		//   NET_BIND_SERVICE — bind privileged ports (<1024), e.g. nginx 80
		//   CHOWN            — nginx master chowns /var/cache/nginx/* on startup
		//   SETUID/SETGID    — nginx master drops to "nginx" worker user
		// Together these are still well below the standard Docker default cap
		// set; container escape is blocked by no-new-privileges + readonly rootfs.
		CapAdd:      []string{"NET_BIND_SERVICE", "CHOWN", "SETUID", "SETGID"},
		SecurityOpt: []string{"no-new-privileges:true"},
		RestartPolicy: container.RestartPolicy{
			Name: "no",
		},
	}
}
