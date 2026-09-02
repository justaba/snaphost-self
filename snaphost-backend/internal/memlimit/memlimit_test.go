package memlimit

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func writeCgroupV2(t *testing.T, contents string) string {
	t.Helper()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "memory.max"), []byte(contents), 0o600); err != nil {
		t.Fatalf("write memory.max: %v", err)
	}
	return root
}

func writeCgroupV1(t *testing.T, contents string) string {
	t.Helper()

	root := t.TempDir()
	dir := filepath.Join(root, "memory")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "memory.limit_in_bytes"), []byte(contents), 0o600); err != nil {
		t.Fatalf("write memory.limit_in_bytes: %v", err)
	}
	return root
}

func TestDetectCgroupV2(t *testing.T) {
	limit, err := Detect(writeCgroupV2(t, "536870912\n"))
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if limit != 536870912 {
		t.Fatalf("limit = %d, want 536870912", limit)
	}
}

func TestDetectCgroupV1(t *testing.T) {
	limit, err := Detect(writeCgroupV1(t, "536870912\n"))
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if limit != 536870912 {
		t.Fatalf("limit = %d, want 536870912", limit)
	}
}

// The two ways a cgroup says "no limit", and they look nothing alike. v2 writes
// a word; v1 writes a number so large it would otherwise read as a limit of
// eight exabytes and produce a GOMEMLIMIT that means nothing.
func TestUnlimitedIsRecognisedInBothVersions(t *testing.T) {
	if _, err := Detect(writeCgroupV2(t, "max\n")); !errors.Is(err, ErrNoLimit) {
		t.Errorf("cgroup v2 \"max\" = %v, want ErrNoLimit", err)
	}
	for _, sentinel := range []int64{math.MaxInt64, 9223372036854771712, math.MaxInt64 / 2} {
		root := writeCgroupV1(t, strconv.FormatInt(sentinel, 10))
		if _, err := Detect(root); !errors.Is(err, ErrNoLimit) {
			t.Errorf("cgroup v1 %d = %v, want ErrNoLimit", sentinel, err)
		}
	}
}

func TestNoCgroupFilesAtAll(t *testing.T) {
	if _, err := Detect(t.TempDir()); !errors.Is(err, ErrNoLimit) {
		t.Fatalf("Detect with no files = %v, want ErrNoLimit", err)
	}
}

func TestMalformedContentsAreAnError(t *testing.T) {
	if _, err := Detect(writeCgroupV2(t, "not-a-number\n")); err == nil {
		t.Fatal("a malformed limit was accepted")
	}
}

// v2 is checked first. A host running both hierarchies must not be read through
// the older one, whose value belongs to a different accounting scheme.
func TestCgroupV2WinsWhenBothExist(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "memory.max"), []byte("111111111"), 0o600); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	dir := filepath.Join(root, "memory")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "memory.limit_in_bytes"), []byte("222222222"), 0o600); err != nil {
		t.Fatalf("write v1: %v", err)
	}

	limit, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if limit != 111111111 {
		t.Fatalf("limit = %d, want the cgroup v2 value", limit)
	}
}

func TestDeriveSubtractsTheReserve(t *testing.T) {
	derived, err := Derive(512<<20, DefaultReserve)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if want := int64(384 << 20); derived != want {
		t.Fatalf("derived = %d, want %d", derived, want)
	}
}

// A container limit that the reserve eats is refused rather than clamped. The
// clamp would produce a plausible number from a configuration that cannot work,
// and the symptom would arrive later as an OOM kill under load.
func TestDeriveRefusesALimitTheReserveConsumes(t *testing.T) {
	for _, limit := range []int64{0, 1 << 20, DefaultReserve, DefaultReserve + MinLimit - 1} {
		if _, err := Derive(limit, DefaultReserve); err == nil {
			t.Errorf("Derive(%d) succeeded; the reserve leaves less than the floor", limit)
		}
	}
	if _, err := Derive(DefaultReserve+MinLimit, DefaultReserve); err != nil {
		t.Errorf("Derive at exactly the floor = %v, want success", err)
	}
}

// A negative reserve means "unset" and takes the default, so a caller that
// forgets to configure one does not end up with GOMEMLIMIT equal to the whole
// cgroup — which is the recipe this package exists to avoid.
func TestANegativeReserveTakesTheDefault(t *testing.T) {
	derived, err := Derive(1<<30, -1)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if want := int64(1<<30) - DefaultReserve; derived != want {
		t.Fatalf("derived = %d, want the default reserve applied", derived)
	}
}

// A zero reserve is a deliberate choice, not an unset one, and is honoured.
func TestAZeroReserveIsHonoured(t *testing.T) {
	derived, err := Derive(1<<30, 0)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if derived != 1<<30 {
		t.Fatalf("derived = %d, want the whole limit", derived)
	}
}
