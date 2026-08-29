package detector

import (
	"encoding/json"
	"strings"

	"snaphost/internal/ai/templates"
)

// Enrich parses known key files into structured formats.
//
// Parsing is best-effort: a malformed file (e.g. invalid JSON in package.json,
// junk in .nvmrc) is silently skipped — Enrich never propagates parse errors
// because the downstream pipeline must still get a usable ProjectSignals and
// can fall back to safe defaults. This matches the pipeline.Permanent /
// pipeline.Transient classification invariant established in Task 5a:
// enrichment is never a source of pipeline errors.
func Enrich(raw templates.ProjectSignals) templates.ProjectSignals {
	if content, ok := raw.KeyFiles["package.json"]; ok {
		var pkg templates.PackageJSON
		if err := json.Unmarshal([]byte(content), &pkg); err == nil {
			raw.PackageJSON = &pkg
		}
	}

	if content, ok := raw.KeyFiles["go.mod"]; ok {
		raw.GoMod = parseGoMod(content)
	}

	if content, ok := raw.KeyFiles["requirements.txt"]; ok {
		lines := strings.Split(content, "\n")
		var reqs []string
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				reqs = append(reqs, strings.ToLower(line))
			}
		}
		raw.RequirementsTxt = reqs
	}

	// Runtime version hints. Precedence is decided downstream
	// (templates.PickNodeVersion). Here we just extract.
	if content, ok := raw.KeyFiles[".nvmrc"]; ok {
		raw.NvmrcVersion = parseVersionFile(content)
	}
	if content, ok := raw.KeyFiles[".node-version"]; ok {
		raw.NodeVersionFile = parseVersionFile(content)
	}
	if content, ok := raw.KeyFiles[".python-version"]; ok {
		raw.PythonVersionFile = parseVersionFile(content)
	}

	return raw
}

func parseGoMod(content string) *templates.GoMod {
	var g templates.GoMod
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			g.ModulePath = strings.TrimSpace(strings.TrimPrefix(line, "module"))
		} else if strings.HasPrefix(line, "go ") {
			g.GoVersion = strings.TrimSpace(strings.TrimPrefix(line, "go"))
		}
	}
	if g.ModulePath == "" {
		return nil
	}
	return &g
}

// parseVersionFile reads a single-line version file (.nvmrc, .node-version,
// .python-version). Strips whitespace and a leading 'v' if present, skips
// blank and comment lines. Returns "" for files with no useful content.
// Non-numeric content like "lts/hydrogen" is returned unchanged — the
// caller (PickNodeVersion) decides how strict to be.
func parseVersionFile(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return strings.TrimPrefix(line, "v")
	}
	return ""
}
