package mcpserver

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// A frontmatter number keeps the text it was sent with: no float64 rounding
// (9007199254740993), no trailing-zero loss (1.10), no expansion (1e21).
const numbersFM = `{"ticket": 9007199254740993, "version": 1.10, "big": 1e21}`

func wantNumbers(t *testing.T, raw string) {
	t.Helper()
	for _, want := range []string{"ticket: 9007199254740993\n", "version: 1.10\n", "big: 1e21\n"} {
		if !strings.Contains(raw, want) {
			t.Errorf("missing %q in\n%s", want, raw)
		}
	}
}

func TestFrontmatterNumbersKeepTheirText(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	fm := strings.TrimSuffix(numbersFM, "}") + `, "type": "Note", "title": "T"}`
	text, isErr := callJSON(t, s, adminCtx, "concept_write", `{"id":"notes/num","frontmatter":`+fm+`,"body":"# T\n"}`)
	if isErr {
		t.Fatal(text)
	}
	raw, err := k.ReadRaw("notes/num.md")
	if err != nil {
		t.Fatal(err)
	}
	wantNumbers(t, raw)

	text, isErr = callJSON(t, s, adminCtx, "concept_patch", `{"id":"notes/num","if_match":"`+okf.ContentHash(raw)+`","frontmatter":{"ticket": 9007199254740995, "n": 3}}`)
	if isErr {
		t.Fatal(text)
	}
	raw, _ = k.ReadRaw("notes/num.md")
	if !strings.Contains(raw, "ticket: 9007199254740995\n") || !strings.Contains(raw, "n: 3\n") {
		t.Errorf("patch:\n%s", raw)
	}

	text, isErr = callJSON(t, s, adminCtx, "concept_batch", `{"operations":[{"op":"write","id":"notes/numb","frontmatter":`+fm+`,"body":"# T\n\n[n](num.md)\n"}]}`)
	if isErr {
		t.Fatal(text)
	}
	raw, err = k.ReadRaw("notes/numb.md")
	if err != nil {
		t.Fatal(err)
	}
	wantNumbers(t, raw)
}
