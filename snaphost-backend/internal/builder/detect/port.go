package detect

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PortFromDockerfile parses the first EXPOSE directive of the given
// Dockerfile and returns its port. Returns 0 if the file is missing,
// has no EXPOSE, or the value is not a plain integer (we deliberately
// don't try to resolve build-arg substitutions like `EXPOSE ${PORT}`,
// since that requires running the build context).
func PortFromDockerfile(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(strings.ToUpper(line), "EXPOSE ") {
			continue
		}
		// EXPOSE may declare multiple ports separated by whitespace,
		// optionally with /tcp or /udp suffixes. Take the first numeric one.
		fields := strings.Fields(line)[1:]
		for _, f := range fields {
			if slash := strings.IndexByte(f, '/'); slash >= 0 {
				f = f[:slash]
			}
			if port, err := strconv.Atoi(f); err == nil && port > 0 && port < 65536 {
				return port
			}
		}
	}
	return 0
}

// FixedPortRisk reports whether a user-shipped Dockerfile looks like it serves
// on a hard-coded port. The runtime injects PORT and invokes the container on
// it, so a server bound to a fixed port builds, loads, and starts —
// and then never receives a request. That is the 2026-07-19 incident: an
// nginx image with EXPOSE 80 reached `running` and was billed while every
// request returned UserCodeError.
//
// The signal is deliberately weak: a numeric EXPOSE with no mention of PORT
// anywhere in the Dockerfile. A Dockerfile cannot show what the application
// reads at runtime — a Node server calling process.env.PORT with EXPOSE 3000
// is perfectly fine and trips this check. So the result is a warning, never a
// build failure. Returns the exposed port and whether it looks risky.
func FixedPortRisk(path string) (port int, risky bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	port = PortFromDockerfile(path)
	if port == 0 {
		return 0, false
	}
	// Any mention of PORT outside the EXPOSE line itself — $PORT, ${PORT},
	// ENV PORT=, ARG PORT — means the author knows the variable exists. Stay
	// quiet then: a false warning on a correct Dockerfile costs more trust
	// than a missed one costs, since the liveness probe catches the rest.
	if mentionsPortVariable(string(content)) {
		return port, false
	}
	return port, true
}

// mentionsPortVariable reports whether the Dockerfile refers to PORT anywhere
// other than in an EXPOSE directive.
func mentionsPortVariable(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(strings.ToUpper(trimmed), "EXPOSE ") {
			continue
		}
		if strings.Contains(trimmed, "PORT") {
			return true
		}
	}
	return false
}

// DefaultPortForLanguage returns the conventional listen port for web
// applications written in the given language. Used as a fallback when
// the Dockerfile has no EXPOSE directive. Returns 0 for unknown values
// so the caller can decide on a global default.
func DefaultPortForLanguage(language string) int {
	switch strings.ToLower(language) {
	case "node":
		return 3000
	case "python":
		return 8000
	case "go", "java":
		return 8080
	case "ruby":
		return 4567
	}
	return 0
}

// DetectLanguage runs the same manifest-file checks Detect uses, but
// without the Dockerfile-presence short-circuit. Useful when a project
// has a Dockerfile (so Detect returns Language="unknown") yet we still
// need to guess the language for the port heuristic.
func DetectLanguage(repoDir string) string {
	if _, err := os.Stat(filepath.Join(repoDir, "package.json")); err == nil {
		return "node"
	}
	if _, err := os.Stat(filepath.Join(repoDir, "go.mod")); err == nil {
		return "go"
	}
	if _, err := os.Stat(filepath.Join(repoDir, "requirements.txt")); err == nil {
		return "python"
	}
	if _, err := os.Stat(filepath.Join(repoDir, "pyproject.toml")); err == nil {
		return "python"
	}
	if _, err := os.Stat(filepath.Join(repoDir, "Gemfile")); err == nil {
		return "ruby"
	}
	if _, err := os.Stat(filepath.Join(repoDir, "pom.xml")); err == nil {
		return "java"
	}
	if _, err := os.Stat(filepath.Join(repoDir, "build.gradle")); err == nil {
		return "java"
	}
	return ""
}
