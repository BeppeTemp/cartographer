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

// checkConceptName guards the name of a concept write (#1280). Always, even
// when the file already "exists" (on a case-insensitive filesystem a stat of
// notes/upper.md hits Upper.md): a name that differs only in case from the
// entry on disk is refused, because the write would overwrite that file under
// another ID. Only for a concept that does not exist yet and when keepName is
// false: a stem that is not a slug is refused too, naming the canonical form
// (the repair would rename it later). keepName is for a move or transfer that
// keeps the source's own legacy stem. absPath is the file the write targets.
func checkConceptName(id okf.ConceptID, absPath string, expanded, fileExists, keepName bool) error {
	stem := string(id)[strings.LastIndex(string(id), "/")+1:]
	dir := filepath.Dir(absPath)
	if expanded {
		dir = filepath.Dir(dir)
	}
	if entries, err := os.ReadDir(dir); err == nil {
		exact, twin := false, ""
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() {
				if !strings.HasSuffix(name, ".md") {
					continue
				}
				name = strings.TrimSuffix(name, ".md")
			}
			switch {
			case name == stem:
				exact = true
			case strings.EqualFold(name, stem):
				twin = name
			}
		}
		if twin != "" && !exact {
			return fmt.Errorf("%w: %q differs only in case from the existing concept %q: write to that one, or choose another name",
				okf.ErrInvalidPath, string(id), strings.TrimSuffix(string(id), stem)+twin)
		}
	}
	if fileExists || keepName || SlugPattern.MatchString(stem) {
		return nil
	}
	if slug := Slugify(stem); slug != "" {
		return fmt.Errorf("%w: %q is not a canonical concept id: its name must be a lowercase-hyphenated slug (a-z, 0-9, -); use %q",
			okf.ErrInvalidPath, string(id), strings.TrimSuffix(string(id), stem)+slug)
	}
	return fmt.Errorf("%w: %q is not a canonical concept id: its name must be a lowercase-hyphenated slug (a-z, 0-9, -) and %q has no letter or digit a slug can keep; choose an ASCII name",
		okf.ErrInvalidPath, string(id), stem)
}
