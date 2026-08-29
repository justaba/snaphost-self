package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func writeDockerfile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Dockerfile")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write dockerfile: %v", err)
	}
	return path
}

func TestFixedPortRiskFlagsHardBoundServer(t *testing.T) {
	// The 2026-07-19 incident: nginx serving on 80, nothing reading PORT.
	path := writeDockerfile(t, "FROM nginx:alpine\nCOPY dist /usr/share/nginx/html\nEXPOSE 80\n")

	port, risky := FixedPortRisk(path)
	if !risky {
		t.Fatal("a Dockerfile exposing a fixed port with no mention of PORT must be flagged")
	}
	if port != 80 {
		t.Fatalf("port = %d, want 80", port)
	}
}

func TestFixedPortRiskStaysQuietWhenPortIsHonored(t *testing.T) {
	cases := map[string]string{
		"env default":  "FROM node:20\nENV PORT=3000\nEXPOSE 3000\nCMD [\"node\", \"server.js\"]\n",
		"braced":       "FROM node:20\nEXPOSE 3000\nCMD [\"sh\", \"-c\", \"node server.js --port ${PORT}\"]\n",
		"bare $PORT":   "FROM python:3.12\nEXPOSE 8000\nCMD gunicorn -b 0.0.0.0:$PORT app:app\n",
		"no EXPOSE":    "FROM node:20\nCMD [\"node\", \"server.js\"]\n",
		"missing file": "",
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeDockerfile(t, content)
			if name == "missing file" {
				path = filepath.Join(t.TempDir(), "absent")
			}
			if _, risky := FixedPortRisk(path); risky {
				t.Fatalf("%s must not be flagged: the check is advisory and a false positive costs trust", name)
			}
		})
	}
}
