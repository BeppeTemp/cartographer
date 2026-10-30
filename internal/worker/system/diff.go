package system

import (
	"fmt"
	"strings"
)

// Bounds of a unit's diff (decision 15).
const (
	maxDiffLines   = 400
	maxDiffBytes   = 64 << 10
	maxDiffProduct = 4_000_000
	diffContext    = 2
)

// LineDiff is a deterministic line diff, LCS-based: lines are prefixed "-"
// (removed), "+" (added) or " " (two lines of context), a gap is "...". It is
// bounded at 400 lines and 64 KiB with a visible marker, and for inputs whose
// line counts multiply past 4M it answers a one-line "diff omitted" instead of
// computing. Identical inputs give "".
func LineDiff(previous, current string) string {
	a, b := lines(previous), lines(current)
	if len(a)*len(b) > maxDiffProduct {
		return "diff omitted: group too large to compare"
	}
	// lcs[i][j] = length of the LCS of a[i:] and b[j:].
	lcs := make([][]int32, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int32, len(b)+1)
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
	type op struct{ kind, text string }
	var ops []op
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			ops = append(ops, op{" ", a[i]})
			i++
			j++
		case j < len(b) && (i == len(a) || lcs[i][j+1] > lcs[i+1][j]):
			ops = append(ops, op{"+", b[j]})
			j++
		default:
			ops = append(ops, op{"-", a[i]})
			i++
		}
	}
	show := make([]bool, len(ops))
	changed := false
	for k, o := range ops {
		if o.kind == " " {
			continue
		}
		changed = true
		for d := -diffContext; d <= diffContext; d++ {
			if k+d >= 0 && k+d < len(ops) {
				show[k+d] = true
			}
		}
	}
	if !changed {
		return ""
	}
	var out []string
	size, gap := 0, false
	for k, o := range ops {
		if !show[k] {
			gap = true
			continue
		}
		if gap && len(out) > 0 {
			out = append(out, "...")
		}
		gap = false
		l := o.kind + o.text
		if len(out) >= maxDiffLines || size+len(l)+1 > maxDiffBytes {
			rest := 0
			for _, s := range show[k:] {
				if s {
					rest++
				}
			}
			return strings.Join(out, "\n") + fmt.Sprintf("\n[diff truncated: %d more lines]", rest)
		}
		out = append(out, l)
		size += len(l) + 1
	}
	return strings.Join(out, "\n")
}

func lines(s string) []string {
	s = strings.TrimSuffix(fold(s), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
