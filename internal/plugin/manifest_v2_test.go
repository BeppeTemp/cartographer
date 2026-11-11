package plugin

import (
	"strings"
	"testing"
)

const validV2 = `schema: 2
name: team-forks
version: 1.2.3
display_name: Team forks
description: Closed forks of supported clients.
cartographer: ">=0.29.0"
components:
  client:
    sdk: {artifacts: 1}
`

var v2Opts = Options{RunningVersion: "0.29.0", ReservedName: testOpts.ReservedName}

// v2With replaces the components block of validV2.
func v2With(components string) string {
	return validV2[:strings.Index(validV2, "components:")] + components
}

func TestParseManifestV2Valid(t *testing.T) {
	m, probs := ParseManifest([]byte(validV2), v2Opts)
	if len(probs) != 0 {
		t.Fatalf("problems: %v", probs)
	}
	if got := m.Components[SideClient].SDK["artifacts"]; got != 1 || m.Cartographer != ">=0.29.0" {
		t.Errorf("not read: %+v", m)
	}
	full := v2With(`components:
  client:
    sdk: {artifacts: 1, providers: 1}
    permissions:
      kb: read
      net: [api.example.com]
      files: {read: ["{home}/.x/*.json"]}
  surveyor:
    sdk: {recipes: 3}
    permissions: {net: [feed.example.com], env: [EXAMPLE_TOKEN]}
  server:
    sdk: {roles: 1}
  atlas: {}
  hologram:
    anything: [goes]
`)
	m, probs = ParseManifest([]byte(full), v2Opts)
	if len(probs) != 0 {
		t.Fatalf("problems: %v", probs)
	}
	if m.Permissions == nil || m.Permissions.KB != "read" {
		t.Errorf("Manifest.Permissions is not the client side's: %+v", m.Permissions)
	}
	if len(m.Components) != 4 || len(m.UnknownSides) != 1 || m.UnknownSides[0] != "hologram" {
		t.Errorf("sides: %v unknown %v", m.Components, m.UnknownSides)
	}
}

func TestParseManifestV2Problems(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"floor range", strings.Replace(validV2, `">=0.29.0"`, `">=0.29.0 <0.30.0"`, 1), "takes a floor"},
		{"floor exact", strings.Replace(validV2, `">=0.29.0"`, `"0.29.0"`, 1), "takes a floor"},
		{"floor greater-than", strings.Replace(validV2, `">=0.29.0"`, `">0.28.9"`, 1), "takes a floor"},
		{"floor below 0.29", strings.Replace(validV2, `">=0.29.0"`, `">=0.28.0"`, 1), "takes a floor, >=0.29.0 or later"},
		{"floor missing", strings.Replace(validV2, "cartographer: \">=0.29.0\"\n", "", 1), "cartographer: required"},
		{"floor malformed", strings.Replace(validV2, `">=0.29.0"`, `"~0.29"`, 1), "cartographer:"},
		{"no components", v2With(""), "components: required"},
		{"empty components", v2With("components: {}\n"), "components: required"},
		{"top-level permissions", validV2 + "permissions:\n  kb: read\n", `unknown key "permissions"`},
		{"unknown key in side", v2With("components:\n  client:\n    colour: red\n"), `unknown key "colour"`},
		{"level zero", v2With("components:\n  client:\n    sdk: {artifacts: 0}\n"), "components.client.sdk.artifacts"},
		{"level high", v2With("components:\n  client:\n    sdk: {artifacts: 100}\n"), "components.client.sdk.artifacts"},
		{"level text", v2With("components:\n  client:\n    sdk: {artifacts: one}\n"), "components.client"},
		{"family name", v2With("components:\n  client:\n    sdk: {Bad-Name: 1}\n"), "components.client.sdk"},
		{"surveyor kb", v2With("components:\n  surveyor:\n    permissions: {kb: read}\n"), "components.surveyor.permissions.kb: not allowed on the surveyor side"},
		{"surveyor files", v2With("components:\n  surveyor:\n    permissions: {files: {read: [\"{home}/.x\"]}}\n"), "components.surveyor.permissions.files: not allowed"},
		{"server net", v2With("components:\n  server:\n    permissions: {net: [a.example.com]}\n"), "components.server.permissions.net: not allowed on the server side"},
		{"atlas env", v2With("components:\n  atlas:\n    permissions: {env: [X_Y]}\n"), "components.atlas.permissions.env: not allowed"},
		{"surveyor bad host", v2With("components:\n  surveyor:\n    permissions: {net: [\"https://x.example.com\"]}\n"), "components.surveyor.permissions.net[0]"},
		{"client bad env", v2With("components:\n  client:\n    permissions: {env: [PATH]}\n"), "components.client.permissions.env[0]"},
		{"bad name", strings.Replace(validV2, "team-forks", "Team", 1), "name:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, probs := ParseManifest([]byte(c.in), v2Opts)
			if !strings.Contains(strings.Join(probs, "\n"), c.want) {
				t.Errorf("want a problem containing %q, got %v", c.want, probs)
			}
		})
	}
}

// A schema 2 floor is read by a 0.28 binary through the same early check as a
// range: it says what it needs instead of "unsupported schema".
func TestParseManifestV2OnOlderBinary(t *testing.T) {
	_, probs := ParseManifest([]byte(validV2), Options{RunningVersion: "0.28.4"})
	if len(probs) != 1 || probs[0] != "cartographer: needs Cartographer >=0.29.0 (this is 0.28.4)" {
		t.Errorf("got %v", probs)
	}
}

func TestSchema1NormalisedToClientSide(t *testing.T) {
	m, probs := ParseManifest([]byte(validManifest), testOpts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	c := m.Components[SideClient]
	if len(m.Components) != 1 || c.SDK["artifacts"] != 1 || c.SDK["providers"] != 1 || c.SDK["scripts"] != 1 {
		t.Errorf("schema 1 is not a client-only plugin at the implicit levels: %+v", m.Components)
	}
}

func TestFamiliesAreDisjointAndSupported(t *testing.T) {
	seen := map[string]string{}
	for _, f := range Families() {
		for _, n := range append(append([]string{}, f.Dirs...), f.Files...) {
			if o, dup := seen[n]; dup {
				t.Errorf("%q claimed by %s and %s/%s", n, o, f.Side, f.Name)
			}
			seen[n] = string(f.Side) + "/" + f.Name
		}
	}
	for _, c := range []struct {
		side   Side
		fam    string
		level  int
		reason string
	}{
		{SideClient, "artifacts", 1, ""},
		{SideClient, "artifacts", 2, "needs sdk artifacts 2 (this build has 1)"},
		{SideClient, "roles", 1, "unknown family roles"},
		{SideServer, "roles", 1, ""},
		{SideSurveyor, "recipes", 1, "unknown family recipes"},
	} {
		if got := Supported(c.side, c.fam, c.level); got != c.reason {
			t.Errorf("Supported(%s,%s,%d) = %q, want %q", c.side, c.fam, c.level, got, c.reason)
		}
	}
}

func TestFloor(t *testing.T) {
	for in, want := range map[string]string{"": ">=0.29.0", "dev": ">=0.29.0", "0.28.3": ">=0.29.0", "v0.29.0": ">=0.29.0", "0.31.2-rc.1": ">=0.31.2"} {
		if got := Floor(in); got != want {
			t.Errorf("Floor(%q) = %q, want %q", in, got, want)
		}
	}
}

// schema2Tree is a client plugin tree under a schema 2 manifest.
func schema2Tree(manifest string, extra ...TreeFile) []TreeFile {
	files := []TreeFile{
		{Path: ManifestFile, Content: []byte(manifest)},
		{Path: "skills/team-example/SKILL.md", Content: []byte("---\nname: team-example\ndescription: Example skill.\n---\n\n# Example\n")},
	}
	return append(files, extra...)
}

func TestReadTreeSchema2(t *testing.T) {
	p, probs := ReadTree(schema2Tree(validV2), v2Opts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	if len(p.Sides) != 1 || p.Sides[0] != SideClient || len(p.Unsupported) != 0 || len(p.Unknown) != 0 {
		t.Errorf("sides %v unsupported %v unknown %v", p.Sides, p.Unsupported, p.Unknown)
	}
}

func TestReadTreeSchema2Declarations(t *testing.T) {
	// The folder is a problem when its family is not declared.
	undeclared := v2With("components:\n  client:\n    sdk: {providers: 1}\n")
	_, probs := ReadTree(schema2Tree(undeclared), v2Opts)
	if len(probs) != 1 || probs[0] != "skills/: declare sdk.artifacts under components.client" {
		t.Errorf("got %v", probs)
	}

	// An sdk entry the build cannot meet makes that side unsupported, not the tree invalid.
	manifest := v2With("components:\n  client:\n    sdk: {artifacts: 2}\n  surveyor:\n    sdk: {recipes: 1}\n  server: {sdk: {roles: 1}}\n  hologram: {}\n")
	p, probs := ReadTree(schema2Tree(manifest, TreeFile{Path: "extras/a.txt", Content: []byte("x")}), v2Opts)
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	if got := p.Unsupported[SideClient]; got != "needs sdk artifacts 2 (this build has 1)" {
		t.Errorf("client: %q", got)
	}
	if got := p.Unsupported[SideSurveyor]; got != "unknown family recipes" {
		t.Errorf("surveyor: %q", got)
	}
	if _, bad := p.Unsupported[SideServer]; bad {
		t.Errorf("server is supported: %v", p.Unsupported)
	}
	if strings.Join(p.Unknown, ",") != "entry extras,side hologram" {
		t.Errorf("unknown: %v", p.Unknown)
	}
	// An unknown entry is stored: it is part of the digest.
	q, _ := ReadTree(schema2Tree(manifest), v2Opts)
	if p.Digest == q.Digest {
		t.Error("the unknown entry is not in the digest")
	}
}

func TestReadTreeUnknownEntryStaysFatalUnderSchema1(t *testing.T) {
	files := []TreeFile{
		{Path: ManifestFile, Content: []byte(validManifest)},
		{Path: "extras/a.txt", Content: []byte("x")},
	}
	_, probs := ReadTree(files, testOpts)
	if len(probs) != 1 || !strings.HasPrefix(probs[0], "unknown entry `extras`") {
		t.Errorf("got %v", probs)
	}
}

func TestReadTreeSchema2FileNamedLikeAFamilyFolder(t *testing.T) {
	for _, name := range []string{"skills", "agents", "hooks", "providers", "presets", "roles"} {
		_, probs := ReadTree(schema2Tree(validV2, TreeFile{Path: name, Content: []byte("x")}), v2Opts)
		if len(probs) != 1 || !strings.HasPrefix(probs[0], `"`+name+`": a file where the `+name+`/ folder`) {
			t.Errorf("%s: got %v", name, probs)
		}
	}
}
