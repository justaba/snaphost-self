package templates

import (
	"regexp"
	"strings"
	"testing"
)

func TestPickMajorFromVersionString(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"18", 18},
		{"18.20.0", 18},
		{"16.20", 16},
		{"lts/hydrogen", 0},
		{"", 0},
		{"  20  ", 20},
	}
	for _, c := range cases {
		if got := pickMajorFromVersionString(c.in); got != c.want {
			t.Errorf("pickMajorFromVersionString(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestPickMajorFromEnginesNode(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"16", 16},
		{"16.20.0", 16},
		{">=16", 16},
		{">=16.0.0", 16},
		{"^16.0.0", 16},
		{"~16.20.0", 16},
		{"16 || 18", 16},
		{">=16 || >=18", 16},
		{"not-a-version", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := pickMajorFromEnginesNode(c.in); got != c.want {
			t.Errorf("pickMajorFromEnginesNode(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestClampNodeMajor(t *testing.T) {
	cases := []struct {
		req  int
		want string
	}{
		{16, "16"},
		{18, "18"},
		{20, "20"},
		{22, "22"},
		{14, "16"},
		{17, "16"},
		{21, "20"},
		{24, "22"},
	}
	for _, c := range cases {
		if got := clampNodeMajor(c.req); got != c.want {
			t.Errorf("clampNodeMajor(%d) = %q, want %q", c.req, got, c.want)
		}
	}
}

func TestPickNodeVersion_Precedence(t *testing.T) {
	s := ProjectSignals{
		NvmrcVersion:    "16",
		NodeVersionFile: "22",
		PackageJSON: &PackageJSON{
			Engines:      map[string]string{"node": "20"},
			Dependencies: map[string]string{"react-scripts": "4.0.3"},
		},
	}
	if got := PickNodeVersion(s, craLegacyHeuristic); got != "16" {
		t.Errorf(".nvmrc precedence: got %q, want 16", got)
	}

	s = ProjectSignals{
		NodeVersionFile: "22",
		PackageJSON:     &PackageJSON{Engines: map[string]string{"node": "20"}},
	}
	if got := PickNodeVersion(s, nil); got != "22" {
		t.Errorf(".node-version precedence: got %q, want 22", got)
	}

	s = ProjectSignals{
		PackageJSON: &PackageJSON{Engines: map[string]string{"node": ">=18.0.0"}},
	}
	if got := PickNodeVersion(s, nil); got != "18" {
		t.Errorf("engines.node precedence: got %q, want 18", got)
	}

	s = ProjectSignals{
		PackageJSON: &PackageJSON{Dependencies: map[string]string{"react-scripts": "^4.0.3"}},
	}
	if got := PickNodeVersion(s, craLegacyHeuristic); got != "16" {
		t.Errorf("cra heuristic: got %q, want 16", got)
	}

	s = ProjectSignals{PackageJSON: &PackageJSON{}}
	if got := PickNodeVersion(s, nil); got != "20" {
		t.Errorf("default: got %q, want 20", got)
	}
}

func TestCraLegacyHeuristic(t *testing.T) {
	cases := []struct {
		name, version string
		want          int
	}{
		{"v3", "3.4.4", 16},
		{"v4", "4.0.3", 16},
		{"v4_caret", "^4.0.3", 16},
		{"v5", "5.0.1", 0},
		{"v5_caret", "^5.0.1", 0},
		{"github_ref", "github:facebook/create-react-app#abc", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := ProjectSignals{
				PackageJSON: &PackageJSON{
					Dependencies: map[string]string{"react-scripts": c.version},
				},
			}
			if got := craLegacyHeuristic(s); got != c.want {
				t.Errorf("craLegacyHeuristic(%q) = %d, want %d", c.version, got, c.want)
			}
		})
	}
}

func TestCRAMatcher_AppliesLegacyHeuristic(t *testing.T) {
	var cra Template
	for _, t := range Library {
		if t.ID == "cra" {
			cra = t
			break
		}
	}
	if cra.ID == "" {
		t.Fatal("cra template not found in Library")
	}
	s := ProjectSignals{
		PackageJSON: &PackageJSON{
			Dependencies: map[string]string{"react-scripts": "4.0.3"},
		},
	}
	matched, vars := cra.Match(s)
	if !matched {
		t.Fatal("expected cra to match react-scripts 4.0.3")
	}
	if got := vars["NODE_VERSION"]; got != "16" {
		t.Errorf("NODE_VERSION for legacy CRA: got %q, want 16", got)
	}
}

// The four nginx templates used to write their server block to
// /etc/nginx/templates/default.conf.template and let the image's entrypoint
// render it into /etc/nginx/conf.d at startup. Every container the runtime
// starts has a read-only root filesystem, so that render failed on its first
// write and killed the container before nginx ran — every static site this
// platform could build was undeployable.
//
// The test that stood here asserted the broken construct, by name, in all
// four templates. It is inverted rather than deleted: the property worth
// holding is that a generated Dockerfile produces a container that needs no
// writable root, and the envsubst path is the one concrete way these
// templates got that wrong.
func TestNginxStaticTemplatesNeedNoWritableRoot(t *testing.T) {
	vars := map[string]string{
		"NODE_VERSION": "20",
		"BUILD_CMD":    "npm run build",
		"DIST_DIR":     "dist",
	}
	for _, tmpl := range Library {
		switch tmpl.ID {
		case "angular", "cra", "static-nginx", "vite-react":
		default:
			continue
		}
		t.Run(tmpl.ID, func(t *testing.T) {
			if tmpl.ExposePort != 8080 {
				t.Fatalf("ExposePort = %d, want 8080", tmpl.ExposePort)
			}
			rendered, err := Render(&tmpl, vars)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}

			// The config lands in its final location during the build, and the
			// port is a literal: nothing injects PORT into a user container, so
			// there is no runtime value for envsubst to substitute anyway.
			for _, want := range []string{
				"ENV PORT=8080",
				"listen 8080;",
				"> /etc/nginx/conf.d/default.conf",
				"EXPOSE 8080",
			} {
				if !strings.Contains(rendered, want) {
					t.Fatalf("rendered template missing %q:\n%s", want, rendered)
				}
			}

			// Comment lines are stripped first: the templates explain this very
			// path in a comment, and a comment is not an instruction.
			if strings.Contains(withoutComments(rendered), "/etc/nginx/templates") {
				t.Fatalf("rendered template defers config rendering to startup, which cannot write to a read-only root:\n%s", rendered)
			}

			// Port 80 needs NET_BIND_SERVICE and collides with the probe, which
			// reads EXPOSE.
			for _, forbidden := range []*regexp.Regexp{
				regexp.MustCompile(`(?m)^\s*listen 80;?\s*$`),
				regexp.MustCompile(`(?m)^EXPOSE 80\s*$`),
			} {
				if forbidden.MatchString(rendered) {
					t.Fatalf("rendered template contains forbidden pattern %q:\n%s", forbidden.String(), rendered)
				}
			}
		})
	}
}

// withoutComments drops Dockerfile comment lines so an assertion about what a
// template does is not satisfied, or broken, by what it says about itself.
func withoutComments(dockerfile string) string {
	var kept []string
	for _, line := range strings.Split(dockerfile, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// `npm create vite` writes vite into devDependencies, and every project
// scaffolded that way keeps it there. The matcher looked only at dependencies,
// so it missed essentially every Vite project — which then fell through to the
// LLM, paying for a generated Dockerfile that this template already had, and
// failing outright on an install with no model key.
func TestViteMatchesWhenViteIsADevDependency(t *testing.T) {
	var vite Template
	for _, tmpl := range Library {
		if tmpl.ID == "vite-react" {
			vite = tmpl
			break
		}
	}
	if vite.ID == "" {
		t.Fatal("vite-react template not found in Library")
	}

	cases := map[string]ProjectSignals{
		"devDependencies": {PackageJSON: &PackageJSON{DevDependencies: map[string]string{"vite": "^7.0.0"}}},
		"dependencies":    {PackageJSON: &PackageJSON{Dependencies: map[string]string{"vite": "^7.0.0"}}},
	}
	for name, signals := range cases {
		t.Run(name, func(t *testing.T) {
			if matched, _ := vite.Match(signals); !matched {
				t.Fatalf("vite in %s did not match", name)
			}
		})
	}

	noVite := ProjectSignals{PackageJSON: &PackageJSON{DevDependencies: map[string]string{"rollup": "^4.0.0"}}}
	if matched, _ := vite.Match(noVite); matched {
		t.Fatal("a rollup project matched the Vite template")
	}
}

// The generic static template exists because the library covered four
// bundlers and there are dozens. A Svelte project built by rollup matched
// nothing and went to the LLM, which on an install with no model key means the
// deploy simply fails.
//
// It has to lose to every specific template, and it has to refuse anything
// that is not a static site — building a server and then serving its source
// directory would be worse than not matching at all.
func TestStaticBuildTemplateMatchesUncoveredBundlers(t *testing.T) {
	cases := []struct {
		name string
		pkg  *PackageJSON
		want string
	}{
		{
			name: "svelte and rollup, the case that prompted this",
			pkg: &PackageJSON{
				Scripts:         map[string]string{"build": "rollup -c"},
				Dependencies:    map[string]string{"sirv-cli": "^0.4.4"},
				DevDependencies: map[string]string{"rollup": "^1.20.0", "svelte": "^3.0.0"},
			},
			want: "static-build",
		},
		{
			name: "vue cli",
			pkg: &PackageJSON{
				Scripts:         map[string]string{"build": "vue-cli-service build"},
				DevDependencies: map[string]string{"@vue/cli-service": "^5.0.0"},
			},
			want: "static-build",
		},
		{
			name: "parcel",
			pkg: &PackageJSON{
				Scripts:         map[string]string{"build": "parcel build"},
				DevDependencies: map[string]string{"parcel": "^2.0.0"},
			},
			want: "static-build",
		},
		{
			// Vite has its own template, and it is the better answer: it knows
			// the output directory instead of searching for it.
			name: "vite still goes to its own template",
			pkg: &PackageJSON{
				Scripts:         map[string]string{"build": "vite build"},
				DevDependencies: map[string]string{"vite": "^7.0.0"},
			},
			want: "vite-react",
		},
		{
			name: "next is a server, not a static site",
			pkg: &PackageJSON{
				Scripts:      map[string]string{"build": "next build"},
				Dependencies: map[string]string{"next": "^15.0.0"},
			},
			want: "nextjs",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			match, ok := FindMatch(ProjectSignals{PackageJSON: tc.pkg})
			if !ok {
				t.Fatalf("no template matched, want %s", tc.want)
			}
			if match.Template.ID != tc.want {
				t.Fatalf("matched %s, want %s", match.Template.ID, tc.want)
			}
		})
	}
}

// The things it must not claim. Each would build successfully and then serve
// nothing, which is a worse outcome than falling through to the LLM.
func TestStaticBuildTemplateRefusesNonStaticProjects(t *testing.T) {
	cases := map[string]*PackageJSON{
		"a bundler but no build script": {
			DevDependencies: map[string]string{"rollup": "^4.0.0"},
		},
		"a build script but no bundler": {
			Scripts:         map[string]string{"build": "tsc -p ."},
			DevDependencies: map[string]string{"typescript": "^5.0.0"},
		},
		"sveltekit emits a server": {
			Scripts:         map[string]string{"build": "vite build"},
			DevDependencies: map[string]string{"@sveltejs/kit": "^2.0.0", "vite": "^7.0.0"},
		},
		"nuxt emits a server": {
			Scripts:         map[string]string{"build": "nuxt build"},
			DevDependencies: map[string]string{"nuxt": "^3.0.0", "vite": "^7.0.0"},
		},
	}

	for name, pkg := range cases {
		t.Run(name, func(t *testing.T) {
			match, ok := FindMatch(ProjectSignals{PackageJSON: pkg})
			if ok && match.Template.ID == "static-build" {
				t.Fatal("static-build claimed a project that does not produce a static site")
			}
		})
	}
}

// Same read-only-root property the four named nginx templates carry. It is
// asserted separately because this template was written later and could have
// reintroduced the construct that made all of them undeployable.
func TestStaticBuildTemplateNeedsNoWritableRoot(t *testing.T) {
	var tmpl Template
	for _, candidate := range Library {
		if candidate.ID == "static-build" {
			tmpl = candidate
			break
		}
	}
	if tmpl.ID == "" {
		t.Fatal("static-build template not found in Library")
	}

	rendered, err := Render(&tmpl, map[string]string{"NODE_VERSION": "20", "BUILD_CMD": "npm run build"})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if strings.Contains(withoutComments(rendered), "/etc/nginx/templates") {
		t.Fatalf("rendered template defers config rendering to startup:\n%s", rendered)
	}
	if !strings.Contains(rendered, "> /etc/nginx/conf.d/default.conf") {
		t.Fatalf("rendered template does not write its config at build time:\n%s", rendered)
	}
}

// Vite is a build tool, not an application shape. SvelteKit, Nuxt, Astro and
// SolidStart all build with it, and none of them produces a directory a web
// server can hand out — a SvelteKit project on adapter-vercel writes a server
// bundle to .svelte-kit/output and no dist/ at all.
//
// This was made worse by fixing the devDependencies lookup: before that, a
// SvelteKit project did not match here because vite sits in devDependencies,
// and it went to the LLM. Afterwards it matched, built for several minutes,
// and failed at the COPY with a message about a missing path.
func TestViteTemplateRefusesFrameworksThatEmitAServer(t *testing.T) {
	cases := map[string]*PackageJSON{
		"sveltekit on adapter-vercel, the case that prompted this": {
			Scripts: map[string]string{"build": "vite build"},
			DevDependencies: map[string]string{
				"@sveltejs/kit":            "^2.16.0",
				"@sveltejs/adapter-vercel": "^5.7.2",
				"vite":                     "^6.2.5",
			},
		},
		"nuxt": {
			Scripts:         map[string]string{"build": "nuxt build"},
			DevDependencies: map[string]string{"nuxt": "^3.0.0", "vite": "^7.0.0"},
		},
		"astro": {
			Scripts:         map[string]string{"build": "astro build"},
			DevDependencies: map[string]string{"astro": "^4.0.0", "vite": "^7.0.0"},
		},
	}

	for name, pkg := range cases {
		t.Run(name, func(t *testing.T) {
			match, ok := FindMatch(ProjectSignals{PackageJSON: pkg})
			if ok && (match.Template.ID == "vite-react" || match.Template.ID == "static-build") {
				t.Fatalf("%s matched %s, which serves a directory this project does not produce", name, match.Template.ID)
			}
		})
	}
}

// A plain Vite app still has to match, or the guard above has taken the
// template's whole purpose with it.
func TestViteTemplateStillMatchesAPlainViteApp(t *testing.T) {
	pkg := &PackageJSON{
		Scripts:         map[string]string{"build": "vite build"},
		Dependencies:    map[string]string{"react": "^19.0.0"},
		DevDependencies: map[string]string{"vite": "^7.0.0", "@vitejs/plugin-react": "^5.0.0"},
	}

	match, ok := FindMatch(ProjectSignals{PackageJSON: pkg})
	if !ok || match.Template.ID != "vite-react" {
		t.Fatalf("a plain Vite app matched %v, want vite-react", match)
	}
}

// Both templates that used to name their output directory now locate it. The
// tools they cover all let a project reconfigure it, and a hardcoded path
// fails at the COPY — minutes into the build, with a message about a missing
// directory rather than about the project.
func TestBuildOutputIsLocatedRatherThanAssumed(t *testing.T) {
	vars := map[string]string{"NODE_VERSION": "20", "BUILD_CMD": "npm run build", "DIST_DIR": "dist"}

	for _, id := range []string{"vite-react", "cra", "static-build"} {
		t.Run(id, func(t *testing.T) {
			var tmpl Template
			for _, candidate := range Library {
				if candidate.ID == id {
					tmpl = candidate
					break
				}
			}
			if tmpl.ID == "" {
				t.Fatalf("%s not found in Library", id)
			}

			rendered, err := Render(&tmpl, vars)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if !strings.Contains(rendered, "COPY --from=builder /app/build-output /usr/share/nginx/html") {
				t.Fatalf("%s copies from a fixed path:\n%s", id, rendered)
			}
			if !strings.Contains(rendered, `if [ -f "$candidate/index.html" ]`) {
				t.Fatalf("%s does not search for its output:\n%s", id, rendered)
			}
		})
	}
}
