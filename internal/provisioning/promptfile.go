package provisioning

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// resolvePromptFile turns an agent's `providers.<client>.prompt_file` into the
// reference the client's agent file carries instead of the body. ref is ""
// when the agent declares none or the client does not read one (the body is
// used). A refusal (IsRefusal) holds the agent: a global binding has no
// workspace to name, and a path that leaves the workspace through a link is not
// the workspace's file. A target that does not exist yet only warns: the file
// may live on another branch.
//
// A path read from a KB is joined to a workspace only here. Read-only: it
// Lstats and evaluates links, and writes nothing.
func resolvePromptFile(a contract.Artifact, opts ApplyOptions, content []byte) (ref, warn string, err error) {
	if a.Kind != "agent" || !contract.PromptFileCapable(opts.Provider) {
		return "", "", nil
	}
	fmRaw, _, hasFM := okf.SplitFrontmatter(string(content))
	if !hasFM {
		return "", "", nil
	}
	fm, perr := okf.ParseFrontmatter(fmRaw)
	if perr != nil {
		return "", "", nil // the translation reports an unparseable frontmatter
	}
	rel, perr := contract.PromptFileFor(fm, opts.Provider)
	if perr != nil {
		return "", "", fmt.Errorf("%w: prompt_file: %v", ErrInvalidArtifactFile, perr)
	}
	if rel == "" {
		return "", "", nil
	}
	if opts.Scope != contract.ScopeProject {
		return "", "", promptFileRefusal("%q needs a workspace binding: a global binding has no workspace to name", rel)
	}
	destRel := contract.DestDirScoped(a.Kind, a.Name, opts.Provider, opts.Scope)
	if destRel == "" {
		return "", "", nil
	}
	target := filepath.Join(opts.BaseDir, filepath.FromSlash(rel))
	root := opts.BaseDir
	if r, e := filepath.EvalSymlinks(root); e == nil {
		root = r
	}
	// The deepest component that exists decides whether the target is inside the
	// workspace once links are followed; the rest does not exist yet.
	existing := target
	for {
		if _, e := os.Lstat(existing); e == nil {
			break
		} else if !errors.Is(e, os.ErrNotExist) {
			return "", "", promptFileRefusal("%q cannot be read: %v", rel, e)
		}
		next := filepath.Dir(existing)
		if next == existing || len(next) < len(opts.BaseDir) {
			break
		}
		existing = next
	}
	resolved, e := filepath.EvalSymlinks(existing)
	if e != nil || (resolved != root && !within(root, resolved)) {
		return "", "", promptFileRefusal("%q escapes the workspace", rel)
	}
	if existing != target {
		warn = fmt.Sprintf("agent %q: prompt_file %q does not exist in %s", a.Name, rel, opts.BaseDir)
	}
	fromDir := filepath.Dir(filepath.Join(opts.BaseDir, destRel))
	back, e := filepath.Rel(fromDir, target)
	if e != nil {
		return "", "", promptFileRefusal("%q cannot be referenced from %s", rel, fromDir)
	}
	return "file://" + filepath.ToSlash(back), warn, nil
}

func promptFileRefusal(format string, args ...any) error {
	return fmt.Errorf("%w: prompt_file "+format, append([]any{ErrInvalidArtifactFile}, args...)...)
}

// promptFileReason is the refusal without its sentinel, for the text a user reads.
func promptFileReason(err error) string {
	return strings.TrimPrefix(err.Error(), ErrInvalidArtifactFile.Error()+": ")
}

func heldForPromptFile(a contract.Artifact, opts ApplyOptions, err error) string {
	return fmt.Sprintf("agent %q held for %s: %s", a.Name, opts.Provider, promptFileReason(err))
}

// AgentPromptFileIssues names, without writing, what the `prompt_file` of each
// KB agent would do in opts' workspace: the agents sync holds (refusals) and the
// files that do not exist yet. Status prints the same lines Apply reports.
func AgentPromptFileIssues(m contract.Manifest, opts ApplyOptions) []string {
	return promptFileFindings(m, opts, true)
}

// promptFileWarnings is the non-refusal part, for Apply: the refusal is
// reported where the agent is held, so it is not printed twice.
func promptFileWarnings(m contract.Manifest, opts ApplyOptions) []string {
	return promptFileFindings(m, opts, false)
}

func promptFileFindings(m contract.Manifest, opts ApplyOptions, refusals bool) []string {
	if !contract.PromptFileCapable(opts.Provider) {
		return nil
	}
	var out []string
	for _, a := range m.Artifacts {
		if a.Kind != "agent" || contract.DestDirScoped(a.Kind, a.Name, opts.Provider, opts.Scope) == "" || !artifactAuthorized(a, opts) || contract.AgentSkipped(a, opts.Provider) {
			continue
		}
		content, err := singleArtifactContent(a, opts)
		if err != nil {
			continue
		}
		_, warn, err := resolvePromptFile(a, opts, content)
		switch {
		case err != nil:
			if refusals {
				out = append(out, heldForPromptFile(a, opts, err))
			}
		case warn != "":
			out = append(out, warn)
		}
	}
	return out
}
