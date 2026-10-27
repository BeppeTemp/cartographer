package search

import "testing"

const sectionFixture = "Intro about everything.\n\n## Install\nrun the installer and wait\n\n## Rollback\nto roll back restore the snapshot, then restart the rollback worker\n\n## Notes\nnothing special\n"

func TestBestSection(t *testing.T) {
	s, ok := BestSection(sectionFixture, "rollback snapshot")
	if !ok || s.Heading != "## Rollback" {
		t.Fatalf("got %+v ok=%v, want ## Rollback", s, ok)
	}
	if s, ok := BestSection(sectionFixture, "installer"); !ok || s.Heading != "## Install" {
		t.Fatalf("got %+v ok=%v, want ## Install", s, ok)
	}
	if _, ok := BestSection(sectionFixture, "absent"); ok {
		t.Fatal("a term in no section must give no section")
	}
	if _, ok := BestSection("no headings here, rollback", "rollback"); ok {
		t.Fatal("a body with no ## must give no section")
	}
}

func TestBestSection_TieGoesToEarlier(t *testing.T) {
	s, ok := BestSection("## A\nfoo\n## B\nfoo\n", "foo")
	if !ok || s.Heading != "## A" {
		t.Fatalf("got %+v", s)
	}
}

func TestBestSection_HeadingWeighsMore(t *testing.T) {
	s, ok := BestSection("## Other\nfoo bar baz\n## Foo\nunrelated words here ok\n", "foo")
	if !ok || s.Heading != "## Foo" {
		t.Fatalf("heading match must win, got %+v", s)
	}
}
