package provisioning

import (
	"os"
	"testing"
)

// TestHermesProjectTrusted: a Hermes workspace skill cell is only active when
// the directory is in skills.trusted_project_dirs; every other state is
// "not trusted", never an error.
func TestHermesProjectTrusted(t *testing.T) {
	home := t.TempDir()
	ws := "/home/user/work/app"

	if HermesProjectTrusted(home, ws) {
		t.Error("a missing config.yaml reported the project as trusted")
	}

	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(HermesConfigPath(home), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("model: x\nskills:\n  trusted_project_dirs:\n    - /home/user/work/app\n    - /home/user/work/other\n")
	if !HermesProjectTrusted(home, ws) {
		t.Error("a listed directory was reported as untrusted")
	}
	if !HermesProjectTrusted(home, ws+"/") {
		t.Error("a trailing separator changed the answer")
	}
	if HermesProjectTrusted(home, "/home/user/work/absent") {
		t.Error("an unlisted directory was reported as trusted")
	}

	write("model: x\nskills:\n  trusted_project_dirs: []\n")
	if HermesProjectTrusted(home, ws) {
		t.Error("an empty list reported the project as trusted")
	}
	write("model: x\n")
	if HermesProjectTrusted(home, ws) {
		t.Error("a config without a skills block reported the project as trusted")
	}
	write(":\n  - [unclosed")
	if HermesProjectTrusted(home, ws) {
		t.Error("an unparsable config reported the project as trusted")
	}
}
