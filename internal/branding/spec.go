package branding

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Caps of the branding folder.
const (
	// DeclFile is the one declaration file of the folder.
	DeclFile    = "branding.yaml"
	maxDecl     = 4 << 10
	maxFile     = 256 << 10
	maxFolder   = 1 << 20
	maxCompany  = 60
	pngSig      = "\x89PNG\r\n\x1a\n"
	icoSig      = "\x00\x00\x01\x00"
	declNothing = "declares nothing"
)

var (
	accentRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	fileRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,60}\.(svg|png|ico)$`)
)

// InFile is one file of the folder as the plugin glue reads it.
type InFile struct {
	Content    []byte
	Executable bool
}

// File is a validated image of the folder.
type File struct {
	Name, Ext string // Ext without the dot: svg, png or ico
	Data      []byte
}

// Spec is a valid branding/ folder.
type Spec struct {
	Accent                       string // lowercase #rrggbb, or ""
	Company                      string // the alt text of the logo, or ""
	LogoLight, LogoDark, Favicon *File  // nil when not declared
}

type declared struct {
	Accent  *string `yaml:"accent"`
	Company *string `yaml:"company"`
	Logo    *struct {
		Light string `yaml:"light"`
		Dark  string `yaml:"dark"`
	} `yaml:"logo"`
	Favicon *string `yaml:"favicon"`
}

// Parse judges the folder, paths relative to branding/. It returns every
// problem found as `branding/<path>: reason`, sorted; the Spec is nil when
// there is one.
func Parse(files map[string]InFile) (*Spec, []string) {
	var probs []string
	add := func(path, format string, a ...any) {
		probs = append(probs, "branding/"+path+": "+fmt.Sprintf(format, a...))
	}
	decl, ok := files[DeclFile]
	switch {
	case !ok:
		add(DeclFile, "missing")
	case len(decl.Content) > maxDecl:
		add(DeclFile, "%d bytes, over the %d cap", len(decl.Content), maxDecl)
	case decl.Executable:
		add(DeclFile, "has the execute bit")
	}
	if len(probs) > 0 {
		return nil, probs
	}
	var d declared
	if err := decode(decl.Content, &d); err != nil {
		add(DeclFile, "%v", err)
		return nil, probs
	}
	s := &Spec{}
	if d.Accent != nil {
		if !accentRe.MatchString(*d.Accent) {
			add(DeclFile, "accent: %q is not a #rrggbb colour", *d.Accent)
		} else {
			s.Accent = strings.ToLower(*d.Accent)
		}
	}
	if d.Company != nil {
		if msg := companyProblem(*d.Company); msg != "" {
			add(DeclFile, "company: %s", msg)
		} else {
			s.Company = *d.Company
		}
	}
	if d.Accent == nil && d.Logo == nil && d.Favicon == nil {
		add(DeclFile, declNothing)
	}
	used := map[string]bool{DeclFile: true}
	load := func(key, name string, exts ...string) *File {
		used[name] = true
		f, msg := loadImage(files, name, exts)
		if msg != "" {
			add(DeclFile, "%s: %s", key, msg)
		}
		return f
	}
	if d.Logo != nil {
		if d.Logo.Light == "" {
			add(DeclFile, "logo: light is required when logo is present")
		} else {
			s.LogoLight = load("logo.light", d.Logo.Light, "svg", "png")
		}
		if d.Logo.Dark != "" {
			s.LogoDark = load("logo.dark", d.Logo.Dark, "svg", "png")
		}
	}
	if d.Favicon != nil {
		s.Favicon = load("favicon", *d.Favicon, "svg", "png", "ico")
	}
	total := 0
	for name, f := range files {
		total += len(f.Content)
		if !used[name] {
			add(name, "a stray file: the folder holds %s and the images it names", DeclFile)
		}
	}
	if total > maxFolder {
		add(DeclFile, "%d bytes in all, over the %d cap of the folder", total, maxFolder)
	}
	if len(probs) > 0 {
		sort.Strings(probs)
		return nil, probs
	}
	return s, nil
}

// decode reads strict YAML: one document, no alias, no unknown key. Aliases are
// refused before the strict decode, as the manifest does.
func decode(data []byte, into any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var root, second yaml.Node
	if err := dec.Decode(&root); err != nil && err != io.EOF {
		return err
	}
	if err := dec.Decode(&second); err != io.EOF {
		return fmt.Errorf("a second YAML document is not allowed")
	}
	if hasAlias(&root) {
		return fmt.Errorf("YAML aliases are not allowed")
	}
	strict := yaml.NewDecoder(bytes.NewReader(data))
	strict.KnownFields(true)
	if err := strict.Decode(into); err != nil && err != io.EOF {
		return err
	}
	return nil
}

func hasAlias(n *yaml.Node) bool {
	if n.Kind == yaml.AliasNode {
		return true
	}
	for _, c := range n.Content {
		if hasAlias(c) {
			return true
		}
	}
	return false
}

func companyProblem(s string) string {
	switch {
	case strings.TrimSpace(s) == "":
		return "must not be empty"
	case utf8.RuneCountInString(s) > maxCompany:
		return fmt.Sprintf("over %d characters", maxCompany)
	case strings.ContainsAny(s, "<>&\"'\\"):
		return "must not contain < > & quotes or a backslash"
	case strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0:
		return "must be one printable line"
	}
	return ""
}

// loadImage checks one image the declaration names; msg is "" when it is fine.
func loadImage(files map[string]InFile, name string, exts []string) (*File, string) {
	m := fileRe.FindStringSubmatch(name)
	if m == nil {
		return nil, fmt.Sprintf("%q is not a file name of this folder (lowercase, .svg, .png or .ico, no path)", name)
	}
	ext := m[1]
	if !contains(exts, ext) {
		return nil, fmt.Sprintf("%s: a .%s file is not allowed here (%s)", name, ext, strings.Join(exts, ", "))
	}
	in, ok := files[name]
	switch {
	case !ok:
		return nil, name + ": no such file in branding/"
	case in.Executable:
		return nil, name + ": has the execute bit"
	case len(in.Content) > maxFile:
		return nil, fmt.Sprintf("%s: %d bytes, over the %d cap of a file", name, len(in.Content), maxFile)
	}
	if msg := checkContent(ext, in.Content); msg != "" {
		return nil, name + ": " + msg
	}
	return &File{Name: name, Ext: ext, Data: in.Content}, ""
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// checkContent judges the bytes, never the extension alone.
func checkContent(ext string, data []byte) string {
	switch ext {
	case "png":
		if !bytes.HasPrefix(data, []byte(pngSig)) {
			return "not a PNG (wrong signature)"
		}
	case "ico":
		if !bytes.HasPrefix(data, []byte(icoSig)) {
			return "not an ICO (wrong header)"
		}
	default:
		return svgProblem(data)
	}
	return ""
}
