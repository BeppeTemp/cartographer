package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// printApprovalDiff says how the descriptor being approved differs from the
// last approved one: unchanged, a diff of the changed lines, or that no version was recorded.
// Nothing is printed for a first approval. Every line goes through escapeControl.
func printApprovalDiff(w io.Writer, prev *clientconfig.MCPApproval, hash string, content []byte) {
	if prev == nil {
		return
	}
	when := prev.ApprovedAt.Format("2006-01-02")
	if prev.ContentHash == hash {
		fmt.Fprintf(w, "unchanged since approved on %s\n", when)
		return
	}
	if prev.Content == "" {
		fmt.Fprintln(w, "previous approval has no stored version")
		return
	}
	fmt.Fprintf(w, "changed since approved on %s:\n", when)
	for _, l := range lineDiff(prettyJSON([]byte(prev.Content)), prettyJSON(content)) {
		if !strings.HasPrefix(l, " ") {
			fmt.Fprintln(w, escapeControl(l))
		}
	}
}

// prettyJSON indents a descriptor so the diff is per field; text that is not
// JSON is compared as it is.
func prettyJSON(b []byte) []string {
	var out bytes.Buffer
	if json.Indent(&out, bytes.TrimSpace(b), "", "  ") != nil {
		out.Reset()
		out.Write(bytes.TrimSpace(b))
	}
	return strings.Split(out.String(), "\n")
}

// lineDiff is a unified-style diff by longest common subsequence: " " kept,
// "-" removed, "+" added. Descriptors are tiny, so the quadratic table is fine.
func lineDiff(a, b []string) []string {
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, "  "+a[i])
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, "- "+a[i])
			i++
		default:
			out = append(out, "+ "+b[j])
			j++
		}
	}
	for ; i < len(a); i++ {
		out = append(out, "- "+a[i])
	}
	for ; j < len(b); j++ {
		out = append(out, "+ "+b[j])
	}
	return out
}
