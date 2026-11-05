package provisioning

import (
	"os"
	"runtime"

	"github.com/BeppeTemp/cartographer/internal/clientvariant"
)

// VariantEnv is the environment a variant's anchors resolve against: the
// approval preview, the fingerprint recorded at approval and the check at every
// sync all read it, so they cannot disagree. A variable so a test can fix it.
var VariantEnv = func() clientvariant.Env {
	home, _ := os.UserHomeDir()
	return clientvariant.Env{GOOS: runtime.GOOS, Home: home, Getenv: os.Getenv}
}

// ResolveVariantArtifact parses the descriptor of a variant artifact and
// resolves it in the current environment, with the reasons paths were left out.
func ResolveVariantArtifact(a Artifact, opts ApplyOptions) (clientvariant.Variant, clientvariant.Env, []string, error) {
	env := VariantEnv()
	files := a.Files
	if len(files) == 0 {
		var err error
		if files, err = ReadArtifactFiles(a, opts.BundleFS, opts.KBRoots); err != nil {
			return clientvariant.Variant{}, env, nil, err
		}
	}
	if len(files) != 1 {
		return clientvariant.Variant{}, env, nil, os.ErrInvalid
	}
	spec, err := clientvariant.Parse(a.Name, files[0].Content)
	if err != nil {
		return clientvariant.Variant{}, env, nil, err
	}
	v, reasons := clientvariant.Resolve(spec, env)
	return v, env, reasons, nil
}

// VariantPathFingerprint is the fingerprint of the paths a variant artifact
// resolves to in this environment, "" when it cannot be read or parsed (which
// authorizes nothing).
func VariantPathFingerprint(a Artifact, opts ApplyOptions) string {
	v, env, _, err := ResolveVariantArtifact(a, opts)
	if err != nil {
		return ""
	}
	return v.PathFingerprint(env.Home)
}
