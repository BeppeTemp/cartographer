package mcpserver

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
	"github.com/BeppeTemp/cartographer/internal/skillbundle"
)

// notToolFields are snake_case words the served text puts in backticks that
// share a prefix with a tool but are a frontmatter field or a response key.
var notToolFields = map[string]bool{
	"ingest_status": true, "ingested_at": true, "source_kind": true, "source_shape": true,
	"concept_id": true, "concept_types": true, "contradiction_kind": true, "secret_refs": true,
}

// #2146: every tool the served instructions and the served procedures name
// exists in the registry, and one that tools/list hides is said so in the same
// paragraph, so an agent never goes looking for a tool its client cannot call.
func TestServedTextNamesOnlyRegisteredTools(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	prefixes := map[string]bool{}
	for name := range s.Tools() {
		prefixes[strings.SplitN(name, "_", 2)[0]] = true
	}

	texts := map[string]string{"entryInstructions": entryInstructions}
	var procs []contract.ProcedureLine
	for _, p := range skillbundle.Procedures() {
		procs = append(procs, contract.ProcedureLine{Short: p.Short, UseWhen: p.UseWhen})
		texts["note "+p.Short] = p.Note
		root := "bundled/" + p.Skill
		err := fs.WalkDir(skillbundle.FS, root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
				return err
			}
			data, rerr := fs.ReadFile(skillbundle.FS, path)
			texts[path] = string(data)
			return rerr
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{"", "custom", "off"} {
		texts["guide operational "+mode] = contract.GuideOperational(false, procs, mode)
	}
	texts["guide read-only"] = contract.GuideOperational(true, procs, "")

	token := regexp.MustCompile("`([a-z]+(?:_[a-z0-9]+)+)`")
	checked := 0
	for name, text := range texts {
		sep := `\n\s*\n`
		if strings.HasPrefix(name, "guide") {
			sep = `\n- ` // a guide bullet is its own paragraph
		}
		for _, para := range regexp.MustCompile(sep).Split(text, -1) {
			for _, m := range token.FindAllStringSubmatch(para, -1) {
				tool := m[1]
				if notToolFields[tool] || (!prefixes[strings.SplitN(tool, "_", 2)[0]] && !IsToolName(tool)) {
					continue
				}
				checked++
				// IsToolName is the complete list of what a build can register
				// (a tool needs a resource class); the test KB registers only
				// the server-side set, so sync_check is not in it.
				if !IsToolName(tool) {
					t.Errorf("%s names %q, which is not in the tool registry", name, tool)
					continue
				}
				if ToolAdvanced(tool) && !strings.Contains(para, "tools/list") {
					t.Errorf("%s names %q, hidden from tools/list, in a paragraph that does not say so (%q)", name, tool, para)
				}
			}
		}
	}
	if checked < 30 {
		t.Fatalf("only %d tool names found: the scan no longer matches the served text", checked)
	}
}
