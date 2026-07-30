package main

import "testing"

func TestParseApproveMCPArgs(t *testing.T) {
	name, kb, yes, ok := parseApproveMCPArgs([]string{"tools", "--kb", "wiki", "--yes"})
	if !ok || name != "tools" || kb != "wiki" || !yes {
		t.Fatalf("got %q %q %v %v", name, kb, yes, ok)
	}
	if _, _, _, ok := parseApproveMCPArgs([]string{"--yes"}); ok {
		t.Fatal("missing name accepted")
	}
	if _, _, _, ok := parseApproveMCPArgs([]string{"tools", "--kb"}); ok {
		t.Fatal("missing kb accepted")
	}
}
