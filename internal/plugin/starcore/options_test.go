package starcore

import (
	"strings"
	"testing"

	"go.starlark.net/starlark"
)

// A host adds modules and registrars; it cannot replace a builtin whose checks
// (the network allow-list, the env list, the closed settings of a load) are the
// runtime's contract.
func TestOptionsRefuseReplacingABuiltin(t *testing.T) {
	src := map[string][]byte{"plugin.star": []byte("x = 1\n")}
	for _, name := range []string{"http", "env", "settings", "log", "json", "math", "time"} {
		for _, o := range []Options{
			{Extra: starlark.StringDict{name: starlark.None}},
			{Registrars: starlark.StringDict{name: starlark.None}},
		} {
			_, err := run(src, &settingsValue{}, o, nil)
			if err == nil || !strings.Contains(err.Error(), "may not replace the builtin") {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
	if _, err := run(src, &settingsValue{}, Options{Extra: starlark.StringDict{"files": starlark.None}}, nil); err != nil {
		t.Errorf("a new module: %v", err)
	}
}
