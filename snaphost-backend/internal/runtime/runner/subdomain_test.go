package runner

import "testing"

func TestGenerateSubdomain(t *testing.T) {
	cases := []struct {
		name     string
		deployID string
		want     string
	}{
		{"standard uuid v4", "269ce67c-cbc4-4d17-a720-467e50447918", "proj-269ce67ccbc44d17a720467e50447918"},
		{"uuid without dashes", "269ce67ccbc44d17a720467e50447918", "proj-269ce67ccbc44d17a720467e50447918"},
		{"empty deploy id", "", "proj-"},
		{"all-zero uuid", "00000000-0000-0000-0000-000000000000", "proj-00000000000000000000000000000000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := generateSubdomain(tc.deployID)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if len(got) > 63 {
				t.Errorf("subdomain length %d exceeds RFC 1035 label limit", len(got))
			}
		})
	}
}

func TestGenerateSubdomain_LengthForFullUUID(t *testing.T) {
	got := generateSubdomain("269ce67c-cbc4-4d17-a720-467e50447918")
	// "proj-" (5) + 32 hex chars = 37.
	if len(got) != 37 {
		t.Errorf("expected length 37 for full-UUID subdomain, got %d (%q)", len(got), got)
	}
}
