package provisioning

import "testing"

func TestPickVersionOutput(t *testing.T) {
	for _, tc := range []struct{ name, stdout, stderr, want string }{
		{"stdout wins", "opencode v2.0.25\n", "1.0.0", "opencode v2.0.25\n"},
		{"stderr when stdout empty", "", "opencode v2.0.25\n", "opencode v2.0.25\n"},
		{"stderr when stdout has no version", "hello", "1.18.34", "1.18.34"},
		{"neither: stdout", "hello", "world", "hello"},
	} {
		if got := pickVersionOutput(tc.stdout, tc.stderr); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestOpenCodeLatestShape(t *testing.T) {
	if got := openCodeLatestShape(); got < 2 {
		t.Errorf("openCodeLatestShape = %d, want the highest HookPluginMajors entry (>= 2)", got)
	}
}
