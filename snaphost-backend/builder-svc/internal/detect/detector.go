// Package detect provides project type detection by inspecting manifest files
// in a cloned repository. Detection results guide Dockerfile generation.
package detect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ProjectInfo describes the detected project type and its build characteristics.
type ProjectInfo struct {
	// Language is the primary language (node, go, python, ruby, unknown).
	Language string
	// Framework is the detected framework (nextjs, react-vite, cra, express, etc.).
	Framework string
	// DockerfilePath is the path to an existing Dockerfile, relative to the repo root.
	// Empty if no Dockerfile was found (signals the pipeline to invoke AI Orchestrator).
	DockerfilePath string
	// BuildTool is the package manager or build tool (npm, yarn, pnpm, go, pip, etc.).
	BuildTool string
	// EntryPoint is the detected application entry point file.
	EntryPoint string
	// UnsupportedReason is a non-empty, user-facing string when the project is
	// recognized as a class snaphost cannot deploy (mobile / desktop / etc.).
	// The pipeline aborts before invoking AI when this is set.
	UnsupportedReason string
}

// dockerfileCandidates is the ordered list of filenames to search for.
var dockerfileCandidates = []string{
	"Dockerfile",
	"dockerfile",
	"Dockerfile.prod",
	"Dockerfile.production",
}

// Detect inspects the given repository directory and returns project metadata.
// If a Dockerfile already exists, detection returns immediately without
// analysing manifest files further.
func Detect(repoDir string) (*ProjectInfo, error) {
	info := &ProjectInfo{Language: "unknown"}

	// Check for existing Dockerfile.
	for _, name := range dockerfileCandidates {
		candidate := filepath.Join(repoDir, name)
		if _, err := os.Stat(candidate); err == nil {
			info.DockerfilePath = name
			return info, nil
		}
	}

	// Node.js detection via package.json.
	pkgPath := filepath.Join(repoDir, "package.json")
	if _, err := os.Stat(pkgPath); err == nil {
		info.Language = "node"
		info.BuildTool = detectNodeBuildTool(repoDir)
		info.Framework = detectNodeFramework(pkgPath)
		info.EntryPoint = detectNodeEntryPoint(pkgPath)
		info.UnsupportedReason = detectNodeUnsupported(pkgPath)
		return info, nil
	}

	// Go detection via go.mod.
	if _, err := os.Stat(filepath.Join(repoDir, "go.mod")); err == nil {
		info.Language = "go"
		info.BuildTool = "go"
		info.EntryPoint = "main.go"
		return info, nil
	}

	// Python detection via requirements.txt or pyproject.toml.
	if _, err := os.Stat(filepath.Join(repoDir, "requirements.txt")); err == nil {
		info.Language = "python"
		info.BuildTool = "pip"
		info.Framework = detectPythonFramework(filepath.Join(repoDir, "requirements.txt"))
		return info, nil
	}
	if _, err := os.Stat(filepath.Join(repoDir, "pyproject.toml")); err == nil {
		info.Language = "python"
		info.BuildTool = "pip"
		return info, nil
	}

	// Ruby detection via Gemfile.
	if _, err := os.Stat(filepath.Join(repoDir, "Gemfile")); err == nil {
		info.Language = "ruby"
		info.BuildTool = "bundler"
		return info, nil
	}

	return info, nil
}

// detectNodeBuildTool determines the Node.js package manager from lockfiles.
func detectNodeBuildTool(repoDir string) string {
	if _, err := os.Stat(filepath.Join(repoDir, "pnpm-lock.yaml")); err == nil {
		return "pnpm"
	}
	if _, err := os.Stat(filepath.Join(repoDir, "yarn.lock")); err == nil {
		return "yarn"
	}
	return "npm"
}

// packageJSON is a minimal representation for dependency scanning.
type packageJSON struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Main            string            `json:"main"`
}

// detectNodeUnsupported flags Node.js projects whose dependencies indicate a
// non-deployable artifact (mobile / desktop). Returns an empty string when
// the project is a normal server/web app.
func detectNodeUnsupported(pkgPath string) string {
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return ""
	}

	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}

	allDeps := mergeMaps(pkg.Dependencies, pkg.DevDependencies)

	if _, ok := allDeps["expo"]; ok {
		return "Expo / React Native mobile project — snaphost deploys server-side apps only."
	}
	if _, ok := allDeps["react-native"]; ok {
		return "React Native mobile project — snaphost deploys server-side apps only."
	}
	if _, ok := allDeps["electron"]; ok {
		return "Electron desktop project — snaphost deploys server-side apps only."
	}

	return ""
}

// detectNodeFramework scans package.json dependencies to identify the framework.
func detectNodeFramework(pkgPath string) string {
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return ""
	}

	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}

	allDeps := mergeMaps(pkg.Dependencies, pkg.DevDependencies)

	if _, ok := allDeps["next"]; ok {
		return "nextjs"
	}
	if _, ok := allDeps["vite"]; ok {
		return "react-vite"
	}
	if _, ok := allDeps["react-scripts"]; ok {
		return "cra"
	}
	if _, ok := allDeps["express"]; ok {
		return "express"
	}

	return ""
}

// detectNodeEntryPoint extracts the main field from package.json.
func detectNodeEntryPoint(pkgPath string) string {
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return ""
	}

	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}

	if pkg.Main != "" {
		return pkg.Main
	}
	return "index.js"
}

// detectPythonFramework scans requirements.txt for known frameworks.
func detectPythonFramework(reqPath string) string {
	data, err := os.ReadFile(reqPath)
	if err != nil {
		return ""
	}
	content := strings.ToLower(string(data))

	if strings.Contains(content, "django") {
		return "django"
	}
	if strings.Contains(content, "flask") {
		return "flask"
	}
	if strings.Contains(content, "fastapi") {
		return "fastapi"
	}
	return ""
}

// mergeMaps combines two string maps; values from b override values in a.
func mergeMaps(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
