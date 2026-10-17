package kb

import "testing"

func TestKeyedTypePredicates(t *testing.T) {
	cases := []struct {
		typ                     string
		source, contra, svc, dg bool
	}{
		{"Source", true, false, false, false},
		{"source", false, false, false, false},
		{"Contradiction", false, true, false, false},
		{"contradiction", false, false, false, false},
		{"Service", false, false, true, false},
		{"service", false, false, true, false},
		{"SERVICE", false, false, true, false},
		{"digest", false, false, false, true},
		{"Digest", false, false, false, true},
		{"DIGEST", false, false, false, true},
		{"Entity", false, false, false, false},
		{"", false, false, false, false},
	}
	for _, c := range cases {
		if got := IsSourceType(c.typ); got != c.source {
			t.Errorf("IsSourceType(%q) = %v", c.typ, got)
		}
		if got := IsContradictionType(c.typ); got != c.contra {
			t.Errorf("IsContradictionType(%q) = %v", c.typ, got)
		}
		if got := IsServiceType(c.typ); got != c.svc {
			t.Errorf("IsServiceType(%q) = %v", c.typ, got)
		}
		if got := IsDigestType(c.typ); got != c.dg {
			t.Errorf("IsDigestType(%q) = %v", c.typ, got)
		}
		if want := c.source || c.contra || c.svc || c.dg; IsKeyedType(c.typ) != want {
			t.Errorf("IsKeyedType(%q) = %v, want %v", c.typ, !want, want)
		}
	}
}
