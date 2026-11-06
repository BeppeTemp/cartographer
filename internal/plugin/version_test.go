package plugin

import "testing"

func TestRangeMet(t *testing.T) {
	cases := []struct {
		rng, running string
		met          bool
	}{
		{">=0.28.0", "0.28.0", true},
		{">=0.28.0", "0.27.3", false},
		{">=0.28.0", "v0.29.1", true},
		{">0.28.0", "0.28.0", false},
		{"<=0.28.0", "0.28.0", true},
		{"<0.28.0", "0.28.0", false},
		{"=0.28.0", "0.28.0", true},
		{"0.28.0", "0.28.0", true},
		{"0.28.0", "0.28.1", false},
		{">=0.28.0 <0.30.0", "0.29.9", true},
		{">=0.28.0 <0.30.0", "0.30.0", false},
		{">=0.28.0   <0.30.0", "0.28.0", true},
		{">=0.10.0", "0.9.0", false},
		// a pre-release or describe build compares as its core
		{">=0.28.0", "0.28.0-rc.1", true},
		{">=0.28.0", "0.28.0-12-g1a2b3c4", true},
		{">=0.28.0", "0.28.0-12-g1a2b3c4-dirty", true},
		{">0.28.0", "0.28.0-rc.1", false},
		// a running version that does not parse never refuses
		{">=9.0.0", "dev", true},
		{">=9.0.0", "", true},
	}
	for _, c := range cases {
		got, err := RangeMet(c.rng, c.running)
		if err != nil || got != c.met {
			t.Errorf("RangeMet(%q, %q) = %v, %v; want %v", c.rng, c.running, got, err, c.met)
		}
	}
}

func TestValidRangeRejects(t *testing.T) {
	for _, rng := range []string{
		"", "   ", ">=", ">=0.28", ">=0.28.0.1", "~0.28.0", "^0.28.0", "latest", "*",
		">=0.28.0 junk", "=>0.28.0", "== 0.28.0", ">= 0.28.0", ">=01.2.3", ">=-1.0.0",
		">=0.28.0-rc.1", ">=1.2.3 >=1.2.3 >=1.2.3 >=1.2.3 >=1.2.3 >=1.2.3 >=1.2.3 >=1.2.3 >=1.2.3",
	} {
		if err := ValidRange(rng); err == nil {
			t.Errorf("ValidRange(%q) accepted a malformed range", rng)
		}
		if _, err := RangeMet(rng, "0.28.0"); err == nil {
			t.Errorf("RangeMet(%q) accepted a malformed range", rng)
		}
	}
}
