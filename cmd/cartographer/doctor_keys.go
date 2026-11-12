package main

import (
	"fmt"
	"os"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/service"
	"github.com/BeppeTemp/cartographer/internal/settingschema"
)

// serverConfigPathFn is the server config.yaml the installed service reads, ""
// when there is none to read. A variable so a test names the file.
var serverConfigPathFn = func() string {
	p, err := service.NewManager().EffectiveConfigPath("")
	if err != nil {
		return ""
	}
	return p
}

// checkUnknownKeys: a key of the client .cartographer.yaml or of the server
// config.yaml that no table knows. Both loaders keep going past it (a key a
// newer release adds must not stop an older one), so a typo does nothing and
// says nothing; doctor is where it shows, with the nearest known key. A file
// that is absent or unreadable is another check's finding, not this one's.
func checkUnknownKeys(dir string) []doctorFinding {
	var out []doctorFinding
	for _, f := range []struct {
		name, path string
		unknown    func([]byte) []settingschema.Unknown
	}{
		{"client", clientconfig.Path(dir), clientconfig.UnknownKeys},
		{"server", serverConfigPathFn(), config.UnknownKeys},
	} {
		data, err := os.ReadFile(f.path)
		if f.path == "" || err != nil {
			continue
		}
		for _, u := range f.unknown(data) {
			fix := "remove the key, or correct its name"
			if u.Hint != "" {
				fix = fmt.Sprintf("rename it to %s", u.Hint)
			}
			out = append(out, doctorFinding{
				Check: "unknown-keys", Severity: doctorWarning, Path: f.path,
				Message: fmt.Sprintf("%s config: unknown key %s (line %d): it is ignored", f.name, escapeControl(u.Path), u.Line),
				Fix:     fix,
			})
		}
	}
	return out
}
