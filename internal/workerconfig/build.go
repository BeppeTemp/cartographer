package workerconfig

import (
	"fmt"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

// Report is the body a worker posts to /api/worker-settings (D1259): typed
// scalar entries and the perimeter's KBs, never a YAML file. The server
// rebuilds every description from the key table and runs Scrub on receipt.
type Report struct {
	Perimeter string   `json:"perimeter"`
	Worker    string   `json:"worker"`
	Version   string   `json:"version"`
	KBs       []string `json:"kbs"`
	Entries   []Entry  `json:"entries"`
}

// Build is the worker half of the report, made from the bytes of the config it
// loaded at start (the file is the effective configuration, no layering). It
// walks the scalar keys of the table that are not inside a list (list elements
// such as models[] and tasks[] are not reported: a key repeated per element
// has no single value, and that is where the API key names and task options
// live). A key absent from the file is sent at its default.
//
// Nothing secret is sent: an `*_env` value is a variable NAME, shown as
// "NAME (set)" or "NAME (unset)" after asking getenv whether it exists, and its
// content is never put in the report; a URL loses its userinfo, query and
// fragment. The worker schema has no key that holds a command or its arguments.
// Scrub then runs on the result as it will on the server, so what leaves this
// process already passes it.
//
// (A line comment on a value in a rendered file would stop the schema from
// adding its head comment, which is why the first plan annotated the rendered
// YAML after rendering; with typed entries the annotation lives in the value.)
func Build(config []byte, perimeter, worker, version string, kbs []string, getenv func(string) string) (*Report, error) {
	var doc yaml.Node
	if len(config) > 0 {
		if err := yaml.Unmarshal(config, &doc); err != nil {
			return nil, fmt.Errorf("workerconfig: parse config: %w", err)
		}
	}
	var entries []Entry
	for _, k := range Schema.Keys {
		if k.Type == "list" || strings.Contains(k.Path, "[]") {
			continue
		}
		e := Entry{Key: k.Path, Origin: "default"}
		if n := lookup(&doc, k.Path); n != nil && n.Kind == yaml.ScalarNode {
			e.Origin = "file"
			_ = n.Decode(&e.Value)
		} else {
			_ = yaml.Unmarshal([]byte(k.Default), &e.Value)
		}
		if i, ok := e.Value.(int); ok {
			e.Value = float64(i) // the JSON number Scrub sees on the server
		}
		s, _ := e.Value.(string)
		switch k.Type {
		case "env":
			e.Value = envValue(s, getenv)
		case "url":
			e.Value = stripURL(s)
		}
		entries = append(entries, e)
	}
	return &Report{Perimeter: perimeter, Worker: worker, Version: version, KBs: kbs, Entries: Scrub(entries)}, nil
}

// envValue shows the name of a variable and whether it is set, never its value.
func envValue(name string, getenv func(string) string) string {
	if name == "" {
		return ""
	}
	if getenv != nil && getenv(name) != "" {
		return name + " (set)"
	}
	return name + " (unset)"
}

// stripURL drops the userinfo, query and fragment of a URL; one that does not
// parse is not sent.
func stripURL(s string) string {
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return Redacted
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

// lookup returns the value node at a dotted path of a document, or nil.
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
