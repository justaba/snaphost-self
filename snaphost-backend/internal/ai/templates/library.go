package templates

import (
	"strings"
)

// ProjectSignals represents the extracted information about a project.
type ProjectSignals struct {
	FileTree        []string
	KeyFiles        map[string]string
	PackageJSON     *PackageJSON
	GoMod           *GoMod
	RequirementsTxt []string
	PyprojectToml   *PyprojectToml

	// Runtime version hints extracted by detector.Enrich. Empty string means
	// the file was absent or unparseable; downstream callers fall back to a
	// per-template default.
	NvmrcVersion      string // contents of .nvmrc (v-prefix stripped)
	NodeVersionFile   string // contents of .node-version (v-prefix stripped)
	PythonVersionFile string // contents of .python-version
}

// PackageJSON represents parsed package.json data.
type PackageJSON struct {
	Name            string            `json:"name"`
	Engines         map[string]string `json:"engines"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Scripts         map[string]string `json:"scripts"`
}

// GoMod represents parsed go.mod data.
type GoMod struct {
	ModulePath string
	GoVersion  string
}

// PyprojectToml represents parsed pyproject.toml data.
type PyprojectToml struct {
	Name          string
	Dependencies  []string
	PythonVersion string
}

// Template represents a Dockerfile template definition.
type Template struct {
	ID          string
	DisplayName string
	FileName    string
	ExposePort  int
	Match       func(input ProjectSignals) (matched bool, vars map[string]string)
}

// supportedNodeMajors are the Node LTS lines we ship Dockerfiles for. Must
// stay sorted ascending — clampNodeMajor relies on that. Append new majors
// here as official node:<N>-alpine images become available.
var supportedNodeMajors = []int{16, 18, 20, 22}

// defaultNodeMajor is the version applied when no signal is available. Kept
// in sync with the current LTS recommendation. Bump cautiously: every existing
// repo without an explicit pin gets this version on next rebuild.
const defaultNodeMajor = 20

// PickNodeVersion is the single source of truth for the NODE_VERSION template
// variable across all Node-based templates. Precedence:
//
//  1. .nvmrc / .node-version (explicit per-repo pin).
//  2. package.json "engines.node" (explicit per-repo pin).
//  3. Template-specific heuristic if provided (e.g. old react-scripts → 16).
//  4. defaultNodeMajor.
//
// Returns a major version string suitable for direct substitution into
// "FROM node:{{NODE_VERSION}}-alpine".
func PickNodeVersion(s ProjectSignals, heuristic func(ProjectSignals) int) string {
	if v := pickMajorFromVersionString(s.NvmrcVersion); v != 0 {
		return clampNodeMajor(v)
	}
	if v := pickMajorFromVersionString(s.NodeVersionFile); v != 0 {
		return clampNodeMajor(v)
	}
	if s.PackageJSON != nil {
		if e, ok := s.PackageJSON.Engines["node"]; ok {
			if v := pickMajorFromEnginesNode(e); v != 0 {
				return clampNodeMajor(v)
			}
		}
	}
	if heuristic != nil {
		if v := heuristic(s); v != 0 {
			return clampNodeMajor(v)
		}
	}
	return itoa(defaultNodeMajor)
}

// pickMajorFromVersionString extracts the leading integer from a version
// string like "16", "16.20.0". Returns 0 if no leading digit (e.g.
// "lts/hydrogen") — caller falls back to the next precedence layer.
func pickMajorFromVersionString(v string) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	end := 0
	for end < len(v) && v[end] >= '0' && v[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	return parseIntOr(v[:end], 0)
}

// pickMajorFromEnginesNode parses the npm-style engines.node range and
// returns the lowest plausible major version that satisfies it.
//
// Supported shapes:
//
//	"16"          -> 16
//	"16.20.0"     -> 16
//	">=16"        -> 16
//	"^16.0.0"     -> 16
//	"~16.20.0"    -> 16
//	"16 || 18"    -> 16  (first satisfying major)
//
// Unparseable input returns 0. We intentionally avoid a full semver dep:
// cost/benefit doesn't justify it for what is essentially "grep the first
// integer". A wrong guess only fails forward to the heuristic or default;
// it never produces an invalid Dockerfile.
func pickMajorFromEnginesNode(spec string) int {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return 0
	}
	if i := strings.Index(spec, "||"); i != -1 {
		spec = spec[:i]
	}
	spec = strings.TrimSpace(spec)
	for _, p := range []string{">=", ">", "<=", "<", "=", "^", "~"} {
		spec = strings.TrimPrefix(spec, p)
	}
	spec = strings.TrimSpace(spec)
	return pickMajorFromVersionString(spec)
}

// clampNodeMajor maps an arbitrary requested major to the closest supported
// major in supportedNodeMajors. Below the lowest supported -> lowest. At or
// above the highest -> highest. Otherwise the largest supported version
// less-or-equal to requested (node 17 -> 16, node 21 -> 20).
func clampNodeMajor(requested int) string {
	lo, hi := supportedNodeMajors[0], supportedNodeMajors[len(supportedNodeMajors)-1]
	if requested < lo {
		return itoa(lo)
	}
	if requested >= hi {
		return itoa(hi)
	}
	best := lo
	for _, m := range supportedNodeMajors {
		if m <= requested {
			best = m
		}
	}
	return itoa(best)
}

func itoa(n int) string {
	if n < 0 {
		return ""
	}
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func parseIntOr(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// craLegacyHeuristic pins Node 16 for old react-scripts (< 5.0.0). Old
// react-scripts pulls in postcss 7.x via postcss-safe-parser, which is
// incompatible with Node 18+'s stricter module resolution
// (ERR_PACKAGE_PATH_NOT_EXPORTED on './lib/tokenize'). react-scripts 5.x
// bundles modern postcss and works on current Node lines.
//
// Returns 0 if the heuristic doesn't apply.
func craLegacyHeuristic(s ProjectSignals) int {
	if s.PackageJSON == nil {
		return 0
	}
	v, ok := s.PackageJSON.Dependencies["react-scripts"]
	if !ok {
		return 0
	}
	v = strings.TrimSpace(v)
	for _, p := range []string{"^", "~", ">=", ">", "<=", "<", "="} {
		v = strings.TrimPrefix(v, p)
	}
	major := pickMajorFromVersionString(v)
	// Intentionally don't treat 0 (unparseable) as "legacy" — values like
	// "github:owner/repo#sha" or "next" must fall through to default.
	if major >= 1 && major <= 4 {
		return 16
	}
	return 0
}

// Library is the registry of all templates.
var Library = []Template{
	{
		ID:          "nextjs",
		DisplayName: "Next.js",
		FileName:    "nextjs.dockerfile.tmpl",
		ExposePort:  3000,
		Match: func(input ProjectSignals) (bool, map[string]string) {
			if input.PackageJSON == nil {
				return false, nil
			}
			if _, ok := input.PackageJSON.Dependencies["next"]; ok {
				vars := map[string]string{
					"NODE_VERSION": PickNodeVersion(input, nil),
					"BUILD_CMD":    "npm run build",
					"START_CMD":    "npm start",
				}
				if cmd, ok := input.PackageJSON.Scripts["build"]; ok {
					vars["BUILD_CMD"] = "npm run " + cmd
				}
				if _, ok := input.PackageJSON.Scripts["start"]; ok {
					_ = ok
				}
				return true, vars
			}
			return false, nil
		},
	},
	{
		ID:          "angular",
		DisplayName: "Angular",
		FileName:    "angular.dockerfile.tmpl",
		ExposePort:  8080,
		Match: func(input ProjectSignals) (bool, map[string]string) {
			if input.PackageJSON == nil {
				return false, nil
			}
			_, hasCore := input.PackageJSON.Dependencies["@angular/core"]
			_, hasCoreDev := input.PackageJSON.DevDependencies["@angular/core"]
			if !hasCore && !hasCoreDev {
				return false, nil
			}
			vars := map[string]string{
				"NODE_VERSION": PickNodeVersion(input, nil),
				"BUILD_CMD":    "npm run build",
			}
			if _, ok := input.PackageJSON.Scripts["build"]; ok {
				vars["BUILD_CMD"] = "npm run build"
			}
			return true, vars
		},
	},
	{
		ID:          "vite-react",
		DisplayName: "Vite + React",
		FileName:    "vite-react.dockerfile.tmpl",
		ExposePort:  8080,
		Match: func(input ProjectSignals) (bool, map[string]string) {
			if input.PackageJSON == nil {
				return false, nil
			}
			// Vite is a build tool, not an application shape. SvelteKit,
			// Nuxt, Astro and SolidStart all build with it and none of them
			// produces a directory of files a web server can hand out — a
			// SvelteKit project on adapter-vercel writes a server bundle to
			// .svelte-kit/output and no dist/ at all. Matching on vite alone
			// claimed those projects and then failed at the COPY, several
			// minutes into a build, with a message about a missing path.
			if hasAnyPackage(input.PackageJSON, serverFrameworks) {
				return false, nil
			}
			// devDependencies too, and that is where it almost always is:
			// `npm create vite` puts vite there, so matching only
			// dependencies missed essentially every Vite project and sent it
			// to the LLM instead — a paid call to generate a Dockerfile this
			// template already had.
			_, inDeps := input.PackageJSON.Dependencies["vite"]
			_, inDevDeps := input.PackageJSON.DevDependencies["vite"]
			if inDeps || inDevDeps {
				return true, map[string]string{
					"NODE_VERSION": PickNodeVersion(input, nil),
					"BUILD_CMD":    "npm run build",
					"DIST_DIR":     "dist",
				}
			}
			return false, nil
		},
	},
	{
		ID:          "cra",
		DisplayName: "Create React App",
		FileName:    "cra.dockerfile.tmpl",
		ExposePort:  8080,
		Match: func(input ProjectSignals) (bool, map[string]string) {
			if input.PackageJSON == nil {
				return false, nil
			}
			if _, ok := input.PackageJSON.Dependencies["react-scripts"]; ok {
				return true, map[string]string{
					"NODE_VERSION": PickNodeVersion(input, craLegacyHeuristic),
					"BUILD_CMD":    "npm run build",
				}
			}
			return false, nil
		},
	},
	{
		ID:          "express",
		DisplayName: "Express.js",
		FileName:    "express.dockerfile.tmpl",
		ExposePort:  3000,
		Match: func(input ProjectSignals) (bool, map[string]string) {
			if input.PackageJSON == nil {
				return false, nil
			}
			if _, ok := input.PackageJSON.Dependencies["express"]; ok {
				return true, map[string]string{
					"NODE_VERSION": PickNodeVersion(input, nil),
					"EXPOSE_PORT":  "3000",
					"ENTRY_POINT":  "index.js",
				}
			}
			return false, nil
		},
	},
	{
		ID:          "fastify",
		DisplayName: "Fastify",
		FileName:    "fastify.dockerfile.tmpl",
		ExposePort:  3000,
		Match: func(input ProjectSignals) (bool, map[string]string) {
			if input.PackageJSON == nil {
				return false, nil
			}
			if _, ok := input.PackageJSON.Dependencies["fastify"]; ok {
				return true, map[string]string{
					"NODE_VERSION": PickNodeVersion(input, nil),
					"EXPOSE_PORT":  "3000",
				}
			}
			return false, nil
		},
	},
	{
		ID:          "fastapi",
		DisplayName: "FastAPI",
		FileName:    "fastapi.dockerfile.tmpl",
		ExposePort:  8000,
		Match: func(input ProjectSignals) (bool, map[string]string) {
			for _, req := range input.RequirementsTxt {
				if strings.Contains(req, "fastapi") {
					return true, map[string]string{
						"PYTHON_VERSION": "3.11",
						"APP_MODULE":     "main:app",
					}
				}
			}
			return false, nil
		},
	},
	{
		ID:          "flask",
		DisplayName: "Flask",
		FileName:    "flask.dockerfile.tmpl",
		ExposePort:  8000,
		Match: func(input ProjectSignals) (bool, map[string]string) {
			for _, req := range input.RequirementsTxt {
				if strings.Contains(req, "flask") {
					return true, map[string]string{
						"PYTHON_VERSION": "3.11",
						"APP_MODULE":     "app",
					}
				}
			}
			return false, nil
		},
	},
	{
		ID:          "django",
		DisplayName: "Django",
		FileName:    "django.dockerfile.tmpl",
		ExposePort:  8000,
		Match: func(input ProjectSignals) (bool, map[string]string) {
			for _, req := range input.RequirementsTxt {
				if strings.Contains(req, "django") {
					return true, map[string]string{
						"PYTHON_VERSION": "3.11",
						"APP_MODULE":     "core",
					}
				}
			}
			return false, nil
		},
	},
	{
		ID:          "go-service",
		DisplayName: "Go Service",
		FileName:    "go-service.dockerfile.tmpl",
		ExposePort:  8080,
		Match: func(input ProjectSignals) (bool, map[string]string) {
			if input.GoMod != nil {
				return true, map[string]string{
					"GO_VERSION": "1.22",
					"CMD_PATH":   ".",
				}
			}
			return false, nil
		},
	},
	// Last of the Node templates on purpose. Every specific one above is a
	// better answer when it matches: this is what catches a project built by a
	// bundler nobody wrote a template for.
	{
		ID:          "static-build",
		DisplayName: "Static site build",
		FileName:    "static-build.dockerfile.tmpl",
		ExposePort:  8080,
		Match: func(input ProjectSignals) (bool, map[string]string) {
			if input.PackageJSON == nil {
				return false, nil
			}
			if _, ok := input.PackageJSON.Scripts["build"]; !ok {
				return false, nil
			}
			// A framework that emits a server is not a static site, and the
			// template would build it and then find nothing to serve. Better to
			// fall through to the LLM, which can at least write a CMD.
			if hasAnyPackage(input.PackageJSON, serverFrameworks) {
				return false, nil
			}
			if !hasAnyPackage(input.PackageJSON, staticBundlers) {
				return false, nil
			}
			return true, map[string]string{
				"NODE_VERSION": PickNodeVersion(input, nil),
				"BUILD_CMD":    "npm run build",
			}
		},
	},
	{
		ID:          "static-nginx",
		DisplayName: "Static Nginx",
		FileName:    "static-nginx.dockerfile.tmpl",
		ExposePort:  8080,
		Match: func(input ProjectSignals) (bool, map[string]string) {
			for _, f := range input.FileTree {
				if f == "index.html" && input.PackageJSON == nil && input.GoMod == nil {
					return true, nil
				}
			}
			return false, nil
		},
	},
}

// staticBundlers are the tools that turn a source tree into files a web
// server can hand out. The list is what decides whether the generic static
// template applies at all: a package.json with a build script proves nothing
// on its own, since a TypeScript backend has one too.
var staticBundlers = []string{
	"rollup",
	"parcel",
	"parcel-bundler",
	"webpack",
	"esbuild",
	"snowpack",
	"vite",
	"@vue/cli-service",
	"gatsby",
	"eleventy",
	"@11ty/eleventy",
}

// serverFrameworks emit something that has to be run, not something that can
// be served from disk. Matching one means the static template is wrong even
// if a bundler is present — these ship a bundler of their own.
var serverFrameworks = []string{
	"next",
	"nuxt",
	"@sveltejs/kit",
	"@sveltejs/adapter-auto",
	"@sveltejs/adapter-node",
	"@sveltejs/adapter-vercel",
	"@sveltejs/adapter-netlify",
	"@sveltejs/adapter-cloudflare",
	"astro",
	"@remix-run/dev",
	"remix",
	"solid-start",
	"@solidjs/start",
	"express",
	"fastify",
	"koa",
	"@nestjs/core",
	"@hapi/hapi",
}

// hasAnyPackage reports whether any of the named packages appears in either
// dependency map. Both are checked because where a package lands is a matter
// of taste for build tools and of necessity for runtime ones, and a matcher
// that reads only one map misses the projects that chose the other.
func hasAnyPackage(pkg *PackageJSON, names []string) bool {
	for _, name := range names {
		if _, ok := pkg.Dependencies[name]; ok {
			return true
		}
		if _, ok := pkg.DevDependencies[name]; ok {
			return true
		}
	}
	return false
}
