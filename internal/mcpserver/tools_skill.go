package mcpserver

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/skill"
	"github.com/BeppeTemp/cartographer/internal/sops"
)

// --- skill_list ---

func toolSkillList(k *kb.KB) Tool {
	return Tool{
		Name:        "skill_list",
		Description: "Lists skills installed in the KB (under skills/ directory).",
		ReadOnly:    true,
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			skills, errs := skill.LoadAllSkills(k.Root)
			if len(skills) == 0 {
				if len(errs) > 0 {
					return errorResult(fmt.Sprintf("skill_list: %v", errs[0])), nil
				}
				return textResult("No skills found."), nil
			}

			catalog := skill.Catalog(skills)
			var sb strings.Builder
			for _, e := range catalog {
				sb.WriteString(fmt.Sprintf("- %s", e.Name))
				if e.Version != "" {
					sb.WriteString(" v" + e.Version)
				}
				sb.WriteString(" (" + e.Path + ")")
				if e.Description != "" {
					sb.WriteString(": " + e.Description)
				}
				sb.WriteByte('\n')
			}
			return textResult(strings.TrimRight(sb.String(), "\n")), nil
		},
	}
}

// --- service_get ---

func toolServiceGet(k *kb.KB) Tool {
	return Tool{
		Name: "service_get",
		// ReadOnly for the default path: it only reads frontmatter+body. With
		// resolve_secrets=true it decrypts and returns the service's secrets,
		// which requires write-equivalent privilege — that override is NOT
		// expressed here (Tool.ReadOnly is a per-tool-name classification) but
		// as a special case in the authorizer (policy.go's authorizeTool, D47)
		// that inspects arguments.resolve_secrets directly.
		ReadOnly:    true,
		Description: "Reads a concept of type Service. Returns frontmatter (YAML) and body. With resolve_secrets=true, also decrypts and returns the service's secrets_source (requires rw scope).",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["service_id"],
			"properties": {
				"service_id": {
					"type": "string",
					"description": "Concept ID of the Service concept"
				},
				"resolve_secrets": {
					"type": "boolean",
					"description": "If true, decrypt the service's secrets_source (flat SOPS file) and include the resolved secrets in the result. Requires a KB with sops_age_key_file configured and rw scope. Default false."
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				ServiceID      string `json:"service_id"`
				ResolveSecrets bool   `json:"resolve_secrets"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.ServiceID == "" {
				return errorResult("'service_id' is required"), nil
			}

			data, err := k.ReadConcept(okf.ConceptID(params.ServiceID))
			if err != nil {
				return errorResult(fmt.Sprintf("service_get %q: %v", params.ServiceID, err)), nil
			}

			fm, err := okf.ParseFrontmatter(data.FrontmatterRaw)
			if err != nil {
				return errorResult(fmt.Sprintf("service_get: parse frontmatter: %v", err)), nil
			}

			// "Service" is a reserved type, matched case-insensitively: a KB whose
			// type vocabulary is lowercase (a very likely outcome of an import, or
			// of a non-English domain vocabulary) declared "service" and the
			// service tools silently returned nothing, leaving secret resolution
			// unusable with no error anywhere (D158).
			if !strings.EqualFold(fm.Type(), "Service") {
				return errorResult(fmt.Sprintf("service_get: %q has type %q, expected Service", params.ServiceID, fm.Type())), nil
			}

			var sb strings.Builder
			sb.WriteString("---\n")
			sb.WriteString(fm.Serialize())
			sb.WriteString("\n---\n\n")
			sb.WriteString(data.Body)

			if !params.ResolveSecrets {
				return textResult(sb.String()), nil
			}

			values, source, legacy, err := resolveFrontmatterSecrets(k, fm)
			if err != nil {
				return errorResult("service_get: resolve_secrets: " + err.Error()), nil
			}
			sb.WriteString("\n\n---\nsecrets (from ")
			sb.WriteString(source)
			sb.WriteString("):\n")
			if legacy {
				sb.WriteString("note: no secret_refs declared; returning whole secrets_source.\n")
			}
			keys := make([]string, 0, len(values))
			for key := range values {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				val := values[key]
				sb.WriteString(key)
				sb.WriteString("=")
				sb.WriteString(val)
				sb.WriteByte('\n')
			}
			return textResult(sb.String()), nil
		},
	}
}

var secretRefName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func parseSecretRefs(raw any) ([]sops.SecretRef, error) {
	entries, ok := raw.([]string)
	if !ok || len(entries) == 0 {
		return nil, fmt.Errorf("secret_refs must be a non-empty list of NAME=secrets/file.sops.yaml#/json-pointer")
	}
	refs := make([]sops.SecretRef, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		eq, hash := strings.Index(entry, "="), strings.Index(entry, "#")
		if eq <= 0 || hash <= eq+1 || hash == len(entry)-1 {
			return nil, fmt.Errorf("invalid secret_refs entry %q; expected NAME=secrets/file.sops.yaml#/json-pointer", entry)
		}
		name, path, ptr := entry[:eq], entry[eq+1:hash], entry[hash+1:]
		if strings.TrimSpace(name) != name || strings.TrimSpace(path) != path || strings.TrimSpace(ptr) != ptr || !secretRefName.MatchString(name) || !filepath.IsLocal(path) || !strings.HasPrefix(path, "secrets/") || !strings.HasSuffix(path, ".sops.yaml") || !strings.HasPrefix(ptr, "/") || seen[name] {
			return nil, fmt.Errorf("invalid secret_refs entry %q; expected NAME=secrets/file.sops.yaml#/json-pointer", entry)
		}
		seen[name] = true
		refs = append(refs, sops.SecretRef{Name: name, SOPSFile: path, SOPSKey: ptr})
	}
	return refs, nil
}

func resolveFrontmatterSecrets(k *kb.KB, fm *okf.Frontmatter) (map[string]string, string, bool, error) {
	var refs []sops.SecretRef
	if raw, ok := fm.Get("secret_refs"); ok {
		var err error
		refs, err = parseSecretRefs(raw)
		if err != nil {
			return nil, "", false, err
		}
	}
	if k.SopsAgeKeyFile == "" {
		return nil, "", false, fmt.Errorf("requires a sops_age_key_file configured for this KB")
	}
	if !sops.Available() {
		return nil, "", false, fmt.Errorf("sops binary not found in PATH")
	}
	if refs != nil {
		values, err := sops.ResolveRefs(k.Root, refs, sops.AgeKeyEnv(k.SopsAgeKeyFile)...)
		return values, "declared refs", false, err
	}
	raw, ok := fm.Get("secrets_source")
	if !ok {
		return nil, "", false, fmt.Errorf("concept has no secret_refs or secrets_source")
	}
	source, ok := raw.(string)
	if !ok || source == "" || !filepath.IsLocal(source) || !strings.HasPrefix(source, "secrets/") || !strings.HasSuffix(source, ".sops.yaml") {
		return nil, "", false, fmt.Errorf("secrets_source must be a path inside the KB under secrets/*.sops.yaml")
	}
	sf, err := sops.Decrypt(k.Root, source, sops.AgeKeyEnv(k.SopsAgeKeyFile)...)
	if err != nil {
		return nil, "", false, err
	}
	return sf.Values, source, true, nil
}

func toolSecretResolve(k *kb.KB) Tool {
	return Tool{Name: "secret_resolve", Description: "Resolves declared SOPS secret_refs for any concept (requires rw scope). Returns the key names with values redacted; pass reveal: true to return the values, which then appear in the transcript and in any log that captures it.", InputSchema: json.RawMessage(`{"type":"object","required":["concept_id"],"properties":{"concept_id":{"type":"string"},"names":{"type":"array","items":{"type":"string"}},"reveal":{"type":"boolean","description":"Return the secret values instead of <redacted>; they will appear in the transcript"}}}`), Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		var p struct {
			ConceptID string   `json:"concept_id"`
			Names     []string `json:"names"`
			Reveal    bool     `json:"reveal"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid params: " + err.Error()), nil
		}
		data, err := k.ReadConcept(okf.ConceptID(p.ConceptID))
		if err != nil {
			return errorResult(fmt.Sprintf("secret_resolve %q: %v", p.ConceptID, err)), nil
		}
		fm, err := okf.ParseFrontmatter(data.FrontmatterRaw)
		if err != nil {
			return errorResult("secret_resolve: parse frontmatter: " + err.Error()), nil
		}
		values, _, _, err := resolveFrontmatterSecrets(k, fm)
		if err != nil {
			return errorResult("secret_resolve: " + err.Error()), nil
		}
		wanted := map[string]bool{}
		for _, n := range p.Names {
			wanted[n] = true
		}
		var sb strings.Builder
		keys := make([]string, 0, len(values))
		for n := range values {
			if len(wanted) == 0 || wanted[n] {
				keys = append(keys, n)
			}
		}
		sort.Strings(keys)
		// Redacted by default (D158): verifying that resolution works must not
		// require printing a credential into an agent transcript, and a safe
		// behaviour that has to be opted into is not the safe default.
		for _, n := range keys {
			if p.Reveal {
				sb.WriteString(n + "=" + values[n] + "\n")
				continue
			}
			sb.WriteString(n + "=<redacted>\n")
		}
		return textResult(strings.TrimSuffix(sb.String(), "\n")), nil
	}}
}

func toolSecretSet(k *kb.KB) Tool {
	return Tool{Name: "secret_set", Description: "Sets one JSON Pointer in an existing encrypted SOPS file (requires rw scope).", InputSchema: json.RawMessage(`{"type":"object","required":["path","key","value"],"properties":{"path":{"type":"string"},"key":{"type":"string"},"value":{"type":"string"}}}`), Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		var p struct {
			Path  string `json:"path"`
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid params: " + err.Error()), nil
		}
		if !filepath.IsLocal(p.Path) || !strings.HasPrefix(p.Path, "secrets/") || !strings.HasSuffix(p.Path, ".sops.yaml") {
			return errorResult("secret_set: path must be a local secrets/*.sops.yaml path"), nil
		}
		if _, err := os.Stat(filepath.Join(k.Root, p.Path)); os.IsNotExist(err) {
			return errorResult("secret_set: file does not exist; bootstrapping an encrypted file and its recipients is an operator action"), nil
		}
		if k.SopsAgeKeyFile == "" {
			return errorResult("secret_set: requires a sops_age_key_file configured for this KB"), nil
		}
		if err := sops.Set(k.Root, p.Path, p.Key, p.Value, sops.AgeKeyEnv(k.SopsAgeKeyFile)...); err != nil {
			return errorResult("secret_set: " + err.Error()), nil
		}
		return textResult(fmt.Sprintf("secret_set: updated %s %s", p.Path, p.Key)), nil
	}}
}

// --- service_list ---

func toolServiceList(k *kb.KB) Tool {
	return Tool{
		Name:        "service_list",
		Description: "Lists all concepts of type Service in the KB.",
		ReadOnly:    true,
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			type svcEntry struct {
				ID     string `json:"id"`
				Title  string `json:"title,omitempty"`
				Status string `json:"status,omitempty"`
			}
			var services []svcEntry

			err := k.WalkConcepts(func(id okf.ConceptID, content string) error {
				fmRaw, _, _ := okf.SplitFrontmatter(content)
				fm, err := okf.ParseFrontmatter(fmRaw)
				if err != nil {
					return nil
				}
				if !strings.EqualFold(fm.Type(), "Service") {
					return nil
				}
				e := svcEntry{ID: string(id)}
				if v, ok := fm.Get("title"); ok {
					e.Title, _ = v.(string)
				}
				if v, ok := fm.Get("status"); ok {
					e.Status, _ = v.(string)
				}
				services = append(services, e)
				return nil
			})
			if err != nil {
				return errorResult(fmt.Sprintf("service_list: walk: %v", err)), nil
			}

			if len(services) == 0 {
				return textResult("No Service concepts found."), nil
			}

			var sb strings.Builder
			for _, s := range services {
				sb.WriteString(fmt.Sprintf("- %s", s.ID))
				if s.Title != "" {
					sb.WriteString(": " + s.Title)
				}
				if s.Status != "" {
					sb.WriteString(" [" + s.Status + "]")
				}
				sb.WriteByte('\n')
			}
			return textResult(strings.TrimRight(sb.String(), "\n")), nil
		},
	}
}

// --- skill_list (bundle-aware) ---

func toolSkillListWithBundle(k *kb.KB, bundleFS fs.FS) Tool {
	return Tool{
		Name:        "skill_list",
		Description: "Lists installed skills and available bundled skills. Source is [installed] or [bundled].",
		ReadOnly:    true,
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			installed, _ := skill.LoadAllSkills(k.Root)
			installedNames := make(map[string]bool, len(installed))
			for _, s := range installed {
				installedNames[s.Name] = true
			}

			bundled, _ := skill.LoadAllFromFS(bundleFS, "bundled")

			if len(installed) == 0 && len(bundled) == 0 {
				return textResult("No skills found."), nil
			}

			var sb strings.Builder
			for _, e := range skill.Catalog(installed) {
				sb.WriteString(fmt.Sprintf("[installed] %s", e.Name))
				if e.Version != "" {
					sb.WriteString(" v" + e.Version)
				}
				sb.WriteString(" (" + e.Path + ")")
				if e.Description != "" {
					sb.WriteString(": " + e.Description)
				}
				sb.WriteByte('\n')
			}
			for _, e := range skill.Catalog(bundled) {
				if installedNames[e.Name] {
					continue // already listed as installed
				}
				sb.WriteString(fmt.Sprintf("[bundled]   %s", e.Name))
				if e.Version != "" {
					sb.WriteString(" v" + e.Version)
				}
				sb.WriteString(" (" + e.Path + ")")
				if e.Description != "" {
					sb.WriteString(": " + e.Description)
				}
				sb.WriteByte('\n')
			}
			return textResult(strings.TrimRight(sb.String(), "\n")), nil
		},
	}
}

// --- skill_install ---

func toolSkillInstall(k *kb.KB, bundleFS fs.FS) Tool {
	return Tool{
		Name:        "skill_install",
		Description: "Installs a bundled skill into the KB's skills/ directory.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["name"],
			"properties": {
				"name": {
					"type": "string",
					"description": "Name of the bundled skill to install (e.g. kb-create)"
				},
				"force": {
					"type": "boolean",
					"description": "Overwrite if already installed (default false)"
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Name  string `json:"name"`
				Force bool   `json:"force"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.Name == "" {
				return errorResult("'name' is required"), nil
			}

			srcDir := "bundled/" + params.Name
			// Verify the bundled skill exists.
			if _, err := fs.Stat(bundleFS, srcDir); err != nil {
				return errorResult(fmt.Sprintf("unknown bundled skill %q", params.Name)), nil
			}

			dstDir := filepath.Join(k.Root, "skills", params.Name)
			if _, err := os.Stat(dstDir); err == nil && !params.Force {
				return errorResult(fmt.Sprintf("skill %q already installed, use force=true to overwrite", params.Name)), nil
			}

			if err := os.MkdirAll(dstDir, 0o755); err != nil {
				return errorResult(fmt.Sprintf("skill_install: mkdir %s: %v", dstDir, err)), nil
			}

			// Recursively copy all files from srcDir into dstDir.
			if err := fs.WalkDir(bundleFS, srcDir, func(path string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if path == srcDir {
					return nil // skip root dir itself
				}
				rel := path[len(srcDir)+1:] // relative path within skill dir
				dstPath := filepath.Join(dstDir, filepath.FromSlash(rel))
				if d.IsDir() {
					return os.MkdirAll(dstPath, 0o755)
				}
				data, err := fs.ReadFile(bundleFS, path)
				if err != nil {
					return err
				}
				return os.WriteFile(dstPath, data, 0o644)
			}); err != nil {
				return errorResult(fmt.Sprintf("skill_install: copy: %v", err)), nil
			}

			result := map[string]interface{}{
				"skill":  params.Name,
				"status": "installed",
				"path":   "skills/" + params.Name + "/",
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}
