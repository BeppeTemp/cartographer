package mcpserver

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
	"gopkg.in/yaml.v3"
)

// The Atlas UI's Secrets route: the structure of the KB's sops files, for a
// human to browse, and the ciphertext the browser needs to decrypt a value
// with an age key the reader pastes into the page. The server never decrypts,
// never sees a key and never runs sops: it only parses YAML it would serve
// from git anyway. Whole-KB resource, like the artifacts (404 otherwise).

const uiSecretsMaxFile = 1 << 20

type uiSecretEntry struct {
	// Pointer is the RFC 6901 pointer of the value; list items count.
	Pointer string `json:"pointer"`
	// AADPath is the additional data sops authenticated the value with: the
	// map keys from the root joined and ended by ":". List indices do not
	// take part (sops walks a list under the path of the key that holds it).
	AADPath string `json:"aad_path"`
	// Enc is the raw ENC[AES256_GCM,...] string.
	Enc string `json:"enc"`
	// ReferencedBy lists the concepts whose secret_refs name this pointer.
	ReferencedBy []string `json:"referenced_by"`
}

type uiSecretFile struct {
	Path         string          `json:"path"`
	Entries      []uiSecretEntry `json:"entries"`
	Recipients   []string        `json:"recipients"`
	AgeStanzas   []string        `json:"age_stanzas"`
	LastModified string          `json:"lastmodified,omitempty"`
	// SourceOf lists concepts that declare the whole file as secrets_source.
	SourceOf []string `json:"source_of"`
	// Error is set when the file could not be parsed as a sops document.
	Error string `json:"error,omitempty"`
}

func (m *MultiKBServer) uiSecrets(w http.ResponseWriter, r *http.Request, srv *Server) {
	if !SecretsVisible(r.Context(), srv.kbRef) {
		writeUINotFound(w)
		return
	}
	files, err := uiSecretsList(srv.kbRef)
	if err != nil {
		writeUIInternal(w, "secrets", err)
		return
	}
	body := map[string]interface{}{"files": files}
	// The kind's own descriptor (D864): the empty state says where a secrets
	// file goes and links the docs, with the path provisioning declares.
	for _, k := range contract.Kinds() {
		if k.Kind == "secret" {
			body["kind"] = uiKindInfoFrom(k)
		}
	}
	writeUIJSON(w, http.StatusOK, body)
}

// uiSecretsVisible is the `secrets` flag of GET /kbs.
func uiSecretsVisible(ctx requestContext, k *kb.KB) bool {
	return SecretsVisible(ctx, k)
}

func uiSecretsList(k *kb.KB) ([]uiSecretFile, error) {
	files := []uiSecretFile{}
	dir := filepath.Join(k.Root, "secrets")
	// secrets/ itself must be a plain directory, not a link out of the KB.
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return files, nil
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	refs, sources, err := uiSecretRefs(k)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		name := it.Name()
		if !strings.HasSuffix(name, ".sops.yaml") || !it.Type().IsRegular() {
			continue // symlinks, directories and other files never leave the KB
		}
		rel := "secrets/" + name
		f := uiSecretFile{Path: rel, Entries: []uiSecretEntry{}, Recipients: []string{}, AgeStanzas: []string{}, SourceOf: sources[rel]}
		if f.SourceOf == nil {
			f.SourceOf = []string{}
		}
		data, err := readCapped(filepath.Join(dir, name))
		if err == nil {
			err = parseSecretFile(data, &f, refs)
		}
		if err != nil {
			f.Entries, f.Error = []uiSecretEntry{}, err.Error()
		}
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func readCapped(p string) ([]byte, error) {
	info, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > uiSecretsMaxFile {
		return nil, fmt.Errorf("not a plain file under %d bytes", uiSecretsMaxFile)
	}
	return os.ReadFile(p)
}

func parseSecretFile(data []byte, f *uiSecretFile, refs map[string][]string) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("not a YAML mapping")
	}
	root := doc.Content[0]
	var meta *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "sops" {
			meta = root.Content[i+1]
		}
	}
	if meta == nil || meta.Kind != yaml.MappingNode {
		return fmt.Errorf("no sops metadata: not an encrypted file")
	}
	for i := 0; i+1 < len(meta.Content); i += 2 {
		switch meta.Content[i].Value {
		case "lastmodified":
			f.LastModified = meta.Content[i+1].Value
		case "age":
			readAgeStanzas(meta.Content[i+1], f)
		case "key_groups":
			// Shamir key groups carry their age recipients one level down.
			for _, group := range meta.Content[i+1].Content {
				for j := 0; j+1 < len(group.Content); j += 2 {
					if group.Content[j].Value == "age" {
						readAgeStanzas(group.Content[j+1], f)
					}
				}
			}
		}
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "sops" {
			continue
		}
		k := root.Content[i].Value
		w := secretWalk{keys: []string{k}, tokens: []string{escapePointerToken(k)}, f: f, refs: refs}
		if err := w.walk(root.Content[i+1]); err != nil {
			return err
		}
	}
	return nil
}

// readAgeStanzas appends the recipients and wrapped data keys of a sops `age`
// list to f.
func readAgeStanzas(list *yaml.Node, f *uiSecretFile) {
	for _, st := range list.Content {
		for j := 0; j+1 < len(st.Content); j += 2 {
			switch st.Content[j].Value {
			case "recipient":
				f.Recipients = append(f.Recipients, st.Content[j+1].Value)
			case "enc":
				f.AgeStanzas = append(f.AgeStanzas, st.Content[j+1].Value)
			}
		}
	}
}

func escapePointerToken(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

// uiSecretsMaxDepth bounds the nesting the panel follows; a real sops document
// stays within a few levels.
const uiSecretsMaxDepth = 64

// secretWalk follows sops' own walk: a map key extends both the AAD path and the
// pointer, a list item only the pointer (sops authenticates a list item under
// the path of the key that holds the list). keys and tokens are one pair of
// slices pushed and popped along the walk and copied only when an entry is
// emitted, so memory does not grow with the depth.
type secretWalk struct {
	keys, tokens []string
	f            *uiSecretFile
	refs         map[string][]string
}

func (w *secretWalk) walk(n *yaml.Node) error {
	switch n.Kind {
	case yaml.MappingNode, yaml.SequenceNode:
		if len(w.tokens) > uiSecretsMaxDepth {
			return fmt.Errorf("nested deeper than %d levels", uiSecretsMaxDepth)
		}
	}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i].Value
			w.keys, w.tokens = append(w.keys, k), append(w.tokens, escapePointerToken(k))
			err := w.walk(n.Content[i+1])
			w.keys, w.tokens = w.keys[:len(w.keys)-1], w.tokens[:len(w.tokens)-1]
			if err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			w.tokens = append(w.tokens, strconv.Itoa(i))
			err := w.walk(c)
			w.tokens = w.tokens[:len(w.tokens)-1]
			if err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		if !strings.HasPrefix(n.Value, "ENC[AES256_GCM,") {
			return nil
		}
		pointer := "/" + strings.Join(w.tokens, "/")
		by := w.refs[w.f.Path+"#"+pointer]
		if by == nil {
			by = []string{}
		}
		w.f.Entries = append(w.f.Entries, uiSecretEntry{Pointer: pointer, AADPath: strings.Join(w.keys, ":") + ":", Enc: n.Value, ReferencedBy: by})
	}
	return nil
}

// uiSecretRefs reads every concept's secret_refs (NAME=file#pointer, as
// parseSecretRefs does) into pointer -> concept ids, and secrets_source into
// file -> concept ids.
func uiSecretRefs(k *kb.KB) (refs, sources map[string][]string, err error) {
	refs, sources = map[string][]string{}, map[string][]string{}
	add := func(m map[string][]string, key, id string) {
		for _, have := range m[key] {
			if have == id {
				return
			}
		}
		m[key] = append(m[key], id)
	}
	err = k.WalkConceptPaths(func(id okf.ConceptID, _, content string) error {
		fmRaw, _, _ := okf.SplitFrontmatter(content)
		fm, perr := okf.ParseFrontmatter(fmRaw)
		if perr != nil {
			return nil
		}
		if raw, ok := fm.Get("secret_refs"); ok {
			var entries []string
			switch v := raw.(type) {
			case []string:
				entries = v
			case []any:
				for _, e := range v {
					if s, ok := e.(string); ok {
						entries = append(entries, s)
					}
				}
			}
			for _, e := range entries {
				eq, hash := strings.Index(e, "="), strings.Index(e, "#")
				if eq > 0 && hash > eq+1 && hash < len(e)-1 {
					add(refs, e[eq+1:hash]+"#"+e[hash+1:], string(id))
				}
			}
		}
		// A string, or a list (lint's stringified_list rewrites it into one).
		if src, ok := fm.Get("secrets_source"); ok {
			switch v := src.(type) {
			case string:
				if v != "" {
					add(sources, v, string(id))
				}
			case []string:
				for _, s := range v {
					add(sources, s, string(id))
				}
			case []any:
				for _, e := range v {
					if s, ok := e.(string); ok && s != "" {
						add(sources, s, string(id))
					}
				}
			}
		}
		return nil
	})
	return refs, sources, err
}
