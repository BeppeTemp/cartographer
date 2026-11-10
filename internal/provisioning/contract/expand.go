package contract

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
)

// HashArtifactFiles uses a versioned, domain-separated length-prefixed
// encoding of path, effective executable mode and raw bytes. It records
// ManagedFile.MaterializedHash on the actually-written (placeholder-expanded)
// bytes; ContentHash stays the manifest's, the one ComputeDiff compares. When
// expansion is a no-op the two are equal (D75 WP3).
func HashArtifactFiles(files []ArtifactFile) string {
	sorted := append([]ArtifactFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	h := sha256.New()
	h.Write([]byte("cartographer:artifact-content:v1\x00"))
	for _, f := range sorted {
		writeHashField(h, []byte(f.Path))
		if f.Executable {
			h.Write([]byte{1})
		} else {
			h.Write([]byte{0})
		}
		writeHashField(h, f.Content)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func writeHashField(h interface{ Write([]byte) (int, error) }, data []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(data)))
	h.Write(length[:])
	h.Write(data)
}
