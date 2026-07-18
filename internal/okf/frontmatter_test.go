package okf

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// --- Base parsing cases ---

func TestParseFrontmatter_Scalari(t *testing.T) {
	raw := "type: Runbook\ntitle: Rotazione certificati\ndescription: Procedura trimestrale."
	fm, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checkString(t, fm, "type", "Runbook")
	checkString(t, fm, "title", "Rotazione certificati")
	checkString(t, fm, "description", "Procedura trimestrale.")
}

func TestParseFrontmatter_ValoreVuoto(t *testing.T) {
	raw := "type: Runbook\nvalid_to:\ntitle: Test"
	fm, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v, ok := fm.Get("valid_to")
	if !ok {
		t.Fatal("key valid_to not found")
	}
	if v != nil {
		t.Fatalf("expected nil, got %v (%T)", v, v)
	}
}

func TestParseFrontmatter_ListaFlow(t *testing.T) {
	raw := "tags: [tls, sicurezza, certificati]"
	fm, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v, ok := fm.Get("tags")
	if !ok {
		t.Fatal("key tags not found")
	}
	got, ok := v.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", v)
	}
	want := []string{"tls", "sicurezza", "certificati"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestParseFrontmatter_ListaBlock(t *testing.T) {
	raw := "type: Runbook\ntags:\n- tls\n- sicurezza\ntitle: Test"
	fm, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	v, ok := fm.Get("tags")
	if !ok {
		t.Fatal("key tags not found")
	}
	got, ok := v.([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", v)
	}
	want := []string{"tls", "sicurezza"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	// title must be parsed correctly after the block list
	checkString(t, fm, "title", "Test")
}

func TestParseFrontmatter_ValoriQuotati(t *testing.T) {
	raw := `type: "Runbook"` + "\n" + `title: 'Rotazione certificati'` + "\n" + `tags: ["tls", 'sicurezza']`
	fm, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checkString(t, fm, "type", "Runbook")
	checkString(t, fm, "title", "Rotazione certificati")
	v, _ := fm.Get("tags")
	got, _ := v.([]string)
	want := []string{"tls", "sicurezza"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tags: expected %v, got %v", want, got)
	}
}

func TestParseFrontmatter_Commenti(t *testing.T) {
	raw := "# intestazione\ntype: Runbook\n# nota\ntitle: Test"
	fm, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checkString(t, fm, "type", "Runbook")
	checkString(t, fm, "title", "Test")

	// Serialize must preserve comments at their original position.
	serialized := fm.Serialize()
	if !strings.Contains(serialized, "# intestazione") {
		t.Fatalf("intestazione comment not found in Serialize(): %q", serialized)
	}
	if !strings.Contains(serialized, "# nota") {
		t.Fatalf("nota comment not found in Serialize(): %q", serialized)
	}
	// Comments must precede their respective keys.
	if strings.Index(serialized, "# intestazione") > strings.Index(serialized, "type:") {
		t.Fatal("# intestazione must precede type:")
	}
	if strings.Index(serialized, "# nota") > strings.Index(serialized, "title:") {
		t.Fatal("# nota must precede title:")
	}

	// CanonicalString must not contain comments.
	canonical := fm.CanonicalString()
	if strings.Contains(canonical, "#") {
		t.Fatalf("CanonicalString must not contain comments: %q", canonical)
	}
}

// --- Roundtrip ---

func TestParseFrontmatter_Roundtrip(t *testing.T) {
	raw := "type: Runbook\ntitle: Rotazione certificati\ntags: [tls, sicurezza]\nvalid_to:"
	fm1, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("first parse: %v", err)
	}
	serialized := fm1.Serialize()
	fm2, err := ParseFrontmatter(serialized)
	if err != nil {
		t.Fatalf("second parse: %v", err)
	}
	// Keys must be the same in insertion order.
	if !reflect.DeepEqual(fm1.Keys(), fm2.Keys()) {
		t.Fatalf("different keys after roundtrip: %v vs %v", fm1.Keys(), fm2.Keys())
	}
	// Values must match.
	for _, k := range fm1.Keys() {
		v1, _ := fm1.Get(k)
		v2, _ := fm2.Get(k)
		if !reflect.DeepEqual(v1, v2) {
			t.Fatalf("key %q: value %v != %v after roundtrip", k, v1, v2)
		}
	}
}

// --- CanonicalString ---

func TestCanonicalString_OrdineAlfabeticoSenzaCommenti(t *testing.T) {
	raw := "# commento\nzeta: z\nalfa: a\nbeta: b"
	fm, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	canonical := fm.CanonicalString()

	// no comments
	if strings.Contains(canonical, "#") {
		t.Fatalf("CanonicalString contains comments: %q", canonical)
	}

	// alphabetical order
	lines := strings.Split(canonical, "\n")
	var keys []string
	for _, l := range lines {
		if l == "" {
			continue
		}
		idx := strings.Index(l, ":")
		if idx > 0 {
			keys = append(keys, strings.TrimSpace(l[:idx]))
		}
	}
	sorted := make([]string, len(keys))
	copy(sorted, keys)
	sort.Strings(sorted)
	if !reflect.DeepEqual(keys, sorted) {
		t.Fatalf("CanonicalString is not sorted: %v", keys)
	}
}

// --- Type() ---

func TestType_Shortcut(t *testing.T) {
	raw := "type: Runbook\ntitle: Test"
	fm, _ := ParseFrontmatter(raw)
	if got := fm.Type(); got != "Runbook" {
		t.Fatalf("Type(): expected Runbook, got %q", got)
	}
}

func TestType_Assente(t *testing.T) {
	raw := "title: Test"
	fm, _ := ParseFrontmatter(raw)
	if got := fm.Type(); got != "" {
		t.Fatalf("Type() without type field: expected \"\", got %q", got)
	}
}

// --- Set / Delete ---

func TestSetDelete(t *testing.T) {
	raw := "type: Runbook\ntitle: Test"
	fm, _ := ParseFrontmatter(raw)

	// Set a new key.
	fm.Set("status", "active")
	checkString(t, fm, "status", "active")

	// Set overwrites an existing key keeping its position.
	fm.Set("type", "Concept")
	checkString(t, fm, "type", "Concept")
	// type must remain before title in order
	keys := fm.Keys()
	if keys[0] != "type" || keys[1] != "title" {
		t.Fatalf("unexpected key order after Set: %v", keys)
	}

	// Delete removes the key.
	fm.Delete("title")
	if _, ok := fm.Get("title"); ok {
		t.Fatal("title should not exist after Delete")
	}
	// other keys must remain
	checkString(t, fm, "type", "Concept")
	checkString(t, fm, "status", "active")

	// Delete of non-existent key is no-op.
	fm.Delete("chiave-inesistente")
}

// --- Tolerance to unknown fields ---

func TestTolleranzaCampiSconosciuti(t *testing.T) {
	raw := "type: Runbook\ncampo_sconosciuto_xyz: valore\ntitle: Test"
	fm, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("unexpected error on unknown field: %v", err)
	}
	checkString(t, fm, "campo_sconosciuto_xyz", "valore")

	// Serialize must preserve the unknown field.
	s := fm.Serialize()
	if !strings.Contains(s, "campo_sconosciuto_xyz: valore") {
		t.Fatalf("unknown field not found in Serialize(): %q", s)
	}
}

// --- Complete realistic OKF frontmatter ---

func TestParseFrontmatter_OKFCompleto(t *testing.T) {
	raw := `type: Runbook
title: Rotazione certificati TLS
description: Procedura trimestrale di rotazione dei certificati.
tags: [tls, sicurezza]
timestamp: 2026-06-25T10:00:00Z
status: active
provenance: [/raw/maintenance/cert-policy.pdf]
confidence: high
review_after: 2026-09-25`

	fm, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checkString(t, fm, "type", "Runbook")
	checkString(t, fm, "title", "Rotazione certificati TLS")
	checkString(t, fm, "description", "Procedura trimestrale di rotazione dei certificati.")
	checkString(t, fm, "timestamp", "2026-06-25T10:00:00Z")
	checkString(t, fm, "status", "active")
	checkString(t, fm, "confidence", "high")
	checkString(t, fm, "review_after", "2026-09-25")

	v, ok := fm.Get("tags")
	if !ok {
		t.Fatal("key tags not found")
	}
	tags, _ := v.([]string)
	if !reflect.DeepEqual(tags, []string{"tls", "sicurezza"}) {
		t.Fatalf("tags: %v", tags)
	}

	v, ok = fm.Get("provenance")
	if !ok {
		t.Fatal("key provenance not found")
	}
	prov, _ := v.([]string)
	if !reflect.DeepEqual(prov, []string{"/raw/maintenance/cert-policy.pdf"}) {
		t.Fatalf("provenance: %v", prov)
	}

	if fm.Type() != "Runbook" {
		t.Fatalf("Type(): %q", fm.Type())
	}

	// All fields must be present in serialization.
	s := fm.Serialize()
	for _, k := range []string{"type", "title", "description", "tags", "timestamp", "status", "provenance", "confidence", "review_after"} {
		if !strings.Contains(s, k+":") {
			t.Fatalf("key %q not found in Serialize()", k)
		}
	}
}

// --- helpers ---

func checkString(t *testing.T, fm *Frontmatter, key, want string) {
	t.Helper()
	v, ok := fm.Get(key)
	if !ok {
		t.Fatalf("key %q not found", key)
	}
	got, ok := v.(string)
	if !ok {
		t.Fatalf("key %q: expected string, got %T", key, v)
	}
	if got != want {
		t.Fatalf("key %q: expected %q, got %q", key, want, got)
	}
}
