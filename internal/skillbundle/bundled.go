// Package skillbundle embeds the skills shipped inside the binary (bundled/) so
// a fresh install can provision an agent client before any KB exists. They are
// handed to the tools as a filesystem, alongside the skills a KB provides.
package skillbundle

import "embed"

//go:embed all:bundled
var FS embed.FS
