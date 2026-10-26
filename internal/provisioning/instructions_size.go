package provisioning

import (
	"os"
	"sort"
	"strings"
)

// InstructionsBlockWarnBytes is the size above which `status` and `sync` warn
// that the managed instructions block is big (D1182). The block is paid on
// every model call of every session; 16 KiB is about 4k tokens, the "few k" the
// brief rendering targets, with margin for a skill-rich KB. Not configurable.
const InstructionsBlockWarnBytes = 16 * 1024

// KBBlockSize is the bytes of one KB's region of the managed block.
type KBBlockSize struct {
	KB    string `json:"kb"`
	Bytes int    `json:"bytes"`
}

// InstructionsBlockSize is what MeasureInstructionsBlock found in a steering
// file. The zero value means no complete block.
type InstructionsBlockSize struct {
	// Bytes is the block as written, begin to end marker, markers included.
	Bytes int
	// KBs are the `cartographer:kb:<name>` regions, largest first.
	KBs []KBBlockSize
}

// ApproxTokens is the block's size in tokens, bytes/4 rounded: an estimate, to
// be labelled approximate wherever it is shown.
func (s InstructionsBlockSize) ApproxTokens() int { return (s.Bytes + 2) / 4 }

// Over reports whether the block exceeds InstructionsBlockWarnBytes.
func (s InstructionsBlockSize) Over() bool { return s.Bytes > InstructionsBlockWarnBytes }

// MeasureInstructionsBlock measures the managed block of the steering file at
// path, with the same recognition by prefix as InstructionsBlockMarkers. A
// missing or unreadable file, or a block with no end marker, measures nothing
// and is not an error here: doctor reports a broken block. Read-only.
func MeasureInstructionsBlock(path string) InstructionsBlockSize {
	data, err := os.ReadFile(path)
	if err != nil {
		return InstructionsBlockSize{}
	}
	content := string(data)
	i := strings.Index(content, instructionsBlockBeginPrefix)
	if i < 0 {
		return InstructionsBlockSize{}
	}
	j := strings.Index(content[i:], instructionsBlockEnd)
	if j < 0 {
		return InstructionsBlockSize{}
	}
	block := content[i : i+j+len(instructionsBlockEnd)]
	size := InstructionsBlockSize{Bytes: len(block)}
	rest := block
	for {
		b := strings.Index(rest, "<!-- cartographer:kb:")
		if b < 0 {
			break
		}
		line := rest[b:]
		nl := strings.IndexByte(line, '\n')
		if nl < 0 {
			break
		}
		marker := line[:nl]
		const suffix = ":begin -->"
		if !strings.HasSuffix(marker, suffix) {
			rest = rest[b+len("<!-- cartographer:kb:"):]
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(marker, "<!-- cartographer:kb:"), suffix)
		end := kbSectionEnd(name)
		e := strings.Index(line, end)
		if e < 0 {
			rest = line[nl:]
			continue
		}
		size.KBs = append(size.KBs, KBBlockSize{KB: name, Bytes: e + len(end)})
		rest = line[e+len(end):]
	}
	sort.SliceStable(size.KBs, func(a, b int) bool { return size.KBs[a].Bytes > size.KBs[b].Bytes })
	return size
}
