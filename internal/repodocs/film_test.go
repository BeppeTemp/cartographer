package repodocs

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The README film is re-rendered only when the hash of film/inputs.sh changes (D1068). A file the film reaches,
// directly or through another file's imports, that is not in that list would let a change to it break the render
// with no render triggered; a listed path that no longer exists makes film-needed.sh fail on every run.
func TestFilmImportsStayInsideTheHashedInputs(t *testing.T) {
	root := repoRoot(t)
	list := regexp.MustCompile(`(?s)FILM_INPUTS=\((.*?)\)`).FindStringSubmatch(readFile(t, root, "film/inputs.sh"))
	if list == nil {
		t.Fatal("film/inputs.sh: no FILM_INPUTS array")
	}
	inputs := strings.Fields(list[1])
	for _, in := range inputs {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(in))); err != nil {
			t.Errorf("film/inputs.sh lists %s, which does not exist: %v", in, err)
		}
	}

	// Walk every code file under film/ and follow relative imports transitively: each file reached must be
	// an input, and each code file reached is read in turn.
	var queue []string
	err := filepath.WalkDir(filepath.Join(root, "film"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "out" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if !d.IsDir() && isFilmCode(p) {
			rel, _ := filepath.Rel(root, p)
			queue = append(queue, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for len(queue) > 0 {
		file := queue[0]
		queue = queue[1:]
		if seen[file] {
			continue
		}
		seen[file] = true
		for _, spec := range importTargets(readFile(t, root, file)) {
			if !strings.HasPrefix(spec, ".") {
				continue // a package: the lockfile is an input
			}
			target := resolveImport(root, path.Join(path.Dir(file), spec))
			if target == "" {
				t.Errorf("%s imports %s, which resolves to no file", file, spec)
				continue
			}
			if !inFilmInputs(inputs, target) {
				t.Errorf("%s imports %s (%s), outside film/inputs.sh: add it there, or a change to it will not re-render the film", file, spec, target)
			}
			if isFilmCode(target) {
				queue = append(queue, target)
			}
		}
	}
}

// filmNames reads film/films.txt as the scripts do, but a NUL byte anywhere in it is an error, as in render.sh, publish.sh,
// record.mjs and vite.config.ts (strings.Fields would keep it inside a name, bash `read` would cut a line at it).
func filmNames(t *testing.T, root string) []string {
	t.Helper()
	text := readFile(t, root, "film/films.txt")
	if strings.ContainsRune(text, 0) {
		t.Fatal("film/films.txt holds a NUL byte, which is not in a film name")
	}
	return strings.Fields(text)
}

// The films are the lines of film/films.txt, the one list render.sh, record.mjs and the Vite config read. A film's
// page is film/<name>.html, except the launch film's, which is film/index.html (its URL is the server's root and
// its file keeps the name the README embeds). A page nobody lists is never rendered; a listed film with no page fails
// the render on the release candidate, so both are caught here, with no browser.
func TestFilmPagesAreTheFilmList(t *testing.T) {
	root := repoRoot(t)
	var films []string
	seen := map[string]bool{}
	for _, name := range filmNames(t, root) {
		if !regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`).MatchString(name) {
			t.Errorf("film/films.txt: %q is not a film name (lower-case words joined by -)", name)
		}
		if seen[name] {
			t.Errorf("film/films.txt lists %s twice", name)
		}
		seen[name] = true
		films = append(films, name)
	}
	if !seen["launch"] {
		t.Error("film/films.txt does not list launch: the README embeds its film")
	}

	pages := map[string]bool{}
	for _, name := range films {
		page := name + ".html"
		if name == "launch" {
			page = "index.html"
		}
		pages[page] = true
		src := readFile(t, root, "film/"+page)
		if !strings.Contains(src, `id="stage"`) {
			t.Errorf("film/%s has no #stage element, which film/src/kit.ts scales and the films append to", page)
		}
		entry := regexp.MustCompile(`<script type="module" src="/src/([^"]+)"`).FindStringSubmatch(src)
		if entry == nil {
			t.Errorf("film/%s loads no module script from /src", page)
			continue
		}
		if _, err := os.Stat(filepath.Join(root, "film", "src", filepath.FromSlash(entry[1]))); err != nil {
			t.Errorf("film/%s loads /src/%s, which does not exist: %v", page, entry[1], err)
		}
	}
	html, err := filepath.Glob(filepath.Join(root, "film", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range html {
		if name := filepath.Base(h); !pages[name] {
			t.Errorf("film/%s is a page of no film in film/films.txt: list the film there or remove the page", name)
		}
	}

	// The three programs that act on the list read the file instead of repeating it.
	for _, f := range []string{"film/vite.config.ts", "film/render.sh", "film/record.mjs"} {
		if !strings.Contains(readFile(t, root, f), "films.txt") {
			t.Errorf("%s does not read film/films.txt: a film added there would be skipped by it", f)
		}
	}
	// Each reader of the list refuses a NUL byte in it (bash `read` cuts a line there, the others keep it): a script that
	// drops the refusal reads another list than the rest.
	for f, nul := range map[string]string{"film/vite.config.ts": `\u0000`, "film/record.mjs": `\u0000`} {
		if !strings.Contains(readFile(t, root, f), nul) {
			t.Errorf("%s does not refuse a NUL byte in film/films.txt, as the other readers of the list do", f)
		}
	}
	// The shell scripts define their NUL helper with the same literal, so the definition alone must not satisfy the check:
	// pin the calls (outside comment lines) and render.sh's own comparison.
	code := func(f string) string {
		var b strings.Builder
		for _, l := range strings.Split(readFile(t, root, f), "\n") {
			if !strings.HasPrefix(strings.TrimLeft(l, " \t"), "#") {
				b.WriteString(l + "\n")
			}
		}
		return b.String()
	}
	for _, call := range []string{"nonul film/films.txt", "nonul film/max-bytes.txt"} {
		if !strings.Contains(code("film/publish.sh"), call) {
			t.Errorf("film/publish.sh does not call `%s`: a NUL byte there would be read as another list or cap than record.mjs reads", call)
		}
	}
	if !regexp.MustCompile(`tr -d '\\000' < film/films\.txt`).MatchString(code("film/render.sh")) {
		t.Errorf("film/render.sh does not compare film/films.txt with itself minus NUL bytes, as the other readers of the list do")
	}
}

// The grammar of film/max-bytes.txt, the same in record.mjs (the render job), publish.sh (the publish job) and here: a
// line is a key and its bytes split by spaces or tabs, with spaces or tabs around it and a CR at its end dropped; a
// blank line, or one whose first character after the spaces and tabs is #, is skipped. bytes is 1 to 15 digits, no
// leading zero, in the file and in FILM_MAX_BYTES. strings.Fields would take a no-break space or a vertical tab as a
// separator, and a value past int64 would pass in one parser and error in another.
var (
	filmBlankLine = regexp.MustCompile(`^[ \t]*(#|$)`)
	filmCapLine   = regexp.MustCompile(`^[ \t]*([^ \t]+)[ \t]+([1-9][0-9]{0,14})[ \t]*$`)
)

// parseFilmCaps returns the key of each cap line in order, and the first line that is not one. A NUL byte anywhere in the
// text, a comment line included, refuses the whole text, as in record.mjs and publish.sh (bash `read` cuts a line at a NUL
// byte, so only a whole-file rule keeps the three in step); bad is then "NUL".
func parseFilmCaps(text string) (keys []string, bad string, ok bool) {
	if strings.ContainsRune(text, 0) {
		return nil, "NUL", false
	}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if filmBlankLine.MatchString(line) {
			continue
		}
		m := filmCapLine.FindStringSubmatch(line)
		if m == nil {
			return keys, line, false
		}
		keys = append(keys, m[1])
	}
	return keys, "", true
}

func TestFilmCapGrammar(t *testing.T) {
	for _, c := range []struct {
		name, text string
		ok         bool
	}{
		{"space", "default 5", true},
		{"tab and surrounding blanks", " \tdefault\t \t5 \t", true},
		{"CRLF", "default 5\r\n# c\r\n\r\n", true},
		{"comment with a CR or a line separator inside", "#a\rb\u2028c", true},
		{"15 digits", "default 999999999999999", true},
		{"16 digits", "default 1234567890123456", false},
		{"past int64", "default 9223372036854775808", false},
		{"zero", "default 0", false},
		{"leading zero", "default 05", false},
		{"no-break space separator", "default\u00a05", false},
		{"vertical tab separator", "default\v5", false},
		{"form feed separator", "default\f5", false},
		{"CR as separator", "default\r5", false},
		{"two CRs at the end", "default 5\r\r", false},
		{"three fields", "default 5 6", false},
		{"no bytes", "default", false},
		{"sign", "default +5", false},
		{"NUL after the bytes", "default 5\x00junk", false},
		{"a line of NUL alone", "default 5\n\x00\n", false},
		{"NUL in a comment line", "# a\x00b\ndefault 5", false},
		{"NUL in a comment after the cap line", "default 5\n#\x00", false},
	} {
		if _, bad, ok := parseFilmCaps(c.text); ok != c.ok {
			t.Errorf("%s: %q accepted=%v, want %v (first refused line %q)", c.name, c.text, ok, c.ok, bad)
		}
	}
}

// The size cap of each film is film/max-bytes.txt, read by record.mjs (the render job) and publish.sh (the publish job,
// which holds the write token and must not trust the render job's check): one source, so the two cannot disagree.
// A line naming a film nobody lists is a cap that never applies; a missing default leaves a new film uncapped.
func TestFilmCapsAreOneFile(t *testing.T) {
	root := repoRoot(t)
	listed := map[string]bool{}
	for _, name := range filmNames(t, root) {
		listed[name] = true
	}
	keys, bad, ok := parseFilmCaps(readFile(t, root, "film/max-bytes.txt"))
	if !ok {
		t.Fatalf("film/max-bytes.txt: %q is not \"<film> <positive number of bytes, at most 15 digits>\"", bad)
	}
	hasDefault := false
	for _, k := range keys {
		if k == "default" {
			hasDefault = true
		} else if !listed[k] {
			t.Errorf("film/max-bytes.txt caps %s, which film/films.txt does not list", k)
		}
	}
	if !hasDefault {
		t.Error(`film/max-bytes.txt has no "default" line: a film with no line of its own would have no cap`)
	}
	// Each script must read the file itself (a comment naming it is not a read), and parse it with the grammar above.
	reads := map[string]*regexp.Regexp{
		"film/record.mjs": regexp.MustCompile(`readNoNul\("max-bytes\.txt"\)`),
		"film/publish.sh": regexp.MustCompile(`(?m)^done < film/max-bytes\.txt$`),
	}
	for _, f := range []string{"film/record.mjs", "film/publish.sh"} {
		src := readFile(t, root, f)
		code := regexp.MustCompile(`(?m)^[ \t]*(//|#).*$`).ReplaceAllString(src, "")
		if !reads[f].MatchString(code) {
			t.Errorf("%s does not read film/max-bytes.txt: the render and the publish would not share one cap", f)
		}
		if !strings.Contains(code, "[1-9][0-9]{0,14}") || strings.Contains(code, "[1-9][0-9]*") {
			t.Errorf("%s does not use the bytes grammar [1-9][0-9]{0,14} of film/max-bytes.txt and FILM_MAX_BYTES", f)
		}
		if regexp.MustCompile(`1024 \* 1024|\b(8388608|25165824)\b`).MatchString(src) {
			t.Errorf("%s repeats a cap that film/max-bytes.txt holds", f)
		}
	}
}

func isFilmCode(p string) bool {
	switch filepath.Ext(p) {
	case ".ts", ".tsx", ".js", ".mjs":
		return true
	}
	return false
}

// resolveImport maps an import path without its query to the repository file it names, as a bundler would:
// the exact file, then with an extension, then as a directory's index. "" when none exists.
func resolveImport(root, base string) string {
	for _, c := range []string{base, base + ".ts", base + ".tsx", base + ".js", base + ".mjs", base + "/index.ts", base + "/index.js"} {
		if st, err := os.Stat(filepath.Join(root, filepath.FromSlash(c))); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

var (
	// from "x" after any import/export clause, over lines (a clause often spans several); `import x = ` is not TS ESM.
	importFrom = regexp.MustCompile(`\bfrom\s*["']([^"'\n]+)["']`)
	// import "x": the side-effect form, which has no `from`.
	importBare = regexp.MustCompile(`\bimport\s*["']([^"'\n]+)["']`)
	// import("x"): the dynamic form.
	importDynamic = regexp.MustCompile(`\bimport\s*\(\s*["']([^"'\n]+)["']\s*\)`)
)

// importTargets lists the module specifiers a TS/JS source imports (static, side-effect, dynamic, re-export),
// with any ?query (?raw, ?url) removed.
func importTargets(src string) []string {
	var out []string
	for _, re := range []*regexp.Regexp{importFrom, importBare, importDynamic} {
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			spec := m[1]
			if i := strings.IndexAny(spec, "?#"); i >= 0 {
				spec = spec[:i]
			}
			out = append(out, spec)
		}
	}
	return out
}

func TestImportTargets(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want []string
	}{
		"named":      {`import { a } from "../x";`, []string{"../x"}},
		"multi-line": {"import {\n  a,\n  b,\n} from '../x/y'", []string{"../x/y"}},
		"type":       {`import type { T } from "./t"`, []string{"./t"}},
		"re-export":  {"export {\n a\n} from \"./r\"", []string{"./r"}},
		"side":       {`import "./side.css";`, []string{"./side.css"}},
		"dynamic":    {`const m = await import( "./lazy" );`, []string{"./lazy"}},
		"raw":        {`import s from "./a.glsl?raw";`, []string{"./a.glsl"}},
		"url":        {`import u from "../l.svg?url"`, []string{"../l.svg"}},
		"package":    {`import * as THREE from "three";`, []string{"three"}},
		"none":       {`const from = "x"; // import is a word`, nil},
	} {
		got := importTargets(tc.src)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: importTargets = %v, want %v", name, got, tc.want)
		}
	}
}

func TestInFilmInputs(t *testing.T) {
	inputs := []string{"film", "web/src/lib/graph3d", "web/src/lib/palette.ts", "docs/brand/coordinate-pine.svg"}
	for target, want := range map[string]bool{
		"film/src/data":                   true,
		"web/src/lib/graph3d/scene":       true,
		"web/src/lib/palette":             true,
		"docs/brand/coordinate-pine.svg":  true,
		"web/src/lib/graph3d-extra/other": false,
		"web/src/lib/api":                 false,
		"web/src/lib/palette-other":       false,
		"docs/brand/cartographer.css":     false,
		"film-other/main":                 false,
	} {
		if got := inFilmInputs(inputs, target); got != want {
			t.Errorf("inFilmInputs(%q) = %v, want %v", target, got, want)
		}
	}
}

// inFilmInputs reports whether an import target, as written (no extension), is one of the inputs or under one.
func inFilmInputs(inputs []string, target string) bool {
	for _, in := range inputs {
		if target == in || strings.HasPrefix(target, in+"/") || strings.HasPrefix(in, target+".") {
			return true
		}
	}
	return false
}

// The deployment film shows one scene per profile, and a profile is a row of the `Deployment scenarios` table in
// docs/deployment.md. PROFILES in film/src/deployment.ts names the row number and the start of the Scenario cell; a
// renumbered or renamed scenario fails here instead of leaving a film that names a row the page no longer has.
func TestFilmDeploymentProfilesAreScenarioRows(t *testing.T) {
	root := repoRoot(t)
	profiles := regexp.MustCompile(`\{ id: "[a-z0-9-]+", row: (\d+), title: "([^"]+)" \}`).FindAllStringSubmatch(readFile(t, root, "film/src/deployment.ts"), -1)
	if len(profiles) == 0 {
		t.Fatal(`film/src/deployment.ts: no PROFILES line of the form { id: "...", row: N, title: "..." }`)
	}
	// The table: the rows between the "Deployment scenarios" heading and the next heading, `| # | Scenario | ...`.
	doc := readFile(t, root, "docs/deployment.md")
	_, after, found := strings.Cut(doc, "### Deployment scenarios\n")
	if !found {
		t.Fatal("docs/deployment.md has no `### Deployment scenarios` section")
	}
	if i := strings.Index(after, "\n#"); i >= 0 {
		after = after[:i]
	}
	scenario := map[string]string{}
	for _, line := range strings.Split(after, "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 4 {
			continue
		}
		scenario[strings.TrimSpace(cells[1])] = strings.TrimSpace(cells[2])
	}
	for _, m := range profiles {
		cell, ok := scenario[m[1]]
		if !ok {
			t.Errorf("film/src/deployment.ts names scenario row %s, which the Deployment scenarios table of docs/deployment.md does not have", m[1])
			continue
		}
		if cell != m[2] && !strings.HasPrefix(cell, m[2]+":") {
			t.Errorf("film/src/deployment.ts names row %s %q, but the table's Scenario cell is %q: rename the profile or the row", m[1], m[2], cell)
		}
	}
}
