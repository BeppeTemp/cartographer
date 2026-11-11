package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx"
)

func TestChangesSince_From(t *testing.T) {
	t.Parallel()
	c := newChangesLinksKB(t)
	admin := authLocalContext()
	writeAs(t, c.s, admin, "ops/first", nil)
	base, err := gitx.HeadSHA(c.k.Root)
	if err != nil {
		t.Fatal(err)
	}
	writeAs(t, c.s, admin, "ops/second", nil)
	writeAs(t, c.s, admin, "other/third", nil)
	head, _ := gitx.HeadSHA(c.k.Root)

	type result struct {
		Since    string `json:"since"`
		Head     string `json:"head"`
		Note     string `json:"note"`
		Concepts []struct {
			ID string `json:"id"`
		} `json:"concepts"`
	}
	call := func(ctxArgs string, narrow bool) (result, string, bool) {
		var ctx context.Context = admin
		if narrow {
			ctx = restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"ops"}}}})
		}
		text, isErr := callJSON(t, c.s, ctx, "changes_since", ctxArgs)
		var r result
		if !isErr {
			if err := json.Unmarshal([]byte(text), &r); err != nil {
				t.Fatal(err)
			}
		}
		return r, text, isErr
	}
	ids := func(r result) string {
		var out []string
		for _, con := range r.Concepts {
			out = append(out, con.ID)
		}
		return strings.Join(out, ",")
	}

	r, text, isErr := call(fmt.Sprintf(`{"from":%q}`, base), false)
	if isErr || r.Head != head || r.Since == "" || len(r.Concepts) != 2 || strings.Contains(ids(r), "first") {
		t.Fatalf("from base = %+v (err %v): %s", r, isErr, text)
	}

	// A principal that sees only map ops gets only that concept.
	r, _, isErr = call(fmt.Sprintf(`{"from":%q}`, base), true)
	if isErr || ids(r) != "ops/second" {
		t.Fatalf("narrow from base = %q (err %v)", ids(r), isErr)
	}

	// From the head: empty window with a note.
	r, _, isErr = call(fmt.Sprintf(`{"from":%q}`, head), false)
	if isErr || len(r.Concepts) != 0 || r.Note != "no commits after the given revision" {
		t.Fatalf("from head = %+v (err %v)", r, isErr)
	}

	// An annotated tag's object name continues from its commit and never echoes
	// the tag's message.
	if out, err := exec.Command("git", "-C", c.k.Root, "tag", "-a", "-m", "tag message secret", "v1", base).CombinedOutput(); err != nil {
		t.Fatalf("git tag: %v: %s", err, out)
	}
	tagOut, err := exec.Command("git", "-C", c.k.Root, "rev-parse", "v1").Output()
	if err != nil {
		t.Fatal(err)
	}
	r, text, isErr = call(fmt.Sprintf(`{"from":%q}`, strings.TrimSpace(string(tagOut))), false)
	if isErr || len(r.Concepts) != 2 || r.Since == "" || strings.Contains(text, "secret") {
		t.Fatalf("from a tag = %+v (err %v): %s", r, isErr, text)
	}

	for name, args := range map[string]string{
		"both":    fmt.Sprintf(`{"from":%q,"since":"1d"}`, base),
		"unknown": fmt.Sprintf(`{"from":%q}`, strings.Repeat("a", 40)),
		"ref":     `{"from":"HEAD"}`,
		"option":  `{"from":"--all"}`,
		"short":   fmt.Sprintf(`{"from":%q}`, base[:10]),
	} {
		if text, isErr := callJSON(t, c.s, admin, "changes_since", args); !isErr {
			t.Errorf("%s: want a tool error, got %s", name, text)
		} else if name != "both" && !strings.Contains(text, "unknown revision; use since") {
			t.Errorf("%s: error = %s", name, text)
		}
	}
}
