package execbit

import (
	"io/fs"
	"testing"
)

func TestIsExecutable(t *testing.T) {
	tests := []struct {
		name string
		mode fs.FileMode
		want bool
	}{
		{"none", 0o644, false},
		{"owner execute", 0o744, true},
		{"group execute", 0o674, true},
		{"other execute", 0o645, true},
		{"all execute", 0o755, true},
		{"regular file default", 0o666, false},
		{"directory no execute", 0o644 | fs.ModeDir, false},
		{"directory with execute", 0o755 | fs.ModeDir, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsExecutable(tt.mode)
			if !Supported {
				tt.want = false
			}
			if got != tt.want {
				t.Fatalf("IsExecutable(%#o) = %v, want %v", tt.mode, got, tt.want)
			}
		})
	}
}
