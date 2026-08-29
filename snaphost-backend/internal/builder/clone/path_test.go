package clone

import (
	"path/filepath"
	"testing"
)

func TestIsWithin(t *testing.T) {
	abs := func(p string) string {
		a, err := filepath.Abs(p)
		if err != nil {
			t.Fatalf("filepath.Abs(%q): %v", p, err)
		}
		return a
	}

	parent := abs("/tmp/workdir")
	subFile := filepath.Join(parent, "src", "main.go")
	hiddenInside := filepath.Join(parent, ".config")
	hiddenNested := filepath.Join(parent, ".config", "auth.json")
	siblingDir := abs("/tmp/other")
	parentOfParent := abs("/tmp")
	unrelated := abs("/var/log")

	cases := []struct {
		name   string
		child  string
		parent string
		want   bool
	}{
		{"identical absolute paths", parent, parent, true},
		{"child is direct descendant file", subFile, parent, true},
		{"hidden file inside parent (.config)", hiddenInside, parent, true},
		{"hidden file nested inside parent", hiddenNested, parent, true},
		{"sibling directory is NOT within", siblingDir, parent, false},
		{"parent of parent is NOT within child", parentOfParent, parent, false},
		{"completely unrelated path", unrelated, parent, false},
		{"hidden file outside parent (.foo in /tmp)", filepath.Join(parentOfParent, ".foo"), parent, false},
		{"path with .. that resolves outside parent", filepath.Join(parent, "..", "escape"), parent, false},
		{"path with .. that resolves back inside parent", filepath.Join(parent, "sub", "..", "src"), parent, true},
		{"path with . segment stays inside", filepath.Join(parent, ".", "src"), parent, true},
		{"relative child path (Abs resolved to cwd, not parent)", "subdir", parent, false},
		{"empty child", "", parent, false},
		{"empty parent", parent, "", false},
		{"both empty", "", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isWithin(tc.child, tc.parent)
			if got != tc.want {
				t.Errorf("isWithin(%q, %q) = %v, want %v", tc.child, tc.parent, got, tc.want)
			}
		})
	}
}
