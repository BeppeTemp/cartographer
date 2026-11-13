package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/hooks"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/skill"
	"github.com/BeppeTemp/cartographer/internal/skillbundle"
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
		Description: "Reads a concept of type Service. Returns frontmatter (YAML) and body. With resolve_secrets=true, also decrypts the service's secrets and lists their key names with values redacted (requires rw scope); pass reveal: true as well to return the values, which then appear in the transcript and in any log that captures it.",
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
					"description": "If true, resolve the service's secret_refs (or decrypt its secrets_source, flat SOPS files) and include the resolved secrets in the result. Requires rw scope, and for SOPS a KB with an age key (<kb>.age or default.age in the server's credentials_dir); a hook reference is asked of its resolver hook. Default false."
				},
				"reveal": {
					"type": "boolean",
					"description": "With resolve_secrets=true, return the secret values instead of <redacted>; they will appear in the transcript. Ignored without resolve_secrets. Default false."
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				ServiceID      string `json:"service_id"`
				ResolveSecrets bool   `json:"resolve_secrets"`
				Reveal         bool   `json:"reveal"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.ServiceID == "" {
				return errorResult("'service_id' is required"), nil
			}

			data, err := k.ReadConcept(okf.ConceptID(params.ServiceID))
			if err != nil {
				return errorResult(fmt.Sprintf("service_get %q: %v", params.ServiceID, relPathErr(err, k.Root))), nil
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
			if !kb.IsServiceType(fm.Type()) {
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

			res, err := resolveFrontmatterSecrets(ctx, k, params.ServiceID, fm, nil)
			if err != nil {
				return withResultAudit(errorResult("service_get: resolve_secrets: "+err.Error()), res.audit), nil
			}
			values, source, legacy := res.values, res.source, res.legacy
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
			// Redacted unless revealed, as secret_resolve (D158, D261): the only
			// caller of this tool is an MCP client, so whatever is written here
			// lands in an agent transcript.
			for _, key := range keys {
				sb.WriteString(key)
				sb.WriteString("=")
				if params.Reveal {
					sb.WriteString(values[key])
				} else {
					sb.WriteString("<redacted>")
				}
				sb.WriteByte('\n')
			}
			return withResultAudit(textResult(sb.String()), res.audit), nil
		},
	}
}

// withResultAudit attaches audit resources to a result (ToolResult.AuditResources).
func withResultAudit(r ToolResult, extra map[string]string) ToolResult {
	r.AuditResources = extra
	return r
}

// errNoAgeKey says what is missing and the command that creates it; the server
// path of the credentials directory is not the caller's to know.
func errNoAgeKey(k *kb.KB) error {
	n := kbName(k)
	return fmt.Errorf("requires an age key for this KB: the operator creates it with `age-keygen -o <credentials_dir>/%s.age` (or default.age for every KB), then adds its public key to the KB's .sops.yaml", n)
}

var secretRefName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// parseSecretRefs reads secret_refs into the sops references and the
// references a resolver hook serves (NAME=hook:<plugin>/<name>#<key>, D1987).
// A NAME is declared once across both; a hook reference must name a
// secret_resolver configured on this server.
func parseSecretRefs(raw any, reg *hooks.Registry) ([]sops.SecretRef, []hooks.SecretRef, error) {
	entries, ok := raw.([]string)
	if !ok || len(entries) == 0 {
		return nil, nil, fmt.Errorf("secret_refs must be a non-empty list of NAME=secrets/file.sops.yaml#/json-pointer")
	}
	refs := make([]sops.SecretRef, 0, len(entries))
	var hookRefs []hooks.SecretRef
	seen := map[string]bool{}
	for _, entry := range entries {
		eq, hash := strings.Index(entry, "="), strings.Index(entry, "#")
		if eq <= 0 || hash <= eq+1 || hash == len(entry)-1 {
			return nil, nil, fmt.Errorf("invalid secret_refs entry %q; expected NAME=secrets/file.sops.yaml#/json-pointer", entry)
		}
		name, path, ptr := entry[:eq], entry[eq+1:hash], entry[hash+1:]
		if strings.HasPrefix(path, hooks.SecretRefPrefix) && secretRefName.MatchString(name) && !seen[name] {
			ref, err := parseHookSecretRef(entry, name, path, ptr, reg)
			if err != nil {
				return nil, nil, err
			}
			seen[name] = true
			hookRefs = append(hookRefs, ref)
			continue
		}
		if strings.TrimSpace(name) != name || strings.TrimSpace(path) != path || strings.TrimSpace(ptr) != ptr || !secretRefName.MatchString(name) || !isSecretsPath(path) || !strings.HasPrefix(ptr, "/") || seen[name] {
			return nil, nil, fmt.Errorf("invalid secret_refs entry %q; expected NAME=secrets/file.sops.yaml#/json-pointer", entry)
		}
		seen[name] = true
		refs = append(refs, sops.SecretRef{Name: name, SOPSFile: path, SOPSKey: ptr})
	}
	return refs, hookRefs, nil
}

// secretError turns an error of the sops package into the one a caller may
// see: the full detail goes to the server log, the caller gets no server path
// nor sops diagnostics, and a failed decryption is named for what it is (the
// KB's age key).
func secretError(k *kb.KB, err error) error {
	if err == nil {
		return nil
	}
	log.Printf("secrets: %s: %s", kbName(k), sops.Detail(err))
	if errors.Is(err, sops.ErrDecrypt) {
		return fmt.Errorf("age key missing or invalid for %s: %w", kbName(k), err)
	}
	return err
}

// resolvedSecrets is what resolveFrontmatterSecrets returns: the values, where
// they come from, whether the legacy whole-file form answered, and the audit
// resources of the resolution (the resolver hooks asked, never a value).
type resolvedSecrets struct {
	values map[string]string
	source string
	legacy bool
	audit  map[string]string
}

// resolveFrontmatterSecrets resolves the concept's secrets. A non-empty names
// restricts the resolution to those names; one the concept does not declare is
// an error ("unknown name"), whatever the other references are. The references
// a resolver hook serves are asked of it (outside the KB lock: both callers are
// lock-free reads); a concept whose names all go to hooks needs no age key.
func resolveFrontmatterSecrets(ctx requestContext, k *kb.KB, conceptID string, fm *okf.Frontmatter, names []string) (resolvedSecrets, error) {
	var refs []sops.SecretRef
	var hookRefs []hooks.SecretRef
	if raw, ok := fm.Get("secret_refs"); ok {
		var err error
		refs, hookRefs, err = parseSecretRefs(raw, hookRegistryFrom(ctx))
		if err != nil {
			return resolvedSecrets{}, err
		}
		if refs == nil {
			refs = []sops.SecretRef{} // declared refs, even when all are hook refs
		}
	}
	if refs != nil && len(hookRefs) > 0 {
		return resolveDeclaredWithHooks(ctx, k, conceptID, refs, hookRefs, names)
	}
	keyFile := k.SopsAgeKey()
	if keyFile == "" {
		return resolvedSecrets{}, errNoAgeKey(k)
	}
	if !sops.Available() {
		return resolvedSecrets{}, fmt.Errorf("sops binary not found in PATH")
	}
	if refs != nil {
		values, err := sops.ResolveNamed(k.Root, refs, names, sops.AgeKeyEnv(keyFile)...)
		return resolvedSecrets{values: values, source: "declared refs"}, secretError(k, err)
	}
	raw, ok := fm.Get("secrets_source")
	if !ok {
		return resolvedSecrets{}, fmt.Errorf("concept has no secret_refs or secrets_source")
	}
	sources, ok := secretsSources(raw)
	if !ok {
		return resolvedSecrets{}, fmt.Errorf("secrets_source must be a path inside the KB under secrets/*.sops.yaml, or a list of them")
	}
	values := map[string]string{}
	owner := map[string]string{}
	for _, source := range sources {
		sf, err := sops.Decrypt(k.Root, source, sops.AgeKeyEnv(keyFile)...)
		if err != nil {
			return resolvedSecrets{}, secretError(k, err)
		}
		for key, v := range sf.Values {
			if first, dup := owner[key]; dup {
				return resolvedSecrets{}, fmt.Errorf("secrets_source: key %q is in both %s and %s", key, first, source)
			}
			owner[key] = source
			values[key] = v
		}
	}
	for _, n := range names {
		if _, ok := values[n]; !ok {
			return resolvedSecrets{}, fmt.Errorf("unknown name %q", n)
		}
	}
	return resolvedSecrets{values: values, source: strings.Join(sources, ", "), legacy: true}, nil
}

// resolveDeclaredWithHooks resolves declared refs of which some are served by
// a resolver hook: names are split between sops and the hooks, and sops (with
// its age key) runs only when a sops reference is wanted.
func resolveDeclaredWithHooks(ctx requestContext, k *kb.KB, conceptID string, refs []sops.SecretRef, hookRefs []hooks.SecretRef, names []string) (resolvedSecrets, error) {
	wantSops, wantHooks := len(refs) > 0, hookRefs
	var sopsNames []string
	if len(names) > 0 {
		byName := map[string]hooks.SecretRef{}
		for _, r := range hookRefs {
			byName[r.Name] = r
		}
		declared := map[string]bool{}
		for _, r := range refs {
			declared[r.Name] = true
		}
		wantHooks = nil
		asked := map[string]bool{}
		for _, n := range names {
			switch r, isHook := byName[n]; {
			case asked[n]:
			case isHook:
				wantHooks = append(wantHooks, r)
			case declared[n]:
				sopsNames = append(sopsNames, n)
			default:
				return resolvedSecrets{}, fmt.Errorf("unknown name %q", n)
			}
			asked[n] = true
		}
		wantSops = len(sopsNames) > 0
	}
	values := map[string]string{}
	if wantSops {
		keyFile := k.SopsAgeKey()
		if keyFile == "" {
			return resolvedSecrets{}, errNoAgeKey(k)
		}
		if !sops.Available() {
			return resolvedSecrets{}, fmt.Errorf("sops binary not found in PATH")
		}
		v, err := sops.ResolveNamed(k.Root, refs, sopsNames, sops.AgeKeyEnv(keyFile)...)
		if err != nil {
			return resolvedSecrets{}, secretError(k, err)
		}
		values = v
	}
	out := resolvedSecrets{values: values, source: "declared refs"}
	if len(wantHooks) == 0 {
		return out, nil
	}
	out.audit = map[string]string{"secret_resolver": resolverAuditValue(wantHooks)}
	hv, err := hookRegistryFrom(ctx).ResolveSecrets(ctx, hooks.SecretQuery{KB: kbName(k), ConceptID: conceptID, Principal: hookPrincipal(ctx)}, wantHooks)
	if err != nil {
		return out, err
	}
	for n, v := range hv {
		values[n] = v
	}
	return out, nil
}

// secretsSources reads the secrets_source field: a list of KB-relative sops
// files (the shape lint enforces), or a plain string read as a one-element
// list. ok is false for any other shape, an empty list, or a path outside
// secrets/*.sops.yaml; the caller words the error without a server path.
func secretsSources(raw any) (sources []string, ok bool) {
	switch v := raw.(type) {
	case string:
		sources = []string{v}
	case []string:
		sources = v
	case []any:
		for _, e := range v {
			s, isStr := e.(string)
			if !isStr {
				return nil, false
			}
			sources = append(sources, s)
		}
	}
	if len(sources) == 0 {
		return nil, false
	}
	for _, s := range sources {
		if s == "" || !isSecretsPath(s) {
			return nil, false
		}
	}
	return sources, true
}

func toolSecretResolve(k *kb.KB) Tool {
	return Tool{Name: "secret_resolve", Description: "Resolves declared secret_refs (SOPS, or a resolver hook) for any concept (requires rw scope). Returns the key names with values redacted; pass reveal: true to return the values, which then appear in the transcript and in any log that captures it.", InputSchema: json.RawMessage(`{"type":"object","required":["concept_id"],"properties":{"concept_id":{"type":"string"},"names":{"type":"array","items":{"type":"string"}},"reveal":{"type":"boolean","description":"Return the secret values instead of <redacted>; they will appear in the transcript"}}}`), Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		var p struct {
			ConceptID string   `json:"concept_id"`
			Names     []string `json:"names"`
			Reveal    bool     `json:"reveal"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid params: " + err.Error()), nil
		}
		data, err := k.ReadConcept(okf.ConceptID(p.ConceptID))
		if errors.Is(err, okf.ErrInvalidPath) {
			return errorResult(fmt.Sprintf("secret_resolve: concept_id %q is not the id of a concept that declares secret_refs; a reference (NAME=secrets/file.sops.yaml#/pointer) is declared in that concept's secret_refs, not passed here", p.ConceptID)), nil
		}
		if err != nil {
			return errorResult(fmt.Sprintf("secret_resolve %q: %v", p.ConceptID, err)), nil
		}
		fm, err := okf.ParseFrontmatter(data.FrontmatterRaw)
		if err != nil {
			return errorResult("secret_resolve: parse frontmatter: " + err.Error()), nil
		}
		res, err := resolveFrontmatterSecrets(ctx, k, p.ConceptID, fm, p.Names)
		if err != nil {
			return withResultAudit(errorResult("secret_resolve: "+err.Error()), res.audit), nil
		}
		values := res.values
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
		return withResultAudit(textResult(strings.TrimSuffix(sb.String(), "\n")), res.audit), nil
	}}
}

// suggestSopsPath proposes the path secret_set expects for a refused one.
func suggestSopsPath(p string) string {
	base := path.Base(strings.ReplaceAll(p, "\\", "/"))
	for _, ext := range []string{".sops.yaml", ".yaml", ".yml"} {
		base = strings.TrimSuffix(base, ext)
	}
	if base == "" || base == "." || base == "/" {
		base = "name"
	}
	return "secrets/" + base + ".sops.yaml"
}

func toolSecretSet(k *kb.KB) Tool {
	return Tool{Name: "secret_set", Description: "Sets one JSON Pointer in an encrypted SOPS file under secrets/ (requires rw scope). A new file is created only when the KB's .sops.yaml creation rule matches its path and the server's key is one of the rule's recipients; the value is never returned.", InputSchema: json.RawMessage(`{"type":"object","required":["path","key","value"],"properties":{"path":{"type":"string"},"key":{"type":"string"},"value":{"type":"string"}}}`), Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		var p struct {
			Path  string `json:"path"`
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid params: " + err.Error()), nil
		}
		if !isSecretsPath(p.Path) {
			return errorResult(fmt.Sprintf("secret_set: path %q must be a local secrets/<name>.sops.yaml path, for example %s", p.Path, suggestSopsPath(p.Path))), nil
		}
		_, statErr := os.Lstat(filepath.Join(k.Root, p.Path))
		creating := os.IsNotExist(statErr)
		keyFile := k.SopsAgeKey()
		if keyFile == "" {
			return errorResult("secret_set: " + errNoAgeKey(k).Error()), nil
		}
		if creating {
			// D1191: the recipients are the operator's creation rule, never the caller's.
			if err := sops.Create(k.Root, p.Path, p.Key, p.Value, keyFile, sops.AgeKeyEnv(keyFile)...); err != nil {
				if errors.Is(err, sops.ErrNoConfig) {
					return errorResult("secret_set: file does not exist and the KB has no .sops.yaml; bootstrapping encryption and its recipients is an operator action"), nil
				}
				return errorResult("secret_set: " + secretError(k, err).Error()), nil
			}
			return textResult(fmt.Sprintf("secret_set: created %s %s", p.Path, p.Key)), nil
		}
		if err := sops.Set(k.Root, p.Path, p.Key, p.Value, sops.AgeKeyEnv(keyFile)...); err != nil {
			return errorResult("secret_set: " + secretError(k, err).Error()), nil
		}
		return textResult(fmt.Sprintf("secret_set: updated %s %s", p.Path, p.Key)), nil
	}}
}

// toolSecretsInit writes the KB's root .sops.yaml creation rule (D2039).
// Admin only: the recipients decide who can decrypt the KB, an operator's choice
// (D1191) that no agent token may make. The key is not installed here: it is a
// file on the operator's machine and credentials_dir is the server's.
func toolSecretsInit(k *kb.KB) Tool {
	return Tool{Name: "secrets_init", Description: "Writes the KB's root .sops.yaml with one creation rule (secrets/*.sops.yaml encrypted to the given age public key) and commits it. Admin only. A different existing rule is kept unless force is set; a replaced rule does not re-encrypt the files already under secrets/.", InputSchema: json.RawMessage(`{"type":"object","required":["recipient"],"properties":{"recipient":{"type":"string","description":"age public key (age1...) of the operator who may decrypt"},"force":{"type":"boolean"}}}`), Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		var p struct {
			Recipient string `json:"recipient"`
			Force     bool   `json:"force"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid params: " + err.Error()), nil
		}
		if pr := auth.PrincipalFromContext(ctx); pr.ID != "" && !pr.Policy.Admin {
			return errorResult("secrets_init: choosing who may decrypt the KB is an administrator action"), nil
		}
		state, previous, err := sops.InitRule(k.Root, strings.TrimSpace(p.Recipient), p.Force)
		if err != nil {
			var exists *sops.RuleExistsError
			if errors.As(err, &exists) {
				return errorResult(fmt.Sprintf("secrets_init: the KB already has a different .sops.yaml (recipients: %s); pass force to replace it", strings.Join(previous, ", "))), nil
			}
			return errorResult("secrets_init: " + relPathErr(err, k.Root)), nil
		}
		msg := "secrets_init: rule " + state
		res := textResult(msg)
		if state == sops.RuleReplaced {
			res = textResult(msg + "; files already under secrets/ keep their old recipients: run sops updatekeys on each")
		}
		if state != sops.RuleUnchanged {
			res.CommitSubject = "chore(secrets): enable secrets, creation rule for 1 recipient"
		}
		return res, nil
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

			visible := visiblePredicate(ctx, k)
			err := k.WalkConcepts(func(id okf.ConceptID, content string) error {
				if !visible(string(id)) {
					return nil
				}
				fmRaw, _, _ := okf.SplitFrontmatter(content)
				fm, err := okf.ParseFrontmatter(fmRaw)
				if err != nil {
					return nil
				}
				if !kb.IsServiceType(fm.Type()) {
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
				return errorResult(fmt.Sprintf("service_list: walk: %v", relPathErr(err, k.Root))), nil
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
					"description": "Name of the bundled skill to install (e.g. cartographer-kb-create)"
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
			if !artifactSlugPattern.MatchString(params.Name) {
				return errorResult(fmt.Sprintf("skill_install: invalid name %q: a skill name is a lowercase-hyphenated slug", params.Name)), nil
			}

			// A name the bundle no longer carries but knew before D958 installs
			// under its current name; the exact name is tried first.
			requested := params.Name
			if _, err := fs.Stat(bundleFS, "bundled/"+params.Name); err != nil {
				if current, ok := skillbundle.Renamed(params.Name); ok {
					params.Name = current
				}
			}
			srcDir := "bundled/" + params.Name
			// Verify the bundled skill exists.
			if _, err := fs.Stat(bundleFS, srcDir); err != nil {
				return errorResult(fmt.Sprintf("unknown bundled skill %q", requested)), nil
			}

			// Every destination is inside the KB: no component of skills/<name>,
			// nor of a file the skill writes, is a symlink (a KB comes from a
			// remote that can track one). All are checked before the first write.
			skillRel := "skills/" + params.Name
			if err := rejectArtifactSymlinks(k.Root, skillRel); err != nil {
				return errorResult("skill_install: " + err.Error()), nil
			}
			dstDir := filepath.Join(k.Root, "skills", params.Name)
			if _, err := os.Lstat(dstDir); err == nil && !params.Force {
				return errorResult(fmt.Sprintf("skill %q already installed, use force=true to overwrite", params.Name)), nil
			}
			type planned struct {
				path, rel string
				dir       bool
			}
			var plan []planned // the bundle below srcDir, parents first
			if err := fs.WalkDir(bundleFS, srcDir, func(path string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if path == srcDir {
					return nil // skip root dir itself
				}
				rel := path[len(srcDir)+1:]
				if err := rejectArtifactSymlinks(k.Root, skillRel+"/"+rel); err != nil {
					return err
				}
				plan = append(plan, planned{path: path, rel: rel, dir: d.IsDir()})
				return nil
			}); err != nil {
				return errorResult("skill_install: " + relPathErr(err, k.Root)), nil
			}

			if err := os.MkdirAll(dstDir, 0o755); err != nil {
				return errorResult(fmt.Sprintf("skill_install: mkdir skills/%s: %v", params.Name, relPathErr(err, k.Root))), nil
			}

			// Recursively copy all files from srcDir into dstDir.
			for _, p := range plan {
				dstPath := filepath.Join(dstDir, filepath.FromSlash(p.rel))
				if p.dir {
					if err := os.MkdirAll(dstPath, 0o755); err != nil {
						return errorResult(fmt.Sprintf("skill_install: copy: %v", relPathErr(err, k.Root))), nil
					}
					continue
				}
				data, err := fs.ReadFile(bundleFS, p.path)
				if err != nil {
					return errorResult(fmt.Sprintf("skill_install: copy: %v", relPathErr(err, k.Root))), nil
				}
				if err := os.WriteFile(dstPath, data, 0o644); err != nil {
					return errorResult(fmt.Sprintf("skill_install: copy: %v", relPathErr(err, k.Root))), nil
				}
			}

			result := map[string]interface{}{
				"skill":  params.Name,
				"status": "installed",
				"path":   "skills/" + params.Name + "/",
			}
			if requested != params.Name {
				result["renamed_from"] = requested
				result["note"] = fmt.Sprintf("%q is now %q: the skill was installed under its current name", requested, params.Name)
			}
			return jsonResult(result), nil
		},
	}
}

// isSecretsPath reports whether p names secrets/<...>.sops.yaml in canonical
// form (the test is sops.InSecretsDir, shared with the lint checks).
func isSecretsPath(p string) bool {
	return sops.InSecretsDir(p) && strings.HasSuffix(p, ".sops.yaml")
}
