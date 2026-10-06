package config

import (
	"testing"
)

func TestSanitizeToolPrefix(t *testing.T) {
	cases := map[string]string{
		"eng-team": "eng_team",
		"ENG Team": "eng_team",
		"eng_team": "eng_team",
		"  ai  ":   "ai",
		"a--b__c":  "a_b_c",
		"---":      "",
		"":         "",
		"1kb":      "1kb", // digit-leading is rejected by ValidateToolPrefixShape, not sanitisation
	}
	for in, want := range cases {
		if got := SanitizeToolPrefix(in); got != want {
			t.Errorf("SanitizeToolPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateToolPrefixShape(t *testing.T) {
	cases := map[string]bool{ // sanitized input -> wantErr
		"eng_team": false,
		"engteam":  false,
		"1kb":      true, // leading digit
		"":         true, // empty (e.g. sanitized from "---")
	}
	for in, wantErr := range cases {
		err := ValidateToolPrefixShape(in)
		if (err != nil) != wantErr {
			t.Errorf("ValidateToolPrefixShape(%q) error = %v, wantErr %v", in, err, wantErr)
		}
	}
}
