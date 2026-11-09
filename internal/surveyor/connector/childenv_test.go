package connector

import (
	"strings"
	"testing"
)

func TestChildEnvHoldsOnlyListedVariablesAndTheBaseSet(t *testing.T) {
	t.Setenv("MODEL_API_KEY", "provider-key-value")
	t.Setenv("WIKI_TOKEN", "wiki-token-value")
	env, err := ChildEnv(map[string]string{"API_TOKEN": "WIKI_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "API_TOKEN=wiki-token-value") {
		t.Errorf("listed variable missing: %v", env)
	}
	if strings.Contains(joined, "provider-key-value") || strings.Contains(joined, "MODEL_API_KEY") {
		t.Errorf("an unlisted variable reached the child: %v", env)
	}
	for _, e := range env {
		name, _, _ := strings.Cut(e, "=")
		switch name {
		case "API_TOKEN", "PATH", "HOME", "SystemRoot", "TEMP", "TMP", "USERPROFILE":
		default:
			t.Errorf("unexpected variable %s in the child env", name)
		}
	}
}

func TestChildEnvUnsetVariableIsNamedNeverItsValue(t *testing.T) {
	t.Setenv("PRESENT", "secret-present-value")
	_, err := ChildEnv(map[string]string{"A": "PRESENT", "B": "ABSENT_VAR"})
	if err == nil || !strings.Contains(err.Error(), "ABSENT_VAR") || strings.Contains(err.Error(), "secret-present-value") {
		t.Fatalf("err = %v", err)
	}
	if _, err := EnvValue("ABSENT_VAR"); err == nil || !strings.Contains(err.Error(), "ABSENT_VAR") {
		t.Errorf("Value err = %v", err)
	}
}

func TestRedactEnv(t *testing.T) {
	t.Setenv("WIKI_TOKEN", "tok-123456")
	got := RedactEnv("failed with tok-123456 in header", map[string]string{"X": "WIKI_TOKEN"})
	if got != "failed with *** in header" {
		t.Errorf("got %q", got)
	}
}
