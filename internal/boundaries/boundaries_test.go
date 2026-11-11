package boundaries

import (
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modPath = "github.com/BeppeTemp/cartographer/"

// client packages: what the Compass side of the binary links. None may reach
// the data plane, however far down the import chain.
var client = []string{
	"agents", "artifactsig", "blocktext", "client", "clientconfig", "componentproto", "configurator",
	"defaults", "execbit", "feedback", "gitx", "jsonorder", "mcpbridge", "okf", "plugin", "plugin/core",
	"procattr", "provisioning", "provisioning/contract", "repoindex", "safewrite", "secretscan", "settingschema", "skill",
	"skillbundle", "updatecheck",
}

// exempt packages are client-side but reach the data plane today; the list only
// shrinks: a package that becomes clean fails the test until it is removed.
var exempt = map[string]string{
	"service":     "imports config, the server's own file, which imports kb",
	"tray":        "calls service.Manager, so it reaches config",
	"plugin/star": "calls service.StateDir, so it reaches config",
}

// serverSide packages are classified on purpose as not client.
var serverSide = []string{
	"audit", "auth", "config", "kb", "lint", "mcpserver", "search", "sqlindex",
	"sops", "graphalgo", "webui", "changelog", "repodocs", "testkbs", "testtree",
	"kbtest", "openapi", "gitx/gitxtest", "surveyor", "surveyorconfig", "pluginserver", "atlasext",
}

var forbidden = []string{"kb", "mcpserver", "search", "graphalgo", "sqlindex", "lint"}

// chain returns the import chain from pkg to a forbidden package, or nil.
func chain(t *testing.T, pkg string) []string { return chainTo(t, pkg, forbidden) }

// chainTo is chain against an explicit list of forbidden packages.
func chainTo(t *testing.T, pkg string, forbidden []string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}\t{{join .Imports \" \"}}", "./internal/"+pkg)
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %s: %v", pkg, err)
	}
	edges := map[string][]string{}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		p, imps, _ := strings.Cut(l, "\t")
		edges[p] = strings.Fields(imps)
	}
	bad := map[string]bool{}
	for _, f := range forbidden {
		bad[modPath+"internal/"+f] = true
	}
	start := modPath + "internal/" + pkg
	prev := map[string]string{start: ""}
	queue := []string{start}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if bad[p] {
			var c []string
			for ; p != ""; p = prev[p] {
				c = append([]string{strings.TrimPrefix(p, modPath)}, c...)
			}
			return c
		}
		for _, n := range edges[p] {
			if _, seen := prev[n]; !seen {
				prev[n] = p
				queue = append(queue, n)
			}
		}
	}
	return nil
}

func TestClientPackagesLinkNoDataPlane(t *testing.T) {
	checked := 0
	for _, pkg := range client {
		checked++
		if c := chain(t, pkg); c != nil {
			t.Errorf("client package internal/%s reaches the data plane: %s", pkg, strings.Join(c, " -> "))
		}
	}
	if checked == 0 {
		t.Fatal("no package checked")
	}
	for pkg, why := range exempt {
		if chain(t, pkg) == nil {
			t.Errorf("internal/%s is exempt (%s) but is clean: remove it from exempt and add it to client", pkg, why)
		}
	}
}

// A package the binary imports must be classified on purpose.
func TestEveryImportedPackageIsClassified(t *testing.T) {
	known := map[string]bool{}
	for _, p := range client {
		known[p] = true
	}
	for p := range exempt {
		known[p] = true
	}
	for _, p := range serverSide {
		known[p] = true
	}
	dir := filepath.Join("..", "..", "cmd", "cartographer")
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range af.Imports {
			p, _ := strconv.Unquote(im.Path.Value)
			rel, ok := strings.CutPrefix(p, modPath+"internal/")
			if !ok {
				continue
			}
			checked++
			if !known[rel] && !strings.HasPrefix(rel, "surveyor/") {
				t.Errorf("%s imports internal/%s, which is in neither the client, the exempt nor the server-side list of internal/boundaries", filepath.Base(f), rel)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no import checked")
	}
}

// The server links the wire contract of provisioning and the plugin core
// (internal/plugin/core), never the half that writes into other people's home
// directories: provisioning, plugin and mcpbridge (D-new-client-import-firewall,
// D-new-plugin-core). Transitive, like the client firewall: the first hop of the
// chain is named.
func TestServerNeverLinksTheApply(t *testing.T) {
	apply := []string{"provisioning", "plugin", "mcpbridge"}
	for _, pkg := range []string{"mcpserver", "lint", "kb", "config", "audit", "auth", "sqlindex", "search", "surveyor", "surveyorconfig"} {
		if c := chainTo(t, pkg, apply); c != nil {
			t.Errorf("server-side internal/%s reaches the apply half: %s", pkg, strings.Join(c, " -> "))
		}
	}
	if c := chainTo(t, "plugin/core", apply); c != nil {
		t.Errorf("internal/plugin/core reaches the apply half: %s", strings.Join(c, " -> "))
	}
	if c := chainTo(t, "provisioning/contract", []string{"kb", "provisioning", "plugin", "mcpbridge"}); c != nil {
		t.Errorf("internal/provisioning/contract reaches the data plane or the apply half: %s", strings.Join(c, " -> "))
	}
}
