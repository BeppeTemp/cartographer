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

// windowsDeviceNames are the file names Windows reserves, with or without an
// extension ("aux.md" is the device too): a page named so makes the repository
// impossible to clone on Windows ("invalid path 'data/m/aux.md'").
var windowsDeviceNames = func() map[string]bool {
	m := map[string]bool{"con": true, "prn": true, "aux": true, "nul": true}
	for i := '1'; i <= '9'; i++ {
		m["com"+string(i)] = true
		m["lpt"+string(i)] = true
	}
	return m
}()

// IsWindowsDeviceName reports whether a file stem (any extension after the
// first dot is ignored) is a name Windows reserves, in any case.
func IsWindowsDeviceName(stem string) bool {
	base, _, _ := strings.Cut(stem, ".")
	return windowsDeviceNames[strings.ToLower(base)]
}

// checkConceptName guards the name of a concept write (#1280). Always, even
// when the file already "exists" (on a case-insensitive filesystem a stat of
// notes/upper.md hits Upper.md): a name that differs only in case from the
// entry on disk is refused, because the write would overwrite that file under
// another ID. The same holds for every directory segment of the ID: with map
// "notes" present, "Notes/foo" would land in notes/ while the answer, the log
// and the links would say Notes/foo, which no Linux clone resolves. Only for
// a concept that does not exist yet and when keepName is false: a stem that is
// not a slug is refused too, naming the canonical form (the repair would
// rename it later), a parent directory that does not exist yet must be a slug
// as well, and a Windows device name is refused in either position. keepName
// is for a move or transfer that keeps the source's own legacy stem. absPath
// is the file the write targets.
func checkConceptName(id okf.ConceptID, absPath string, expanded, fileExists, keepName bool) error {
	segs := strings.Split(string(id), "/")
	stem := segs[len(segs)-1]
	dir := filepath.Dir(absPath)
	if expanded {
		dir = filepath.Dir(dir)
	}
	twinErr := func(segIdx int, twin string) error {
		canon := append(append([]string{}, segs[:segIdx]...), twin)
		canon = append(canon, segs[segIdx+1:]...)
		return fmt.Errorf("%w: %q differs only in case from the existing concept %q: write to that one, or choose another name",
			okf.ErrInvalidPath, string(id), strings.Join(canon, "/"))
	}
	// Parent segments, innermost first: cur is the directory of segment i,
	// and its parent is the directory whose entries are compared with it.
	cur := dir
	for i := len(segs) - 2; i >= 0; i-- {
		parent := filepath.Dir(cur)
		exact, twin := dirEntryTwin(parent, segs[i])
		switch {
		case twin != "" && !exact:
			return twinErr(i, twin)
		case !exact && !keepName:
			if _, err := os.Lstat(cur); err != nil {
				if !SlugPattern.MatchString(segs[i]) {
					slug := Slugify(segs[i])
					if slug == "" {
						return fmt.Errorf("%w: %q is not a canonical concept id: the directory %q is not a lowercase-hyphenated slug (a-z, 0-9, -); choose an ASCII name",
							okf.ErrInvalidPath, string(id), segs[i])
					}
					return fmt.Errorf("%w: %q is not a canonical concept id: the new directory %q must be a lowercase-hyphenated slug (a-z, 0-9, -); use %q",
						okf.ErrInvalidPath, string(id), segs[i], slug)
				}
				if IsWindowsDeviceName(segs[i]) {
					return fmt.Errorf("%w: %q: %q is a reserved Windows device name, a repository holding it cannot be cloned on Windows; choose another name",
						okf.ErrInvalidPath, string(id), segs[i])
				}
			}
		}
		cur = parent
	}
	if exact, twin := dirEntryTwin(dir, stem); twin != "" && !exact {
		return twinErr(len(segs)-1, twin)
	}
	if fileExists || keepName {
		return nil
	}
	if SlugPattern.MatchString(stem) {
		if IsWindowsDeviceName(stem) {
			return fmt.Errorf("%w: %q: %q is a reserved Windows device name, a repository holding it cannot be cloned on Windows; choose another name",
				okf.ErrInvalidPath, string(id), stem)
		}
		return nil
	}
	if slug := Slugify(stem); slug != "" {
		return fmt.Errorf("%w: %q is not a canonical concept id: its name must be a lowercase-hyphenated slug (a-z, 0-9, -); use %q",
			okf.ErrInvalidPath, string(id), strings.TrimSuffix(string(id), stem)+slug)
	}
	return fmt.Errorf("%w: %q is not a canonical concept id: its name must be a lowercase-hyphenated slug (a-z, 0-9, -) and %q has no letter or digit a slug can keep; choose an ASCII name",
		okf.ErrInvalidPath, string(id), stem)
}

// dirEntryTwin looks in dir for the entry a concept segment names: a
// directory, or a "<name>.md" file. exact says an entry spelled exactly
// name exists; twin is the spelling of one that differs only in case.
func dirEntryTwin(dir, name string) (exact bool, twin string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, ""
	}
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() {
			if !strings.HasSuffix(n, ".md") {
				continue
			}
			n = strings.TrimSuffix(n, ".md")
		}
		switch {
		case n == name:
			exact = true
		case strings.EqualFold(n, name):
			twin = n
		}
	}
	return exact, twin
}
