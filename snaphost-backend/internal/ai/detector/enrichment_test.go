package detector

import (
	"testing"

	"snaphost/internal/ai/templates"
)

func TestParseVersionFile(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain", "18", "18"},
		{"v_prefix", "v18.20.0", "18.20.0"},
		{"trailing_newline", "16\n", "16"},
		{"leading_whitespace", "  18.0.0  ", "18.0.0"},
		{"comment_then_value", "# old version\n16", "16"},
		{"empty", "", ""},
		{"only_whitespace", "\n\n  \n", ""},
		{"alias_passthrough", "lts/hydrogen", "lts/hydrogen"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseVersionFile(c.in); got != c.want {
				t.Fatalf("parseVersionFile(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestEnrich_NodeAndPythonVersionFiles(t *testing.T) {
	raw := templates.ProjectSignals{
		KeyFiles: map[string]string{
			".nvmrc":          "v16\n",
			".node-version":   "18.20.0",
			".python-version": "3.11.4",
		},
	}
	got := Enrich(raw)
	if got.NvmrcVersion != "16" {
		t.Errorf("NvmrcVersion = %q, want %q", got.NvmrcVersion, "16")
	}
	if got.NodeVersionFile != "18.20.0" {
		t.Errorf("NodeVersionFile = %q, want %q", got.NodeVersionFile, "18.20.0")
	}
	if got.PythonVersionFile != "3.11.4" {
		t.Errorf("PythonVersionFile = %q, want %q", got.PythonVersionFile, "3.11.4")
	}
}

func TestEnrich_MissingFiles(t *testing.T) {
	got := Enrich(templates.ProjectSignals{KeyFiles: map[string]string{}})
	if got.NvmrcVersion != "" || got.NodeVersionFile != "" || got.PythonVersionFile != "" {
		t.Errorf("expected empty version fields, got %+v", got)
	}
}

func TestEnrich_MalformedJSONDoesNotPanic(t *testing.T) {
	raw := templates.ProjectSignals{
		KeyFiles: map[string]string{"package.json": "{not json"},
	}
	got := Enrich(raw)
	if got.PackageJSON != nil {
		t.Errorf("expected nil PackageJSON on malformed input, got %+v", got.PackageJSON)
	}
}
