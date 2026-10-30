package skillbundle

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// Procedure is a bundled skill that kb_guide serves as a step-by-step
// procedure to a client with no filesystem (D1100).
type Procedure struct {
	// Short is the name a caller types; Skill is the bundled skill's name.
	Short, Skill string
	// UseWhen is the one-line trigger shown in the procedures list.
	UseWhen string
	// Note is one line for a reader without a shell; empty when the body
	// needs none.
	Note string
}

// served is the only place that knows which bundled skills are served, in the
// order kb_guide lists them. cartographer-kb-create, cartographer-kb-doctor,
// cartographer-kb-ingest-plan, cartographer-kb-source-author, cartographer-ops and cartographer-feedback are
// deliberately absent: they run commands, scripts and subagents, configure the server's host, or (feedback)
// target provisioned clients and build a URL.
var served = []Procedure{
	{
		Short: "ingest", Skill: "cartographer-kb-ingest",
		UseWhen: "one new primary source (document, transcript, thread, ticket, incident timeline, email, web page, dataset) must become KB knowledge, or an IngestUnit to execute",
		Note:    "Without a shell, skip the sha256 of step 1 and give a `locator` instead (the ledger then dedupes on it); for credentials found in a source (step 6), use `secret_set` for them, or leave them out of every page and tell the user.",
	},
	{
		Short: "conflict-resolve", Skill: "cartographer-kb-conflict-resolve",
		UseWhen: "a write is refused because of a git conflict, or `conflicts_list` returns something",
	},
	{
		Short: "import", Skill: "cartographer-kb-import",
		UseWhen: "an existing wiki or folder of notes that is not in the KB's format must be brought into the KB in batches, the user approving the plan first",
		Note:    "The `cartographer import` scaffold of step 3 is a command of the server's host: use the agent-side fallback that follows it, with `concept_batch`. The target KB must already exist (creating one is done on the server's host). Step 1 needs you to be able to read the corpus: if your host gives you no way to, ask the user to provide the files' content.",
	},
}

// Procedures returns a copy of the served list, in listing order.
func Procedures() []Procedure { return append([]Procedure(nil), served...) }

// ProcedureNames returns the short names, in listing order.
func ProcedureNames() []string {
	out := make([]string, len(served))
	for i, p := range served {
		out[i] = p.Short
	}
	return out
}

// FindProcedure resolves a short or full bundled name (trimmed, case-insensitive).
// A bundled skill that is not served, and a pre-D958 name, are not found.
func FindProcedure(name string) (Procedure, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return Procedure{}, false
	}
	for _, p := range served {
		if n == p.Short || n == p.Skill {
			return p, true
		}
	}
	return Procedure{}, false
}

func (p Procedure) dir() string { return path.Join("bundled", p.Skill) }

// Body returns the procedure's SKILL.md without its frontmatter, read from
// fsys (rooted so that bundled/<skill>/SKILL.md exists).
func (p Procedure) Body(fsys fs.FS) (string, error) {
	data, err := fs.ReadFile(fsys, path.Join(p.dir(), "SKILL.md"))
	if err != nil {
		return "", fmt.Errorf("procedure %q is not in this server's bundle", p.Short)
	}
	if _, body, has := okf.SplitFrontmatter(string(data)); has {
		return strings.TrimLeft(body, "\n"), nil
	}
	return string(data), nil
}

// ReferenceFiles lists the files under the procedure's references/, sorted,
// as "references/<name>"; empty when the directory is absent.
func (p Procedure) ReferenceFiles(fsys fs.FS) []string {
	var out []string
	_ = fs.WalkDir(fsys, path.Join(p.dir(), "references"), func(f string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		out = append(out, strings.TrimPrefix(f, p.dir()+"/"))
		return nil
	})
	sort.Strings(out)
	return out
}

// Reference returns a file under the procedure's references/ verbatim. file
// must be canonical slash form, start with references/ and name a regular file;
// anything else is refused with ok false.
func (p Procedure) Reference(fsys fs.FS, file string) (text string, ok bool) {
	if file == "" || file != path.Clean(file) || !fs.ValidPath(file) ||
		strings.ContainsAny(file, "\\\x00") || !strings.HasPrefix(file, "references/") {
		return "", false
	}
	full := path.Join(p.dir(), file)
	info, err := fs.Stat(fsys, full)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	data, err := fs.ReadFile(fsys, full)
	if err != nil {
		return "", false
	}
	return string(data), true
}
