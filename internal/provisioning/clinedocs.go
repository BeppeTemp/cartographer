package provisioning

import "github.com/BeppeTemp/cartographer/internal/safewrite"

// clineDocumentsDir is the Documents folder Cline reads its global hooks from
// (Documents/Cline/Hooks). The resolver is safewrite.DocumentsDir, the one
// authority the {documents} anchor of a plugin provider shares.
func clineDocumentsDir(baseDir string) string { return safewrite.DocumentsDir(baseDir) }
