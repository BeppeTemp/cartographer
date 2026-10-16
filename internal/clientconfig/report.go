package clientconfig

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// SettingsReport is what a client tells its server about its own settings
// (D818), so the Atlas can show the viewer their own client beside the server
// and the KBs. It is built here, on the client, because only the client knows
// its file and the schema of the version it runs. Entries have the JSON shape
// of the server's own settings entries, so the Atlas reads both alike.
type SettingsReport struct {
	Machine   string        `json:"machine"`
	Version   string        `json:"version"`
	ServerURL string        `json:"server_url"`
	Entries   []ReportEntry `json:"entries"`
	// YAML is the .cartographer.yaml as it is on disk, commented by the schema
	// when Save wrote it; "" when the machine has no file yet.
	YAML string `json:"yaml"`
}

// ReportEntry is one client key with its effective value and origin: "file"
// when .cartographer.yaml sets it, "default" otherwise.
type ReportEntry struct {
	Key         string `json:"key"`
	Value       any    `json:"value"`
	Origin      string `json:"origin"`
	Description string `json:"description"`
	Default     string `json:"default"`
	Type        string `json:"type,omitempty"`
	Values      string `json:"values,omitempty"`
}

// Report reads the client settings in dir. A missing file is no error: every
// key is then at its default.
func Report(dir, version string) (*SettingsReport, error) {
	data, err := os.ReadFile(Path(dir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("clientconfig: read %s: %w", Path(dir), err)
	}
	var doc yaml.Node
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("clientconfig: parse %s: %w", Path(dir), err)
		}
	}
	machine, _ := os.Hostname()
	r := &SettingsReport{Machine: machine, Version: version, YAML: string(data), Entries: []ReportEntry{}}
	for _, k := range Schema.Keys {
		e := ReportEntry{Key: k.Path, Origin: "default", Description: k.Description, Default: k.Default, Type: k.Type, Values: k.Values}
		if n := lookup(&doc, k.Path); n != nil {
			e.Origin = "file"
			_ = n.Decode(&e.Value)
		} else {
			_ = yaml.Unmarshal([]byte(k.Default), &e.Value)
		}
		if k.Path == "server_url" {
			r.ServerURL, _ = e.Value.(string)
		}
		r.Entries = append(r.Entries, e)
	}
	return r, nil
}

// lookup returns the value node at a dotted path in a YAML document, or nil.
func lookup(doc *yaml.Node, path string) *yaml.Node {
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	for _, seg := range strings.Split(path, ".") {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == seg {
				next = n.Content[i+1]
				break
			}
		}
		n = next
	}
	return n
}
