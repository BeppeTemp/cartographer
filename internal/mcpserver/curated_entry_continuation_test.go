package mcpserver

import (
	"strings"
	"testing"
)

// removeCuratedEntry drops the whole list item of the entry, not only its first
// line: a wrapped entry used to leave its continuation behind as a stray line.

const srcIndexHead = "---\ntype: Index\ntitle: Src\n---\n"

func TestRemoveCuratedEntry_DropsTheContinuation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, index, want string
	}{
		{
			name:  "wrapped onto a second line",
			index: "- [mover](mover.md) — a long description that\n  wraps onto a second line\n- [friend](friend.md)\n",
			want:  "- [friend](friend.md)\n",
		},
		{
			name:  "nested sub-list without links",
			index: "- [mover](mover.md)\n  - first note\n    - deeper note\n- [friend](friend.md)\n",
			want:  "- [friend](friend.md)\n",
		},
		{
			name:  "nested sub-item citing the same concept",
			index: "- [mover](mover.md)\n  - see [section](mover.md#why)\n- [friend](friend.md)\n",
			want:  "- [friend](friend.md)\n",
		},
		{
			name:  "ordered item",
			index: "1. [mover](mover.md)\n   wrapped\n2. [friend](friend.md)\n",
			want:  "2. [friend](friend.md)\n",
		},
		{
			name:  "tab-indented continuation",
			index: "- [mover](mover.md)\n\twrapped\n- [friend](friend.md)\n",
			want:  "- [friend](friend.md)\n",
		},
		{
			name:  "last entry of the file",
			index: "- [friend](friend.md)\n- [mover](mover.md)\n  wrapped\n",
			want:  "- [friend](friend.md)\n",
		},
		{
			name:  "nested entry: the sibling and the parent stay",
			index: "- Group\n  - [mover](mover.md)\n    wrapped\n  - [friend](friend.md)\n- Next\n",
			want:  "- Group\n  - [friend](friend.md)\n- Next\n",
		},
		{
			name:  "a blank line ends the entry",
			index: "- [mover](mover.md)\n  wrapped\n\n  another paragraph\n- [friend](friend.md)\n",
			want:  "\n  another paragraph\n- [friend](friend.md)\n",
		},
		{
			name:  "a flush-left line ends the entry",
			index: "- [mover](mover.md)\nNot indented, so not a continuation.\n- [friend](friend.md)\n",
			want:  "Not indented, so not a continuation.\n- [friend](friend.md)\n",
		},
		{
			name:  "the next sibling is never taken",
			index: "- [mover](mover.md)\n- [friend](friend.md)\n  wrapped friend\n",
			want:  "- [friend](friend.md)\n  wrapped friend\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			k, _ := archiveFixture(t, "", tc.index)
			removed, kept, err := removeCuratedEntry(k, "src", "src/mover")
			if err != nil {
				t.Fatal(err)
			}
			if !removed || kept != "" {
				t.Fatalf("removed=%v kept=%q, want removed and nothing kept", removed, kept)
			}
			got, _ := k.ReadIndex("src")
			if got != srcIndexHead+tc.want {
				t.Errorf("index after removal:\n%q\nwant:\n%q", got, srcIndexHead+tc.want)
			}
		})
	}
}

// D344: a line that cites several concepts is never edited — and neither is an
// entry whose nested items cite another concept, which would lose that entry.
func TestRemoveCuratedEntry_KeepsWhatCitesOtherConcepts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, index string
	}{
		{"multi-concept line", "- [mover](mover.md) and [friend](friend.md)\n  wrapped note\n"},
		{"nested sub-item citing another concept", "- [mover](mover.md)\n  - [friend](friend.md)\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			k, _ := archiveFixture(t, "", tc.index)
			removed, kept, err := removeCuratedEntry(k, "src", "src/mover")
			if err != nil {
				t.Fatal(err)
			}
			if removed || kept == "" {
				t.Fatalf("removed=%v kept=%q, want nothing removed and the line reported", removed, kept)
			}
			got, _ := k.ReadIndex("src")
			if got != srcIndexHead+tc.index {
				t.Errorf("the index must be untouched:\n%q", got)
			}
		})
	}
}

// Through the tool: concept_archive on a wrapped entry leaves no stray line.
func TestConceptArchive_RemovesAWrappedEntryWhole(t *testing.T) {
	t.Parallel()
	k, s := archiveFixture(t, "", "- [mover](mover.md) — replaced by something that\n  needs two lines to say so\n- [friend](friend.md)\n")
	res := callTool(t, s, "concept_archive", archiveArgs(t, k, ""))
	if res.IsError {
		t.Fatalf("concept_archive: %+v", res.Content)
	}
	idx, _ := k.ReadIndex("src")
	if strings.Contains(idx, "mover") || strings.Contains(idx, "two lines") {
		t.Errorf("the continuation line was left behind:\n%s", idx)
	}
	if !strings.Contains(idx, "friend.md") {
		t.Errorf("the sibling entry was lost:\n%s", idx)
	}
}
