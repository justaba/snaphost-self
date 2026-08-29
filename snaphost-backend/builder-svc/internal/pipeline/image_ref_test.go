package pipeline

import (
	"strings"
	"testing"
)

func TestImageRefForJob(t *testing.T) {
	const (
		userID   = "a4a355f8-9769-454f-b5c0-9782acceebc0"
		deployID = "b132cb0d-ce92-4009-a8f3-221d86d8607c"
	)

	cases := []struct {
		name       string
		registry   string
		wantPrefix string
	}{
		{
			name:       "yandex production registry",
			registry:   "cr.yandex/crp123/snaphost",
			wantPrefix: "cr.yandex/crp123/snaphost/proj-",
		},
		{
			name:       "docker dev registry",
			registry:   "registry:5000/snaphost",
			wantPrefix: "registry:5000/snaphost/proj-",
		},
		{
			name:       "trims trailing slash",
			registry:   "cr.yandex/crp123/snaphost/",
			wantPrefix: "cr.yandex/crp123/snaphost/proj-",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := imageRefForJob(tc.registry, userID, deployID)
			if !strings.HasPrefix(got, tc.wantPrefix) {
				t.Fatalf("imageRefForJob() = %q, want prefix %q", got, tc.wantPrefix)
			}
			if !strings.HasSuffix(got, ":"+deployID) {
				t.Fatalf("imageRefForJob() = %q, want deploy_id tag %q", got, deployID)
			}
			if strings.Contains(got, "snaphost//proj-") {
				t.Fatalf("imageRefForJob() contains double slash: %q", got)
			}
		})
	}
}
