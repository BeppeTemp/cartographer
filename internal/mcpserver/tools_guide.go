package mcpserver

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/skill"
	"github.com/BeppeTemp/cartographer/internal/skillbundle"
)

// kb_guide (D1100): how to work in a KB, through a tool, because a host with
// no Cartographer client side gives the model only tools. It writes nothing,
// takes no lock and fetches nothing: it returns text embedded in the binary and
// the KB's own files.

// guideSkillDescMax cuts a KB skill's description in the listing.
const guideSkillDescMax = 240

// procedurePreface precedes a procedure body. It says nothing KB-specific.
const procedurePreface = "This procedure was written for an agent that can also run commands; you are reading it through `kb_guide`. " +
	"A step that runs a `cartographer` command, a shell command, a script or a subagent needs a provisioned client or the server's host: skip it and use the tool route the step gives. " +
	"A tool the procedure names that is not in your tool list is not available to you: take the alternative the step gives, or skip the step and tell the user. " +
	"A skill it names that is not in the \"Procedures\" list is not available; a `references/<file>` it cites is read with `kb_guide`, `procedure` and `file`. " +
	"\"The operator\" is the person you are working with: put to them what it asks you to put to the operator.\n\n"

func toolKBGuide(k *kb.KB, deps Deps) Tool {
	return Tool{
		Name: "kb_guide",
		Description: "How to work in this KB: the operating guide, the procedures available and, if you may see them, the KB's own skills. " +
			"Call it first if you were given no instructions. procedure (ingest, conflict-resolve, import) returns its steps; " +
			"file reads a references/ file of that procedure. Read-only.",
		ReadOnly:    true,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"procedure":{"type":"string"},"file":{"type":"string"}}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Procedure string `json:"procedure"`
				File      string `json:"file"`
			}
			if len(args) > 0 {
				if err := json.Unmarshal(args, &params); err != nil {
					return errorResult("invalid params: " + err.Error()), nil
				}
			}
			names := skillbundle.ProcedureNames()
			if strings.TrimSpace(params.Procedure) == "" {
				if params.File != "" {
					return errorResult("file belongs to a procedure: pass procedure too (" + strings.Join(names, ", ") + ")"), nil
				}
				return textResult(renderKBGuide(ctx, k, deps.BundleFS)), nil
			}
			if deps.BundleFS == nil {
				return errorResult("no bundled procedures on this server"), nil
			}
			p, ok := skillbundle.FindProcedure(params.Procedure)
			if !ok {
				return errorResult(fmt.Sprintf("unknown procedure %q; available: %s", params.Procedure, strings.Join(names, ", "))), nil
			}
			if params.File != "" {
				text, ok := p.Reference(deps.BundleFS, params.File)
				if !ok {
					return errorResult(fmt.Sprintf("%q is not a readable file of the %s procedure; %s", params.File, p.Short, referenceList(p.ReferenceFiles(deps.BundleFS)))), nil
				}
				return textResult(text), nil
			}
			body, err := p.Body(deps.BundleFS)
			if err != nil {
				return errorResult(err.Error()), nil
			}
			var sb strings.Builder
			sb.WriteString(procedurePreface)
			if p.Note != "" {
				sb.WriteString(p.Note)
				sb.WriteString("\n\n")
			}
			sb.WriteString(body)
			if refs := p.ReferenceFiles(deps.BundleFS); len(refs) > 0 {
				fmt.Fprintf(&sb, "\n\nReference files of this procedure: %s. Read one with `kb_guide` `procedure: %s` and `file`.\n", strings.Join(refs, ", "), p.Short)
			}
			return textResult(sb.String()), nil
		},
	}
}

func referenceList(refs []string) string {
	if len(refs) == 0 {
		return "it has no reference files"
	}
	return "readable files: " + strings.Join(refs, ", ")
}

// renderKBGuide is the guide of this one KB (D1097's renderer over its
// gatherer), then the procedures and, for a whole-KB reader, the KB's skills.
func renderKBGuide(ctx requestContext, k *kb.KB, bundle fs.FS) string {
	name := kbName(k)
	var sb strings.Builder
	if g, ok := GuideKB(ctx, k, name); ok {
		sb.WriteString(provisioning.RenderGuide([]provisioning.GuideKB{g}, provisioning.GuideOptions{
			Routed:   len(routedSetFrom(ctx)) >= 2,
			ReadOnly: !writesAnyKB(ctx, []string{name}),
		}))
	}
	if bundle != nil {
		var lines []string
		for _, p := range skillbundle.Procedures() {
			if _, err := p.Body(bundle); err != nil {
				continue
			}
			lines = append(lines, fmt.Sprintf("- `%s` — use when %s. Call `kb_guide` with `procedure: %s`.\n", p.Short, p.UseWhen, p.Short))
		}
		if len(lines) > 0 {
			sb.WriteString("\n## Procedures\n\n")
			for _, l := range lines {
				sb.WriteString(l)
			}
		}
	}
	if WholeVisible(ctx, k, false) {
		if lines := kbSkillLines(k); len(lines) > 0 {
			sb.WriteString("\n## This KB's skills\n\n")
			sb.WriteString("Read one with `artifact_read` `path: skills/<name>/SKILL.md`. A step that needs a CLI, a script or a subagent does not apply to a client without them.\n")
			for _, l := range lines {
				sb.WriteString(l)
			}
		}
	}
	return sb.String()
}

// kbSkillLines lists the KB's own skills by directory name, sorted, leaving
// out any that artifact_read would refuse.
func kbSkillLines(k *kb.KB) []string {
	skills, _ := skill.LoadAllSkills(k.Root)
	type row struct{ dir, desc string }
	var rows []row
	for _, s := range skills {
		dir := path.Base(s.DirPath)
		rel := "skills/" + dir + "/SKILL.md"
		if _, err := classifyArtifactPath(rel); err != nil {
			continue
		}
		if rejectArtifactSymlinks(k.Root, rel) != nil {
			continue
		}
		desc := strings.Join(strings.Fields(s.Description), " ")
		if r := []rune(desc); len(r) > guideSkillDescMax {
			desc = string(r[:guideSkillDescMax]) + "…"
		}
		rows = append(rows, row{dir, desc})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].dir < rows[j].dir })
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = fmt.Sprintf("- `%s` — %s\n", r.dir, r.desc)
	}
	return out
}
