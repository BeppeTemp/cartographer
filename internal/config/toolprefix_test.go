package config

import "testing"

func TestSanitizeToolPrefix(t *testing.T) {
	cases := map[string]string{
		"ai-team": "ai_team",
		"AI Team": "ai_team",
		"ai_team": "ai_team",
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
		"ai_team": false,
		"aiteam":  false,
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
		{name: "off, no explicit prefix", spec: KBSpec{}, mode: "off", kbName: "ai-team", want: ""},
		{name: "explicit prefix wins", spec: KBSpec{ToolPrefix: "custom"}, mode: "kb-name", kbName: "ai-team", want: "custom"},
		{name: "explicit prefix sanitised", spec: KBSpec{ToolPrefix: "AI Team"}, mode: "off", kbName: "ai-team", want: "ai_team"},
		{name: "kb-name mode derives from kbName", spec: KBSpec{}, mode: "kb-name", kbName: "ai-team", want: "ai_team"},
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
