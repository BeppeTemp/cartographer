// Package cartographer is the module root. It holds one thing: the
// documentation of the commit the binary is built from.
package cartographer

import "embed"

// DocsFS is the documentation of this commit, laid out as the repository root
// (README.md, CONTRIBUTING.md, docs/…), read by internal/docskb. The patterns
// are a closed list: a new docs subdirectory they miss is caught by
// TestEmbeddedDocsEqualTheDisk, not silently left out of the binary.
//
//go:embed README.md CONTRIBUTING.md docs/*.md docs/brand/*.md docs/decisions/*.md
var DocsFS embed.FS
