package clientconfig

import "github.com/BeppeTemp/cartographer/internal/settingschema"

// Retired is the registry of the .cartographer.yaml keys that were removed, and
// of the values that switched a feature off and are reset to the default
// (D903): Load ignores a removed key with a warning that names what to use, and
// RealignFile drops it. Plans that remove or reset a client key add it here.
//
// It is empty today: the keys the cache split took out (D747) and
// doctor_timer_opt_out (D704) predate it and are dropped by Save (droppedKeys).
var Retired = settingschema.Registry{}

// RealignFile brings the client config in dir up to date (D903): the removed
// keys dropped, off values reset to the default, the schema's comments written,
// and the original kept beside it as .cartographer.yaml.bak-<version>. It
// reports what it changed, and does nothing, writing no backup, to a file that
// is already in that form, or that does not exist. `sync` runs it, under the
// client-state lock, before it loads the file.
func RealignFile(dir, version string) (rep settingschema.Report, backup string, err error) {
	return Schema.RealignFile(Path(dir), Retired, version)
}
