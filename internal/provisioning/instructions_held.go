package provisioning

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/blocktext"
)

// A held KB (ApplyOptions.HeldKBs) is one the server was told to serve and could
// not mount now: its lock entries are carried forward by the client, and its
// region of the instructions block is carried here. The block is rebuilt from the
// instructions artifacts of the manifest, and a held KB has none, so without this
// its region would leave at the first rewrite while its lock entry stayed. The
// region is the bytes already in the block, never rebuilt from anything the client
// lacks (the KB's text is not here), and never run through the placeholder
// expansion: it is the user's block as it stands.

// extractBlock returns the text of the block delimited by begin and end lines in
// content, from its begin line through its end line (no trailing newline), found
// through blocktext's marker lookup. ok is false when the block is absent or
// corrupted (a refusal the write path owns).
func extractBlock(content, begin, end string) (text string, ok bool) {
	start, stop, outcome := blocktext.BlockRange(content, begin, end)
	if outcome != blocktext.Found {
		return "", false
	}
	return strings.TrimRight(content[start:stop], "\n"), true
}

// heldKBSections returns, for each held name, the exact text between and
// including its KB section markers in the instructions block already in the file
// at fullPath. A name with no section there (no file, no block, a corrupted
// block, no section of that name) is not in the map: nothing is invented.
func heldKBSections(fullPath string, names []string) map[string]string {
	out := map[string]string{}
	if len(names) == 0 {
		return out
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return out
	}
	block, ok := extractBlock(string(data), instructionsBlockBegin, instructionsBlockEnd)
	if !ok {
		return out
	}
	for _, name := range names {
		if text, ok := extractBlock(block, kbSectionBegin(name), kbSectionEnd(name)); ok {
			out[name] = text
		}
	}
	return out
}

// heldSectionBody is the text of a held section between its markers, the shape
// the shared-section decision reads for a section of the signed set.
func heldSectionBody(section string) string {
	i := strings.IndexByte(section, '\n')
	j := strings.LastIndexByte(section, '\n')
	if i < 0 || j <= i {
		return ""
	}
	return section[i+1 : j]
}

// heldInstructions reads the held regions for opts, and the names asked for that
// have none on disk (their lock entry is dropped by the client, not held).
func heldInstructions(opts ApplyOptions, destRel string) (sections map[string]string, missing []string) {
	if len(opts.HeldKBs) == 0 {
		return nil, nil
	}
	sections = heldKBSections(filepath.Join(opts.BaseDir, destRel), opts.HeldKBs)
	for _, name := range opts.HeldKBs {
		if _, ok := sections[name]; !ok {
			missing = append(missing, name)
		}
	}
	return sections, missing
}
