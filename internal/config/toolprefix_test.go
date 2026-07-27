package config

import "testing"

func TestSanitizeToolPrefix(t *testing.T) {
	cases := map[string]string{
		"eng-team": "eng_team",
		"ENG Team": "eng_team",
		"eng_team": "eng_team",
		"  ai  ":  "ai",
		"a--b__c": "a_b_c",
		"---":     "",
		"":        "",
		"1kb":     "1kb", // digit-leading is rejected by ValidateToolPrefixShape, not sanitisation
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
		"1kb":     true, // leading digit
		"":        true, // empty (e.g. sanitized from "---")
	}
	for in, wantErr := range cases {
		err := ValidateToolPrefixShape(in)
		if (err != nil) != wantErr {
			t.Errorf("ValidateToolPrefixShape(%q) error = %v, wantErr %v", in, err, wantErr)
		}
	}
}

func TestResolveToolPrefix(t *testing.T) {
	tests := []struct {
		name    string
		spec    KBSpec
		mode    string
		kbName  string
		want    string
		wantErr bool
	}{
		{name: "off, no explicit prefix", spec: KBSpec{}, mode: "off", kbName: "eng-team", want: ""},
		{name: "explicit prefix wins", spec: KBSpec{ToolPrefix: "custom"}, mode: "kb-name", kbName: "eng-team", want: "custom"},
		{name: "explicit prefix sanitised", spec: KBSpec{ToolPrefix: "ENG Team"}, mode: "off", kbName: "eng-team", want: "eng_team"},
		{name: "kb-name mode derives from kbName", spec: KBSpec{}, mode: "kb-name", kbName: "eng-team", want: "eng_team"},
		{name: "kb-name mode, digit-leading name fails", spec: KBSpec{}, mode: "kb-name", kbName: "1kb", wantErr: true},
		{name: "explicit prefix sanitises to empty fails", spec: KBSpec{ToolPrefix: "---"}, mode: "off", kbName: "x", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveToolPrefix(tt.spec, tt.mode, tt.kbName)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveToolPrefix() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("ResolveToolPrefix() = %q, want %q", got, tt.want)
			}
		})
	}
}
