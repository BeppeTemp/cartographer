package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/repodocs"
)

// D1126: the environment-variable table of docs/deployment.md and the
// CARTOGRAPHER_* names the program reads are compared with config.Schema in CI.

const envHeading = "### Environment variables"

// envName matches a whole variable name: it ends in a letter or a digit.
var envName = regexp.MustCompile(`CARTOGRAPHER_[A-Z0-9_]*[A-Z0-9]`)

// nonSchemaEnv holds the table rows that legitimately are not Schema keys.
var nonSchemaEnv = map[string]struct{ flag, reason string }{
	"CARTOGRAPHER_CONFIG":              {"--config", "selects the config file, so it cannot be a key of the file"},
	"CARTOGRAPHER_KB":                  {"--kb", "mounts a folder as it is, no YAML key (D903)"},
	"CARTOGRAPHER_ALLOW_INSECURE_HTTP": {"—", "client variable (D1023)"},
	"CARTOGRAPHER_SERVER_URL":          {"—", "client variable (D64)"},
}

// undocumentedEnvAllow holds names in non-test Go source that no current-state
// page needs to describe.
var undocumentedEnvAllow = map[string]string{
	"CARTOGRAPHER_TOKEN_CI":    "placeholder in the Example value of auth.tokens (internal/config/schema.go): ${NAME} of the operator's own choosing, never read by the program",
	"CARTOGRAPHER_TOKEN_ADMIN": "placeholder in the Example value of auth.tokens (internal/config/schema.go): ${NAME} of the operator's own choosing, never read by the program",
}

func retiredEnv() map[string]bool {
	out := map[string]bool{}
	for _, r := range Retired.Removed {
		if r.Env != "" {
			out[r.Env] = true
		}
	}
	return out
}

// envTableRows parses the variable rows of the table under envHeading into
// variable -> flag cell.
func envTableRows(t *testing.T, root string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "docs", "deployment.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimRight(l, " \t") == envHeading {
			if start >= 0 {
				start = -2
				break
			}
			start = i
		}
	}
	const missing = `docs/deployment.md has no single "### Environment variables" heading: this test reads the table under it (internal/config/envdocs_test.go); if you renamed it, rename it here`
	if start < 0 {
		t.Fatal(missing)
	}
	rows := map[string]string{}
	started := false
	var table []string
	for _, l := range lines[start+1:] {
		if strings.HasPrefix(l, "|") {
			started = true
			table = append(table, l)
			continue
		}
		if started {
			break
		}
	}
	if len(table) == 0 {
		t.Fatal(missing)
	}
	for _, l := range table {
		cells := splitRow(l)
		if len(cells) < 2 {
			continue
		}
		first := strings.Trim(cells[0], " ")
		if !strings.HasPrefix(first, "`CARTOGRAPHER_") || !strings.HasSuffix(first, "`") || strings.Count(first, "`") != 2 {
			continue
		}
		name := strings.Trim(first, "`")
		flag := strings.TrimSpace(cells[1])
		if flag != "—" && !regexp.MustCompile("^`--[a-z0-9-]+`$").MatchString(flag) {
			t.Errorf("docs/deployment.md row %s: malformed \"Equivalent flag\" cell %q (one backticked --flag or —)", name, flag)
			continue
		}
		if _, dup := rows[name]; dup {
			t.Errorf("docs/deployment.md lists %s in two rows", name)
		}
		rows[name] = strings.Trim(flag, "`")
	}
	return rows
}

// splitRow splits a markdown table line into cells, honouring \| escapes.
func splitRow(l string) []string {
	l = strings.TrimSpace(l)
	l = strings.TrimPrefix(l, "|")
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(l); i++ {
		if l[i] == '\\' && i+1 < len(l) && l[i+1] == '|' {
			cur.WriteString(`\|`)
			i++
			continue
		}
		if l[i] == '|' {
			cells = append(cells, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(l[i])
	}
	return append(cells, cur.String())
}

func TestEnvironmentTableMatchesSchema(t *testing.T) {
	root, err := repodocs.Root()
	if err != nil {
		t.Fatal(err)
	}
	rows := envTableRows(t, root)
	retired := retiredEnv()
	schemaEnv := map[string]bool{}
	for _, k := range Schema.Keys {
		if k.Env == "" {
			if k.Flag != "" {
				t.Errorf("Schema key %s has flag %s but no Env: give it an Env, the table in docs/deployment.md is keyed by variable (internal/config/envdocs_test.go)", k.Path, k.Flag)
			}
			continue
		}
		schemaEnv[k.Env] = true
		want := k.Flag
		if want == "" {
			want = "—"
		}
		got, ok := rows[k.Env]
		if !ok {
			t.Errorf("%s is a Schema env (internal/config/schema.go) but has no row in docs/deployment.md §Environment variables: add the row, with its flag cell", k.Env)
			continue
		}
		if got != want {
			t.Errorf("docs/deployment.md row %s: flag cell %q, Schema says %q", k.Env, got, want)
		}
	}
	for name, e := range nonSchemaEnv {
		switch {
		case strings.TrimSpace(e.reason) == "":
			t.Errorf("nonSchemaEnv[%s] has no reason", name)
		case schemaEnv[name]:
			t.Errorf("nonSchemaEnv[%s] is now a Schema env: move it out of nonSchemaEnv, the Schema owns it", name)
		}
		got, ok := rows[name]
		if !ok {
			t.Errorf("nonSchemaEnv[%s] has no row in docs/deployment.md §Environment variables: remove the entry, or restore the row", name)
		} else if got != e.flag {
			t.Errorf("docs/deployment.md row %s: flag cell %q, nonSchemaEnv says %q", name, got, e.flag)
		}
	}
	for name := range rows {
		if retired[name] {
			t.Errorf("%s is removed (config.Retired) but has a live row in docs/deployment.md: a removed variable belongs in the removed-notes row", name)
		}
		if _, ok := nonSchemaEnv[name]; !schemaEnv[name] && !ok {
			t.Errorf("%s is in the table but is neither a Schema env nor in nonSchemaEnv (internal/config/envdocs_test.go): if it is a real option add the Schema key, if it is a client/CLI-only variable add it to nonSchemaEnv with the reason", name)
		}
	}
}

// goEnvLiterals returns, for every CARTOGRAPHER_* name found in a string
// literal of non-test Go source (comments are not read), the first file naming it.
func goEnvLiterals(t *testing.T, root string, files []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	fset := token.NewFileSet()
	for _, f := range files {
		if !strings.HasSuffix(f, ".go") || strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(f)), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			for _, m := range envName.FindAllString(s, -1) {
				if _, seen := out[m]; !seen {
					out[m] = f
				}
			}
			return true
		})
	}
	return out
}

func TestEveryEnvLiteralIsDocumented(t *testing.T) {
	root, err := repodocs.Root()
	if err != nil {
		t.Fatal(err)
	}
	files, err := repodocs.TextFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	code := goEnvLiterals(t, root, files)

	docs := map[string]bool{}
	mds, err := repodocs.MarkdownFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range mds {
		if f != "README.md" && !strings.HasPrefix(f, "docs/") {
			continue
		}
		if f == "docs/decisions.md" || strings.HasPrefix(f, "docs/decisions/") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range envName.FindAllString(string(b), -1) {
			docs[m] = true
		}
	}
	if len(docs) == 0 {
		t.Fatal("no CARTOGRAPHER_* name found in README.md and docs/: the doc walk is broken")
	}
	for _, k := range Schema.Keys {
		if k.Env != "" {
			if _, ok := code[k.Env]; !ok {
				t.Fatalf("Schema env %s not found in the string literals of non-test Go source: the code walk is broken", k.Env)
			}
		}
	}

	retired := retiredEnv()
	var names []string
	for n := range code {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if docs[n] || retired[n] {
			continue
		}
		if _, ok := undocumentedEnvAllow[n]; ok {
			continue
		}
		t.Errorf("%s is named in %s but in no current-state doc page: describe it in docs/deployment.md §Environment variables (server) or the page of its feature, or, if it is a placeholder or retired, list it in config.Retired / undocumentedEnvAllow with the reason (internal/config/envdocs_test.go)", n, code[n])
	}
	for n, reason := range undocumentedEnvAllow {
		_, inCode := code[n]
		switch {
		case strings.TrimSpace(reason) == "":
			t.Errorf("undocumentedEnvAllow[%s] has no reason", n)
		case !inCode:
			t.Errorf("undocumentedEnvAllow[%s] is no longer in non-test Go source: remove it", n)
		case docs[n] || retired[n]:
			t.Errorf("undocumentedEnvAllow[%s] is now documented or retired: remove it", n)
		}
	}
}
