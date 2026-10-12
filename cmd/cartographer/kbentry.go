package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// errNoServerConfig reports that there is no server config file to record a
// kbs[] entry in; callers print the resolved path and the entry to add by hand.
var errNoServerConfig = errors.New("no server config file")

// kbEntryPlan is the preflight of `kb create/clone --artifact-write` (D711).
type kbEntryPlan struct {
	Name       string
	KBPath     string // absolute
	ConfigPath string
	EntryIndex int // -1: no entry yet
}

// planKBEntry resolves the server config (--config, else the standard path),
// loads it and locates the KB's kbs[] entry, writing nothing. A missing file
// wraps errNoServerConfig; an unreadable one or an ambiguous match is refused.
func planKBEntry(name, kbPath, configPath string) (*kbEntryPlan, error) {
	abs, err := filepath.Abs(kbPath)
	if err != nil {
		return nil, err
	}
	if configPath == "" {
		if resolved, err := service.ConfigPath(); err == nil {
			configPath = resolved
		}
	}
	p := &kbEntryPlan{Name: name, KBPath: abs, ConfigPath: configPath, EntryIndex: -1}
	if configPath == "" {
		return p, fmt.Errorf("%w: cannot resolve its path", errNoServerConfig)
	}
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		return p, fmt.Errorf("%w at %s", errNoServerConfig, configPath)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("server config %s cannot be read, refusing to rewrite it: %w", configPath, err)
	}
	if p.EntryIndex, err = findKBEntry(name, abs, configPath, cfg); err != nil {
		return nil, err
	}
	return p, nil
}

// kbEntryYAML is the snippet an operator adds to kbs: by hand.
func kbEntryYAML(kbPath string) string {
	return fmt.Sprintf("kbs:\n  - path: %s\n    allow_artifact_write: true\n", kbPath)
}

// applyKBEntry upserts allow_artifact_write: true for the KB through a
// yaml.Node document (comments, order and unmodelled fields survive), then
// re-loads the result; a file that no longer loads is restored.
func applyKBEntry(p *kbEntryPlan) error {
	orig, err := os.ReadFile(p.ConfigPath)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(orig, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", p.ConfigPath, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: top level is not a mapping", p.ConfigPath)
	}
	root := doc.Content[0]
	str := func(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }
	yes := func() *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"} }

	kbs := mapValue(root, "kbs")
	switch {
	case kbs == nil:
		kbs = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		root.Content = append(root.Content, str("kbs"), kbs)
	case kbs.Kind == yaml.ScalarNode && kbs.Tag == "!!null":
		*kbs = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	case kbs.Kind != yaml.SequenceNode:
		return fmt.Errorf("%s: kbs is not a sequence", p.ConfigPath)
	}

	if p.EntryIndex >= 0 {
		entry, err := kbEntryNode(&doc, p.EntryIndex)
		if err != nil {
			return fmt.Errorf("%s: %w", p.ConfigPath, err)
		}
		if v := mapValue(entry, "allow_artifact_write"); v != nil {
			*v = *yes()
		} else {
			entry.Content = append(entry.Content, str("allow_artifact_write"), yes())
		}
	} else {
		kbs.Content = append(kbs.Content, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
			str("path"), str(p.KBPath), str("allow_artifact_write"), yes(),
		}})
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	if err := os.WriteFile(p.ConfigPath, out, 0o600); err != nil {
		return err
	}
	if _, err := config.Load(p.ConfigPath); err != nil {
		if rerr := os.WriteFile(p.ConfigPath, orig, 0o600); rerr != nil {
			return fmt.Errorf("written config does not load (%v) and could not be restored: %w", err, rerr)
		}
		return fmt.Errorf("written config does not load, original restored: %w", err)
	}
	return nil
}

// preflightArtifactWrite runs before `kb create/clone --artifact-write`
// creates anything: a config that cannot take the entry must stop the command
// while there is still nothing to clean up. It returns a non-zero exit code
// when the command must stop.
func preflightArtifactWrite(name, kbPath, configPath string) (*kbEntryPlan, int) {
	plan, err := planKBEntry(name, kbPath, configPath)
	if err == nil {
		return plan, 0
	}
	abs, _ := filepath.Abs(kbPath)
	fmt.Fprintln(os.Stderr, "Error:", err)
	if errors.Is(err, errNoServerConfig) {
		fmt.Fprintln(os.Stderr, "Nothing was created. Create the server config, pass --config <path>, or drop --artifact-write; to enable artifact writes by hand add:")
		fmt.Fprint(os.Stderr, kbEntryYAML(abs))
	}
	return nil, 1
}

// finishArtifactWrite writes the entry after the KB exists. A failure keeps
// the KB (it mounts as discovered, same stance as D156) and prints the entry
// to add by hand.
func finishArtifactWrite(plan *kbEntryPlan) int {
	if err := applyKBEntry(plan); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		fmt.Fprintln(os.Stderr, "The KB was kept and mounts as discovered (artifact writes off). To enable them add to the server config:")
		fmt.Fprint(os.Stderr, kbEntryYAML(plan.KBPath))
		return 1
	}
	fmt.Printf("kbs[] entry with allow_artifact_write: true written to %s\n", plan.ConfigPath)
	return 0
}
