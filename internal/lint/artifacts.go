package lint

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/skill"
)

// Artifact checks (D316): lint covers what a KB ships next to its concepts —
// skills, agents, instructions.md — and the junk files committed with them,
// not only the concepts and hooks. Every check here is deterministic and
// warning or info: the artifact is already in the KB, and the fix is its
// author's.

// Options carries the inputs lint cannot read from the KB itself.
type Options struct {
	// CrossKBRoots maps sibling KB names to their absolute roots. Populated
	// by the server when it mounts more than one KB (kb.KB.SiblingRoots); nil
	// for a single-KB server. checkCrossKBPaths uses it to detect hard-coded
	// cross-KB references in artifacts.
	CrossKBRoots map[string]string

	// Usage is the per-artifact usage the clients reported (D326), keyed by
	// kb.UsageKey. Nil means no client ever reported — a fresh KB, the scan
	// switched off, no supported client — and artifact_unused stays silent:
	// the absence of a signal is not evidence of disuse.
	Usage map[string]kb.UsageSummary
	// UsageStaleDays is the age past which a used artifact is reported as
	// unused; 0 disables the check. Run fills both from the KB.
	UsageStaleDays int
}

// artifactChecks are the KB-level checks of this file: no concept's
// lint_ignore can reach them, so naming one there is reported as invalid.
var artifactChecks = map[string]bool{
	"skill_invalid":           true,
	"skill_warning":           true,
	"legacy_tool_name":        true,
	"skill_broken_ref":        true,
	"skill_git_command":       true,
	"missing_instructions":    true,
	"junk_file":               true,
	"junk_asset":              true,
	"cross_kb_path":           true,
	"skill_missing_perimeter": true,
	"artifact_unused":         true,
}

// missingInstructionsThreshold is the concept count above which a KB with no
// instructions.md is reported: below it sit the demo and freshly-created KBs,
// for which the generated routing sentence is enough. Not configurable.
const missingInstructionsThreshold = 10

// artifactMaxScanBytes bounds the text read from one artifact file: a skill
// may ship a large data file, and a lint pass must not read it into memory.
const artifactMaxScanBytes = 1 << 20

// legacyToolNameRe matches a pre-D288 prefixed tool name ("kb_a__search"): the
// double underscore was the separator, and no current tool name contains one,
// so a single-segment name with an underscore ("concept_read") never matches.
var legacyToolNameRe = regexp.MustCompile(`\b[a-z][a-z0-9_]*__[a-z_]+\b`)

// legacyToolNameExts are the files scanned for legacy tool names: prose and
// scripts an agent reads or runs. Stylesheets and markup are left out because
// a BEM class ("artifacts__list") has exactly the same shape.
var legacyToolNameExts = map[string]bool{
	".md": true, ".txt": true, ".sh": true, ".bash": true, ".zsh": true, ".ps1": true,
	".py": true, ".json": true, ".yaml": true, ".yml": true, ".toml": true,
}

// gitMutationRe matches a git subcommand that writes to a clone. clone, log,
// status and diff are reads and stay silent.
var gitMutationRe = regexp.MustCompile(`\bgit\s+(add|commit|push|pull|merge|rebase|reset|checkout|stash)\b`)

// skillRefRe matches a KB-relative path a skill's code may run or read.
var skillRefRe = regexp.MustCompile(`(?:tools|scripts|skills)/[A-Za-z0-9_.\-/]+`)

// mcpMethods are the MCP JSON-RPC methods skillRefRe would read as a path: a
// skill about an MCP server names them ("do not re-read `tools/list`"), and
// they are protocol, not files.
var mcpMethods = map[string]bool{"tools/list": true, "tools/call": true}

// sopsCommandRe finds a sops invocation; the rest of its pipeline is read by
// hand, because RE2 has no negative lookahead for "without --output-type json".
var sopsCommandRe = regexp.MustCompile(`\bsops\b`)

// sopsDecryptRe tells a decrypting sops call from any other subcommand.
var sopsDecryptRe = regexp.MustCompile(`(?:^|\s)(?:decrypt|-d|--decrypt)(?:\s|$)`)

// sopsJSONOutRe is the flag that makes sops emit JSON.
var sopsJSONOutRe = regexp.MustCompile(`--output-type[=\s]+json\b`)

// jsonConsumerRe is a pipeline stage that only reads JSON.
var jsonConsumerRe = regexp.MustCompile(`^\s*(?:jq\b|python3?\s.*json\.load)`)

// sopsValueFlags are the sops flags that take a value, so the token after
// them is not the file being decrypted.
var sopsValueFlags = map[string]bool{
	"--output-type": true, "--input-type": true, "--extract": true, "--config": true,
}

// kbInstructions is what lint reads from instructions.md's frontmatter.
type kbInstructions struct {
	exists      bool
	perimeter   string
	legacyPaths []legacyPath // longest prefix first
}

// legacyPath is one declared prefix rewrite (WP13).
type legacyPath struct{ from, to string }

// readInstructions reads instructions.md once per lint run. A missing file, or
// one with no frontmatter, declares nothing.
func readInstructions(k *kb.KB) kbInstructions {
	data, err := os.ReadFile(filepath.Join(k.Root, "instructions.md"))
	if err != nil {
		return kbInstructions{}
	}
	out := kbInstructions{exists: true}
	fmRaw, _, hasFM := okf.SplitFrontmatter(string(data))
	if !hasFM {
		return out
	}
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil || fm == nil {
		return out
	}
	if v, ok := fm.Get("perimeter"); ok {
		if s, ok := v.(string); ok {
			out.perimeter = strings.TrimSpace(s)
		}
	}
	if v, ok := fm.Get("legacy_paths"); ok {
		if b, ok := v.(okf.Block); ok {
			out.legacyPaths = parseLegacyPaths(string(b))
		}
	}
	return out
}

// parseLegacyPaths reads the indented `"old/": "new/"` lines of the
// legacy_paths mapping. The order is longest prefix first, so "wiki/ops/"
// is rewritten before "wiki/" could claim the same text.
func parseLegacyPaths(block string) []legacyPath {
	var out []legacyPath
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rest, ok := splitYAMLKey(line)
		if !ok || key == "" {
			continue
		}
		out = append(out, legacyPath{from: key, to: unquote(strings.TrimSpace(rest))})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].from) != len(out[j].from) {
			return len(out[i].from) > len(out[j].from)
		}
		return out[i].from < out[j].from
	})
	return out
}

// splitYAMLKey splits `key: value`, honouring a quoted key that contains a
// colon or a slash.
func splitYAMLKey(line string) (key, value string, ok bool) {
	if line[0] == '"' || line[0] == '\'' {
		end := strings.IndexByte(line[1:], line[0])
		if end < 0 {
			return "", "", false
		}
		key = line[1 : end+1]
		rest := strings.TrimSpace(line[end+2:])
		if !strings.HasPrefix(rest, ":") {
			return "", "", false
		}
		return key, rest[1:], true
	}
	i := strings.Index(line, ":")
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:i]), line[i+1:], true
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// artifactText is one text file of a KB-root artifact, KB-root-relative.
type artifactText struct {
	rel     string
	content string
}

// checkArtifacts runs every artifact check of an unscoped lint. The skills are
// loaded once and handed to each check that needs them: LoadAllSkills walks
// the filesystem.
func checkArtifacts(k *kb.KB, opts Options, instr kbInstructions, conceptCount int, hasSecretsDir bool) []Finding {
	skills, missing := loadSkills(k)
	files := artifactTextFiles(k)

	var findings []Finding
	findings = append(findings, checkSkills(skills, missing)...)
	findings = append(findings, checkLegacyToolNames(files, mountedKBNames(k, opts))...)
	findings = append(findings, checkSkillInternalRefs(k, skills)...)
	findings = append(findings, checkInstructions(instr, conceptCount)...)
	findings = append(findings, checkSkillGitCommands(skills)...)
	for _, s := range skills {
		findings = append(findings, sopsFindings(s.Body, skillFile(s), k.Root, hasSecretsDir)...)
	}
	findings = append(findings, checkCrossKBPaths(files, opts.CrossKBRoots)...)
	findings = append(findings, checkSkillMissingPerimeter(skills, instr.perimeter)...)
	findings = append(findings, checkArtifactUnused(k, skills, opts, time.Now())...)
	for i := range findings {
		if findings[i].Path != "" {
			findings[i].Artifact = true
		}
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Check < findings[j].Check
	})
	return findings
}

func skillFile(s skill.Skill) string { return s.DirPath + "/SKILL.md" }

// loadSkills returns the KB's loadable skills and the skill directories that
// have no SKILL.md, KB-root-relative. A KB with no skills/ has neither.
func loadSkills(k *kb.KB) (skills []skill.Skill, missing []string) {
	entries, err := os.ReadDir(filepath.Join(k.Root, "skills"))
	if err != nil {
		return nil, nil
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || kb.IsJunkPath(e.Name()+"/x") {
			continue
		}
		if _, statErr := os.Stat(filepath.Join(k.Root, "skills", e.Name(), "SKILL.md")); statErr != nil {
			missing = append(missing, "skills/"+e.Name())
		}
	}
	skills, _ = skill.LoadAllSkills(k.Root) // the errors are the missing SKILL.md above
	return skills, missing
}

// checkSkills surfaces skill.Validate in lint (WP1), the same way checkHooks
// surfaces ValidateHookJSON: errors as skill_invalid, warnings as
// skill_warning.
func checkSkills(skills []skill.Skill, missing []string) []Finding {
	var findings []Finding
	for _, dir := range missing {
		findings = append(findings, Finding{
			Path:     dir,
			Check:    "skill_invalid",
			Severity: SevWarning,
			Message:  fmt.Sprintf("skill directory %s has no SKILL.md: no client can catalogue it", dir),
		})
	}
	for i := range skills {
		s := &skills[i]
		for _, issue := range skill.Validate(s) {
			f := Finding{Path: skillFile(*s), Check: "skill_invalid", Severity: SevWarning, Message: issue.Message}
			if issue.Warning {
				f.Check, f.Severity = "skill_warning", SevInfo
			}
			findings = append(findings, f)
		}
	}
	return findings
}

// artifactTextFiles reads the text files of skills/**, agents/*.md and
// instructions.md. Symlinks, junk, binaries and oversized files are skipped.
func artifactTextFiles(k *kb.KB) []artifactText {
	var out []artifactText
	read := func(rel string) {
		abs := filepath.Join(k.Root, filepath.FromSlash(rel))
		fi, err := os.Lstat(abs)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > artifactMaxScanBytes {
			return
		}
		data, err := os.ReadFile(abs)
		if err != nil || !utf8.Valid(data) {
			return
		}
		out = append(out, artifactText{rel: rel, content: string(data)})
	}
	_ = filepath.WalkDir(filepath.Join(k.Root, "skills"), func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(k.Root, abs)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if kb.IsJunkPath(rel+"/x") || (strings.HasPrefix(d.Name(), ".") && rel != "skills") {
				return filepath.SkipDir
			}
			return nil
		}
		if !kb.IsJunkPath(rel) {
			read(rel)
		}
		return nil
	})
	if entries, err := os.ReadDir(filepath.Join(k.Root, "agents")); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				read("agents/" + e.Name())
			}
		}
	}
	read("instructions.md")
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out
}

// mountedKBNames is every KB name this server mounts: the KB itself and its
// siblings.
func mountedKBNames(k *kb.KB, opts Options) []string {
	var names []string
	if k.AuthName != "" {
		names = append(names, k.AuthName)
	}
	for n := range opts.CrossKBRoots {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// checkLegacyToolNames reports every distinct pre-D288 prefixed tool name in
// an artifact file (WP2). The fix strips the prefix; the message names the KB
// the prefix most likely meant — the mounted KB whose name, "-" read as "_",
// equals it — because the call also needs kb: "<name>", which a mechanical
// fix must not invent.
func checkLegacyToolNames(files []artifactText, kbNames []string) []Finding {
	var findings []Finding
	for _, f := range files {
		if !legacyToolNameExts[strings.ToLower(filepath.Ext(f.rel))] {
			continue
		}
		seen := map[string]bool{}
		for _, m := range legacyToolNameRe.FindAllString(f.content, -1) {
			if seen[m] {
				continue
			}
			seen[m] = true
			// mcp__<server>__<tool> is how Claude Code and Codex name any MCP
			// server's tool (mcp__homeassistant__ha_get_state): a client's
			// current name, not a Cartographer prefix. Stripping "mcp__" would
			// break every agent that lists it.
			if strings.HasPrefix(m, "mcp__") {
				continue
			}
			prefix, bare, _ := strings.Cut(m, "__")
			kbArg := "<kb>"
			for _, n := range kbNames {
				if strings.ReplaceAll(n, "-", "_") == prefix {
					kbArg = n
					break
				}
			}
			findings = append(findings, Finding{
				Path:     f.rel,
				Check:    "legacy_tool_name",
				Severity: SevWarning,
				Message:  fmt.Sprintf("references pre-D288 prefixed tool name %q; the prefix no longer exists — use the bare tool name with kb: %q", m, kbArg),
				Fix:      &Fix{Kind: FixStripToolPrefix, Field: m, To: bare},
			})
		}
	}
	return findings
}

// codeSpans returns the text of every fenced code block and every inline code
// span of a Markdown body: the places a skill shows a command to run. Prose is
// left out on purpose — scanning it for paths and commands produced too many
// false positives.
func codeSpans(body string) []string {
	var spans []string
	var fence []string
	inFence := false
	marker := ""
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if inFence {
			if strings.HasPrefix(trimmed, marker) && strings.Trim(trimmed, marker[:1]) == "" {
				spans = append(spans, strings.Join(fence, "\n"))
				fence, inFence = nil, false
				continue
			}
			fence = append(fence, line)
			continue
		}
		if m := fenceMarker(trimmed); m != "" {
			inFence, marker = true, m
			continue
		}
		spans = append(spans, inlineCode(line)...)
	}
	if inFence {
		spans = append(spans, strings.Join(fence, "\n"))
	}
	return spans
}

// fenceMarker returns the run of backticks or tildes that opens a fence.
func fenceMarker(trimmed string) string {
	for _, c := range []string{"`", "~"} {
		n := 0
		for n < len(trimmed) && trimmed[n] == c[0] {
			n++
		}
		if n >= 3 {
			return trimmed[:n]
		}
	}
	return ""
}

// inlineCode returns the inline code spans of one line.
func inlineCode(line string) []string {
	var out []string
	for {
		start := strings.IndexByte(line, '`')
		if start < 0 {
			return out
		}
		n := 1
		for start+n < len(line) && line[start+n] == '`' {
			n++
		}
		delim := line[start : start+n]
		rest := line[start+n:]
		end := strings.Index(rest, delim)
		if end < 0 {
			return out
		}
		out = append(out, rest[:end])
		line = rest[end+n:]
	}
}

// checkSkillInternalRefs reports a path in a skill's code that exists neither
// under the KB root nor under the skill's own directory (WP3): a script a
// skill tells the agent to run and that is not there.
func checkSkillInternalRefs(k *kb.KB, skills []skill.Skill) []Finding {
	var findings []Finding
	for _, s := range skills {
		seen := map[string]bool{}
		for _, span := range codeSpans(s.Body) {
			for _, idx := range skillRefRe.FindAllStringIndex(span, -1) {
				// A match inside a longer path (/opt/tools/x, ./x/scripts/y)
				// names something else.
				if idx[0] > 0 && strings.ContainsAny(span[idx[0]-1:idx[0]], "/.~$-_") {
					continue
				}
				ref := strings.TrimRight(span[idx[0]:idx[1]], ".,:;")
				if seen[ref] || strings.HasSuffix(ref, "/") || mcpMethods[ref] {
					continue
				}
				seen[ref] = true
				if refExists(k.Root, ref) || refExists(filepath.Join(k.Root, filepath.FromSlash(s.DirPath)), ref) {
					continue
				}
				findings = append(findings, Finding{
					Path:     skillFile(s),
					Check:    "skill_broken_ref",
					Severity: SevWarning,
					Message:  fmt.Sprintf("references %s, which exists neither under the KB root nor in the skill's directory", ref),
				})
			}
		}
	}
	return findings
}

func refExists(base, ref string) bool {
	local := filepath.FromSlash(ref)
	if !filepath.IsLocal(local) {
		return true // not a KB path: nothing to say about it
	}
	_, err := os.Stat(filepath.Join(base, local))
	return err == nil
}

// checkInstructions reports a KB past the demo size with no instructions.md
// (WP4): its agents receive the generated routing sentence and nothing else.
func checkInstructions(instr kbInstructions, conceptCount int) []Finding {
	if instr.exists || conceptCount <= missingInstructionsThreshold {
		return nil
	}
	return []Finding{{
		Path:     "",
		Check:    "missing_instructions",
		Severity: SevWarning,
		Message:  fmt.Sprintf("this KB has %d concepts but no instructions.md — agents receive no routing or field rules", conceptCount),
	}}
}

// checkSkillGitCommands reports a skill whose code runs a git mutation (WP6):
// the KB is written only through MCP tools, and an agent's own git in the
// clone is how a KB forks on its remote (D264). Info: the command may target
// another repository, which only the author can tell.
func checkSkillGitCommands(skills []skill.Skill) []Finding {
	var findings []Finding
	for _, s := range skills {
		seen := map[string]bool{}
		for _, span := range codeSpans(s.Body) {
			for _, m := range gitMutationRe.FindAllStringSubmatch(span, -1) {
				if seen[m[1]] {
					continue
				}
				seen[m[1]] = true
				findings = append(findings, Finding{
					Path:     skillFile(s),
					Check:    "skill_git_command",
					Severity: SevInfo,
					Message:  fmt.Sprintf("skill instructs git %s on the KB clone — the KB is written only via MCP tools", m[1]),
				})
			}
		}
	}
	return findings
}

// sopsFindings checks the sops pipelines in body's code (WP9): one piped into
// jq or json.load without --output-type json (sops prints YAML by default),
// and — only when the KB has a secrets/ directory — a cited secrets/ file
// that does not exist. Nothing is decrypted or read.
func sopsFindings(body, path, kbRoot string, hasSecretsDir bool) []Finding {
	if !strings.Contains(body, "sops") {
		return nil
	}
	var findings []Finding
	mismatch := false
	missing := map[string]bool{}
	for _, span := range codeSpans(body) {
		for _, line := range strings.Split(span, "\n") {
			for _, loc := range sopsCommandRe.FindAllStringIndex(line, -1) {
				cmd, after, piped := strings.Cut(line[loc[1]:], "|")
				if !sopsDecryptRe.MatchString(cmd) {
					continue
				}
				if piped && !mismatch && !sopsJSONOutRe.MatchString(cmd) && jsonConsumerRe.MatchString(after) {
					mismatch = true
					findings = append(findings, Finding{
						Path:     path,
						Check:    "sops_format_mismatch",
						Severity: SevWarning,
						Message:  "sops decrypt piped to jq/json.load without --output-type json — the default output is YAML",
					})
				}
				if !hasSecretsDir {
					continue
				}
				if file := sopsFileArg(cmd); file != "" && !missing[file] {
					if _, err := os.Stat(filepath.Join(kbRoot, filepath.FromSlash(file))); err != nil {
						missing[file] = true
						findings = append(findings, Finding{
							Path:     path,
							Check:    "sops_missing_file",
							Severity: SevWarning,
							Message:  fmt.Sprintf("sops decrypt cites %q, which does not exist under secrets/", file),
						})
					}
				}
			}
		}
	}
	return findings
}

// sopsFileArg returns the secrets/ file a sops command decrypts, or "" when it
// names none this check can resolve (a variable, a placeholder, a path
// outside secrets/).
func sopsFileArg(cmd string) string {
	fields := strings.Fields(cmd)
	for i := 0; i < len(fields); i++ {
		f := strings.Trim(fields[i], `"'`)
		if strings.HasPrefix(f, "-") {
			if sopsValueFlags[f] {
				i++
			}
			continue
		}
		if f == "decrypt" {
			continue
		}
		if !strings.HasPrefix(f, "secrets/") || strings.ContainsAny(f, "$<>{}*`") {
			return ""
		}
		return f
	}
	return ""
}

// legacyPathFindings reports, once per declared prefix, a concept body that
// still carries a path instructions.md maps to a new one (WP13). The fix is
// that prefix's rewrite.
func legacyPathFindings(body, path string, mapping []legacyPath) []Finding {
	var findings []Finding
	for _, lp := range mapping {
		if !strings.Contains(body, lp.from) {
			continue
		}
		findings = append(findings, Finding{
			Path:     path,
			Check:    "legacy_path",
			Severity: SevWarning,
			Message:  fmt.Sprintf("body contains legacy path prefix %q — declare the mapping in instructions.md and run kb_repair legacy_path", lp.from),
			Fix:      &Fix{Kind: FixReplacePrefix, Field: lp.from, To: lp.to},
		})
	}
	return findings
}

// checkCrossKBPaths reports an artifact that hard-codes a sibling KB's local
// root (WP10): the path breaks on every other machine and couples one KB to
// another's layout. The KB's own root is machine_path's business, not this.
func checkCrossKBPaths(files []artifactText, siblings map[string]string) []Finding {
	if len(siblings) == 0 {
		return nil
	}
	names := make([]string, 0, len(siblings))
	for n := range siblings {
		names = append(names, n)
	}
	sort.Strings(names)
	const boundary = `(?:[/\\\s"'` + "`" + `)\]]|$)`
	res := make(map[string]*regexp.Regexp, len(names))
	for _, n := range names {
		alts := []string{regexp.QuoteMeta("~/cartographer-data/" + n)}
		if root := filepath.Clean(siblings[n]); root != "" && root != "." && root != "/" {
			alts = append(alts, regexp.QuoteMeta(filepath.ToSlash(root)))
		}
		res[n] = regexp.MustCompile(`(?:` + strings.Join(alts, "|") + `)` + boundary)
	}
	var findings []Finding
	for _, f := range files {
		urlSpans := urlRe.FindAllStringIndex(f.content, -1)
		for _, n := range names {
			for _, span := range res[n].FindAllStringIndex(f.content, -1) {
				if withinAnySpan(span, urlSpans) {
					continue
				}
				findings = append(findings, Finding{
					Path:     f.rel,
					Check:    "cross_kb_path",
					Severity: SevWarning,
					Message:  fmt.Sprintf("references KB %q's local root %s — use a {{repo:…}} or {{path:…}} placeholder, or move this artifact to that KB", n, siblings[n]),
				})
				break // one finding per sibling per file
			}
		}
	}
	return findings
}

// checkSkillMissingPerimeter reports a skill whose description does not name
// the perimeter instructions.md declares (WP11). Case-insensitive substring on
// purpose: "ops" matches "DevOps operations", because for an info finding a
// false negative costs more than a false positive. No perimeter, no finding.
func checkSkillMissingPerimeter(skills []skill.Skill, perimeter string) []Finding {
	if perimeter == "" {
		return nil
	}
	want := strings.ToLower(perimeter)
	var findings []Finding
	for _, s := range skills {
		if strings.Contains(strings.ToLower(s.Description), want) {
			continue
		}
		findings = append(findings, Finding{
			Path:     skillFile(s),
			Check:    "skill_missing_perimeter",
			Severity: SevInfo,
			Message:  fmt.Sprintf("skill description does not mention the KB's perimeter %q — agents on other KBs may activate it by mistake", perimeter),
		})
	}
	return findings
}

// checkJunkFiles reports every junk file git tracks, or would track at the
// next commit, outside the assets the expanded-concept pass already reported
// as junk_asset (WP5).
func checkJunkFiles(k *kb.KB, junkAssets map[string]bool) []Finding {
	files, err := gitx.ListFiles(k.Root)
	if err != nil {
		files = walkFiles(k.Root) // not a git repository: every file would be added
	}
	var findings []Finding
	for _, rel := range files {
		if !kb.IsJunkPath(rel) {
			continue
		}
		if dataRel, ok := strings.CutPrefix(rel, "data/"); ok && junkAssets[dataRel] {
			continue
		}
		findings = append(findings, Finding{
			Path:     rel,
			Check:    "junk_file",
			Severity: SevWarning,
			Message:  "junk file tracked in git; remove it with git rm",
			Artifact: true,
		})
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Path < findings[j].Path })
	return findings
}

// walkFiles lists every regular file under root, slash-relative, outside .git
// and the server's own .cartographer state.
func walkFiles(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".cartographer") {
			return filepath.SkipDir
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if rel, relErr := filepath.Rel(root, abs); relErr == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

// Usage states of a skill or agent, as UsageState reports them.
const (
	UsageNever   = "never"   // no client reported it
	UsageCatalog = "catalog" // only a catalogue load: available, not seen used
	UsageStale   = "stale"   // last activated more than the threshold ago
	UsageActive  = "active"
)

// UsageState classifies one artifact against the usage reports. seen is false
// for an artifact absent from them. kb_status counts by it and
// artifact_unused reports by it, so the two cannot disagree.
func UsageState(u kb.UsageSummary, seen bool, staleDays int, now time.Time) string {
	switch {
	case !seen || u.LastUsed.IsZero():
		return UsageNever
	case u.Count == 0:
		return UsageCatalog
	case int(now.Sub(u.LastUsed).Hours()/24) > staleDays:
		return UsageStale
	}
	return UsageActive
}

// UsageArtifact is a skill or agent the KB ships, as usage tracking sees it.
type UsageArtifact struct {
	Kind, Name string
	// Path is the file findings point at, KB-root-relative.
	Path string
}

// UsageArtifacts lists the KB's skills and agents. Bundled skills are not
// among them: they ship with the binary and are not the KB's to retire.
func UsageArtifacts(k *kb.KB) []UsageArtifact {
	skills, _ := loadSkills(k)
	return usageArtifacts(k, skills)
}

func usageArtifacts(k *kb.KB, skills []skill.Skill) []UsageArtifact {
	var items []UsageArtifact
	for _, s := range skills {
		items = append(items, UsageArtifact{"skill", s.Name, skillFile(s)})
	}
	if entries, err := os.ReadDir(filepath.Join(k.Root, "agents")); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				items = append(items, UsageArtifact{"agent", strings.TrimSuffix(e.Name(), ".md"), "agents/" + e.Name()})
			}
		}
	}
	return items
}

// checkArtifactUnused reports the skills and agents no client has activated
// for opts.UsageStaleDays (D326): a candidate for retirement, or a sign it is
// broken (twelve skills of fifty had never been read when this was measured).
// Info, because the signal is partial by construction — a provider with no
// readable transcript contributes nothing, and a Codex catalogue load proves
// availability, not use — so it never blocks a gate.
func checkArtifactUnused(k *kb.KB, skills []skill.Skill, opts Options, now time.Time) []Finding {
	if opts.Usage == nil || opts.UsageStaleDays <= 0 {
		return nil
	}
	var findings []Finding
	for _, it := range usageArtifacts(k, skills) {
		u, seen := opts.Usage[kb.UsageKey(it.Kind, it.Name)]
		days := int(now.Sub(u.LastUsed).Hours() / 24)
		f := Finding{Path: it.Path, Check: "artifact_unused", Severity: SevInfo}
		switch UsageState(u, seen, opts.UsageStaleDays, now) {
		case UsageNever:
			f.Message = fmt.Sprintf("%s %q has never been activated by any client (no signal in session transcripts)", it.Kind, it.Name)
		case UsageCatalog:
			// Only a catalogue load: the client listed it, nothing shows it was used.
			f.Message = fmt.Sprintf("%s %q has never been seen activated; a client last loaded its catalogue %d days ago (%s)", it.Kind, it.Name, days, u.Provider)
		case UsageStale:
			f.Message = fmt.Sprintf("%s %q was last activated %d days ago (by %s) — consider retiring or updating it", it.Kind, it.Name, days, u.Provider)
		default:
			continue
		}
		findings = append(findings, f)
	}
	return findings
}
