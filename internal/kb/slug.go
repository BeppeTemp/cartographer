package kb

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/search"
)

// SlugPattern is the concept file-name pattern, the same as an artifact slug.
// The write path (checkNewConceptID) and the nonslug_file_name repair apply
// this one rule, so an ID a caller was handed never changes later (#1280).
var SlugPattern = regexp.MustCompile(`^[a-z0-9]+(-{1,2}[a-z0-9]+)*$`)

// slugExpansions spells out letters that have no single-letter ASCII fold.
// Search.Fold stays rune-preserving for search offsets; file slugs can expand.
var slugExpansions = strings.NewReplacer(
	"ß", "ss", "ẞ", "ss",
	"æ", "ae", "Æ", "ae",
	"œ", "oe", "Œ", "oe",
	"þ", "th", "Þ", "th",
	"ĳ", "ij", "Ĳ", "ij",
)

// Slugify lowercases a file stem, expands slug-specific letters, folds accents,
// and turns every run of other characters into one hyphen.
func Slugify(s string) string {
	var sb strings.Builder
	pendingHyphen := false
	for _, r := range search.Fold(slugExpansions.Replace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if pendingHyphen && sb.Len() > 0 {
				sb.WriteByte('-')
			}
			pendingHyphen = false
			sb.WriteRune(r)
			continue
		}
		pendingHyphen = true
	}
	return sb.String()
}

// checkNewConceptID refuses, for a concept that does not exist yet, an ID the
// repair would later rename (its file stem is not a slug) and an ID that
// differs only in case from an existing concept of the same folder. The check
// runs on every filesystem: on a case-insensitive one the second write would
// overwrite the first file, on a case-sensitive one it would create a twin
// that breaks the first on a checkout elsewhere (#1280). absPath is the file
// the write targets (its parent is the folder to scan).
func checkNewConceptID(id okf.ConceptID, absPath string, expanded bool) error {
	stem := string(id)[strings.LastIndex(string(id), "/")+1:]
	if !SlugPattern.MatchString(stem) {
		if slug := Slugify(stem); slug != "" {
			return fmt.Errorf("%w: %q is not a canonical concept id: its name must be a lowercase-hyphenated slug (a-z, 0-9, -); use %q",
				okf.ErrInvalidPath, string(id), strings.TrimSuffix(string(id), stem)+slug)
		}
		return fmt.Errorf("%w: %q is not a canonical concept id: its name must be a lowercase-hyphenated slug (a-z, 0-9, -) and %q has no letter or digit a slug can keep; choose an ASCII name",
			okf.ErrInvalidPath, string(id), stem)
	}
	dir := filepath.Dir(absPath)
	if expanded {
		dir = filepath.Dir(dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // no folder yet: nothing to collide with
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			// An expanded concept is a folder; a plain subfolder collides alike.
		} else if strings.HasSuffix(name, ".md") {
			name = strings.TrimSuffix(name, ".md")
		} else {
			continue
		}
		if name != stem && strings.EqualFold(name, stem) {
			return fmt.Errorf("%w: %q differs only in case from the existing concept %q: write to that one, or choose another name",
				okf.ErrInvalidPath, string(id), strings.TrimSuffix(string(id), stem)+name)
		}
	}
	return nil
}
