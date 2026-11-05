package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/clientvariant"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// printVariantPreview is what approving a client variant lets happen (D1695).
// The approval is the one security control for where a variant writes, so the
// text lists EVERY destination of the descriptor after the anchors are resolved
// on this machine (global under the home, project relative to a project root),
// plus the detection rules, the MCP mode and what it touches. The approval is
// bound to the descriptor's hash: a descriptor whose paths change has another
// hash and asks again. Everything the KB chose goes through escapeControl.
func printVariantPreview(w io.Writer, a provisioning.Artifact, data []byte) error {
	spec, err := clientvariant.Parse(a.Name, data)
	if err != nil {
		return err
	}
	env := provisioning.VariantEnv() // the environment the fingerprint is taken in
	home := env.Home
	v, reasons := clientvariant.Resolve(spec, env)
	e := escapeControl
	p := func(segs []string) string { return e(filepath.Join(append([]string{home}, segs...)...)) }
	rel := func(segs []string) string { return e(strings.Join(segs, "/")) }
	fmt.Fprintf(w, "Approve client variant %s from KB %s\nhash: %s\n", e(v.Name), e(strings.TrimPrefix(a.Source, "kb:")), shortHash(a.ContentHash))
	fmt.Fprintf(w, "base: %s\nmode: %s\n", e(v.Base), e(modeOrFile(v.MCPMode)))
	fmt.Fprintln(w, "detected by:")
	for _, b := range v.Binaries {
		fmt.Fprintf(w, "  binary %s\n", e(b))
	}
	for _, d := range v.DetectDirs {
		fmt.Fprintf(w, "  directory %s\n", p(d))
	}
	fmt.Fprintln(w, "it will write, on this machine:")
	cell := func(kind string, c *clientvariant.Cell, project bool) {
		if c == nil {
			return
		}
		segs := c.Dir
		if segs == nil {
			segs = c.File
		}
		if project {
			fmt.Fprintf(w, "  project %s: <project>/%s\n", kind, rel(segs))
			return
		}
		fmt.Fprintf(w, "  global  %s: %s\n", kind, p(segs))
	}
	for _, sc := range []struct {
		s       clientvariant.Scope
		project bool
	}{{v.Global, false}, {v.Project, true}} {
		cell("skill", sc.s.Skill, sc.project)
		cell("agent", sc.s.Agent, sc.project)
		cell("instructions", sc.s.Instructions, sc.project)
	}
	if v.MCPFile != nil {
		fmt.Fprintf(w, "  global  mcp: %s\n", p(v.MCPFile))
	}
	if v.HookDir != nil {
		fmt.Fprintf(w, "  global  hook: %s\n  the hook dispatcher is shared: its hooks run under every Cline-family client installed\n", p(v.HookDir))
	}
	if v.SeedTemplate != nil {
		fmt.Fprintf(w, "  seed template: %s\n  derived files removed after a template change: %s\n  servers added through the extension's UI are reset\n", p(v.SeedTemplate), p(v.SeedDerived))
	}
	if v.MCPMode == "cli" {
		fmt.Fprintln(w, "  no MCP entry: the client reaches Cartographer through `cartographer call`")
	}
	for _, r := range reasons {
		fmt.Fprintf(w, "  left out on this machine: %s\n", e(r))
	}
	return nil
}

func modeOrFile(m string) string {
	if m == "" {
		return "file"
	}
	return m
}

// variantPathsMoved reports whether a is a variant approved for exactly these
// bytes whose paths resolve differently in this environment than when it was
// approved (D1695): the approval no longer authorizes it.
func variantPathsMoved(cfg *clientconfig.Config, a provisioning.Artifact) bool {
	if a.Kind != "variant" {
		return false
	}
	ap, ok := cfg.MCPApprovals[strings.TrimPrefix(a.Source, "kb:")][provisioning.ApprovalName(a)]
	return ok && ap.ContentHash == a.ContentHash && ap.PathFingerprint != provisioning.VariantPathFingerprint(a, provisioning.ApplyOptions{})
}
