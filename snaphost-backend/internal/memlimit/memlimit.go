// Package memlimit tells the Go runtime about the ceiling it is running under.
//
// Nothing in the inherited tree set GOMEMLIMIT or GOGC, while the production
// Compose file did set a container memory limit. Go therefore grew its heap
// against the machine's memory and learned about the container's limit only
// when the kernel killed it — the one failure mode where the process gets no
// chance to shed anything first.
//
// # Why the limit is detected rather than configured
//
// An operator who sets a memory limit in one place and a GOMEMLIMIT in another
// has two numbers to keep in step, and the failure when they drift is an OOM
// kill under load rather than an error at startup. The cgroup already knows the
// answer, so this reads it.
//
// # Why there is a reserve
//
// GOMEMLIMIT bounds the Go heap. It does not bound this container, and on this
// platform the difference includes:
//
//   - the build workspace and the uploaded archive are files, and the page
//     cache holding them is charged to the cgroup too;
//   - the Go runtime's own non-heap memory — stacks, the binary, mmapped
//     metadata — is outside GOMEMLIMIT by definition.
//
// Leaving an explicit reserve prevents the heap from consuming the entire
// cgroup while those allocations are still live.
package memlimit

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultReserve is held back from the cgroup limit for page cache, goroutine
// stacks, the binary and other memory that GOMEMLIMIT does not cover.
const DefaultReserve = 128 << 20 // 128 MiB

// MinLimit is the floor below which a derived GOMEMLIMIT is refused. Setting it
// under this would make the garbage collector run continuously without ever
// getting under the limit, which trades an OOM kill for a process that is alive
// and does no work — the worse of the two.
const MinLimit = 64 << 20 // 64 MiB

// ErrNoLimit reports that the cgroup does not constrain memory: the container
// was started without a limit, or this is not a container at all.
var ErrNoLimit = errors.New("no cgroup memory limit")

// Detect returns the cgroup memory limit in bytes.
//
// root is the mount point to read, normally /sys/fs/cgroup. Both cgroup
// versions are handled because a Docker host may be either, and the file that
// exists is the version in use.
func Detect(root string) (int64, error) {
	// cgroup v2. Inside a container this is the namespaced file, so it is the
	// container's own limit rather than a parent's.
	if limit, err := readLimit(filepath.Join(root, "memory.max")); err == nil {
		return limit, nil
	}
	// cgroup v1.
	if limit, err := readLimit(filepath.Join(root, "memory", "memory.limit_in_bytes")); err == nil {
		return limit, nil
	}
	return 0, ErrNoLimit
}

// readLimit parses one of the two limit files.
func readLimit(path string) (int64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	text := strings.TrimSpace(string(raw))

	// cgroup v2 spells "no limit" as the literal string.
	if text == "max" {
		return 0, ErrNoLimit
	}

	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	if value <= 0 {
		return 0, ErrNoLimit
	}
	// cgroup v1 spells "no limit" as a number near the top of the range —
	// PAGE_COUNTER_MAX times the page size, which varies by kernel and
	// architecture. Anything at that scale is not a limit anyone configured.
	if value >= math.MaxInt64/2 {
		return 0, ErrNoLimit
	}
	return value, nil
}

// Derive turns a cgroup limit into a GOMEMLIMIT, or reports why it will not.
//
// A limit so small that the reserve consumes it is refused rather than clamped
// to the floor: it means the container limit and what this container actually
// runs disagree, and quietly picking a number would hide that.
func Derive(cgroupLimit, reserve int64) (int64, error) {
	if reserve < 0 {
		reserve = DefaultReserve
	}
	derived := cgroupLimit - reserve
	if derived < MinLimit {
		return 0, fmt.Errorf(
			"container memory limit %d is too small: %d is reserved for non-heap use, leaving less than the %d minimum",
			cgroupLimit, reserve, int64(MinLimit))
	}
	return derived, nil
}
