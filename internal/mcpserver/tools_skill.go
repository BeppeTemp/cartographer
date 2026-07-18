package mcpserver

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
		Handler: func(args json.RawMessage) (ToolResult, error) {
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
		// expressed here (Tool.ReadOnly is a per-tool-name classification
		// consulted by the HTTP guard before arguments are known) but as a
		// special case in mcpAccessGuard (httpserver.go, D47) that inspects
		// arguments.resolve_secrets directly.
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
		Handler: func(args json.RawMessage) (ToolResult, error) {
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

			if fm.Type() != "Service" {
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

			// secrets_source is a flat string field (frontmatter supports only
			// string/[]string — okf/frontmatter.go — so per-ref least-privilege
			// via secret_refs is not parsable; the whole file is decrypted).
			raw, ok := fm.Get("secrets_source")
			if !ok {
				return errorResult(fmt.Sprintf("service_get: %q has no secrets_source", params.ServiceID)), nil
			}
			secretsSource, ok := raw.(string)
			if !ok || secretsSource == "" {
				return errorResult(fmt.Sprintf("service_get: %q secrets_source is not a non-empty string", params.ServiceID)), nil
			}
			// secrets_source must stay inside the KB root: reject absolute paths
			// and "../" traversal so a crafted frontmatter can't decrypt files
			// outside the KB with the KB's age key (defence in depth: also gated rw).
			if !filepath.IsLocal(secretsSource) {
				return errorResult(fmt.Sprintf("service_get: secrets_source %q must be a path inside the KB (no absolute paths or '..')", secretsSource)), nil
			}
			if k.SopsAgeKeyFile == "" {
				return errorResult("service_get: resolve_secrets requires a sops_age_key_file configured for this KB"), nil
			}
			if !sops.Available() {
				return errorResult("service_get: sops binary not found in PATH"), nil
			}

			sf, err := sops.Decrypt(filepath.Join(k.Root, secretsSource), sops.AgeKeyEnv(k.SopsAgeKeyFile)...)
			if err != nil {
				return errorResult(fmt.Sprintf("service_get: resolve_secrets: %v", err)), nil
			}

			sb.WriteString("\n\n---\nsecrets (from ")
			sb.WriteString(secretsSource)
			sb.WriteString("):\n")
			for key, val := range sf.Values {
				sb.WriteString(key)
				sb.WriteString("=")
				sb.WriteString(val)
				sb.WriteByte('\n')
			}
			return textResult(sb.String()), nil
		},
	}
}

// --- service_list ---

func toolServiceList(k *kb.KB) Tool {
	return Tool{
		Name:        "service_list",
		Description: "Lists all concepts of type Service in the KB.",
		ReadOnly:    true,
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
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
				if fm.Type() != "Service" {
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
		Handler: func(args json.RawMessage) (ToolResult, error) {
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
		Handler: func(args json.RawMessage) (ToolResult, error) {
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
