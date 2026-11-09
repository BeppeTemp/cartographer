package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/worker/connector"
	"github.com/BeppeTemp/cartographer/internal/worker/feed"
	"github.com/BeppeTemp/cartographer/internal/worker/poll"
	"github.com/BeppeTemp/cartographer/internal/worker/recipe"
)

// The poll task kind (D1262): it turns what a feed brought into IngestUnits.
// Listing needs no model; the one model call, per NEW unit, returns data that
// this code validates and writes. Nothing is kept locally: the cursor and the
// lease live in the feed Source (package feed), the listing is per call, and
// the unit is the idempotency token.

const (
	pollKindName = "poll"
	// pollMaxOutput caps the answer of the one model call.
	pollMaxOutput = 2000
	// pollListTimeout bounds one recipe listing.
	pollListTimeout = 15 * time.Minute
	pollFeedMax     = 500
)

// Codes of feed_error written by the poll (package feed adds bad_field).
const (
	codeRecipeUnknown = "recipe_unknown"
	codeRecipeShape   = "recipe_shape"
	codeNoPlan        = "no_ingest_plan"
	codeAmbiguousPlan = "ambiguous_ingest_plan"
	codeListFailed    = "list_failed"
	codeProtocol      = "protocol_error"
	codeUnitRefused   = "unit_refused"
	codeGrant         = "grant_exhausted"
)

// builtinConnector is the connector a document Source of a built-in recipe names.
var builtinConnector = map[string]string{"git-docs": "git"}

// PollKind is the registration of the poll task kind.
func PollKind() Kind {
	return Kind{
		Name:    pollKindName,
		Options: []KindOption{schemaOption("default_interval"), schemaOption("min_interval"), schemaOption("max_entries")},
		Validate: func(o map[string]any) error {
			def, _ := o["default_interval"].(time.Duration)
			min, _ := o["min_interval"].(time.Duration)
			if min <= 0 {
				return errors.New("min_interval must be above zero")
			}
			if def < min {
				return errors.New("default_interval must be at least min_interval")
			}
			if n, _ := o["max_entries"].(int); n < 1 {
				return errors.New("max_entries must be at least 1")
			}
			return nil
		},
		Build: func(o map[string]any) (Instance, error) {
			def, _ := o["default_interval"].(time.Duration)
			min, _ := o["min_interval"].(time.Duration)
			n, _ := o["max_entries"].(int)
			return &pollInstance{def: def, min: min, maxEntries: n, now: time.Now}, nil
		},
	}
}

type pollInstance struct {
	def, min   time.Duration
	maxEntries int
	now        func() time.Time

	reg    *recipe.Registry
	regErr error
	hosts  map[string][]string // built-in recipe -> its allowed hosts
	secret map[string]string   // connector credential variables, for redaction
	clone  recipe.CloneFunc    // nil = https; tests inject a fixture
	// connector is the connector a document Source of each recipe names: git for
	// git-docs, the recipe's own for an exec recipe.
	connector map[string]string
}

// NewRecipeRegistry is the one builder of the recipe registry from a loaded
// config: the poll task and any dry run of a recipe use it, so they see the same
// recipes. clone nil = https.
func NewRecipeRegistry(cfg *Config, clone recipe.CloneFunc) (*recipe.Registry, error) {
	f := &fetcher{allowPrivate: cfg.FetchAllowPrivate}
	return recipe.NewRegistry(recipe.Options{
		GitHosts: cfg.GitAllowHosts, Instructions: cfg.RecipeInstructions, Clone: clone,
		FetchHosts: cfg.FetchAllowHosts, InsecureHTTP: cfg.FetchAllowInsecureHTTP, Get: f.page,
		Exec: cfg.ExecRecipes,
	})
}

// Bind builds the recipe registry from the config.
func (in *pollInstance) Bind(cfg *Config, _ Profile) {
	in.reg, in.regErr = NewRecipeRegistry(cfg, in.clone)
	in.hosts = builtinHosts(cfg)
	in.secret = map[string]string{}
	in.connector = map[string]string{}
	for k, v := range builtinConnector {
		in.connector[k] = v
	}
	for _, x := range cfg.ExecRecipes {
		in.connector[x.Name] = x.Connector
		for k, v := range x.Env {
			in.secret["recipe-"+x.Name+"/"+k] = v
		}
	}
	for _, c := range cfg.Connectors {
		for _, m := range []map[string]string{c.Env, c.Headers} {
			for k, v := range m {
				in.secret[c.Name+"/"+k] = v
			}
		}
		if c.TokenEnv != "" {
			in.secret[c.Name+"/token"] = c.TokenEnv
		}
	}
}

// builtinHosts maps each built-in recipe to the host list its locator must match.
func builtinHosts(cfg *Config) map[string][]string {
	return map[string][]string{"git-docs": cfg.GitAllowHosts, "http-pages": cfg.FetchAllowHosts}
}

type pollItem struct {
	f      feed.Feed
	perr   error
	system bool // source_shape: system (D1372): listed after a preflight, diffed by the worker
}

type listOut struct {
	Results []struct {
		ID          string         `json:"id"`
		Fields      map[string]any `json:"fields"`
		ContentHash string         `json:"content_hash"`
	} `json:"results"`
	Truncated bool `json:"truncated"`
	Total     int  `json:"total"`
}

// conceptList asks concept_list and, when the answer says truncated, asks again
// with limit set to its total.
func conceptList(tools ToolCaller, args map[string]any) (listOut, error) {
	var l listOut
	for try := 0; try < 2; try++ {
		text, err := callOK(tools, "concept_list", args)
		if err != nil {
			return l, err
		}
		l = listOut{}
		if err := json.Unmarshal([]byte(text), &l); err != nil {
			return l, fmt.Errorf("concept_list: %w", err)
		}
		if !l.Truncated || l.Total <= 0 {
			break
		}
		args["limit"] = l.Total
	}
	return l, nil
}

// fields turns the JSON lists of a concept_list answer into the []string the
// feed package reads.
func plainFields(f map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range f {
		if l, ok := v.([]any); ok {
			ss := make([]string, 0, len(l))
			for _, x := range l {
				s, _ := x.(string)
				ss = append(ss, s)
			}
			out[k] = ss
			continue
		}
		out[k] = v
	}
	return out
}

func strList(v any) []string {
	switch l := v.(type) {
	case []any:
		var out []string
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return []string{l}
	}
	return nil
}

// Next offers the due feed checked longest ago. The runner's tried set is the
// "attempted in this run" set: a feed is due again only after feed_checked_at
// (which the lease and a failure both move) plus its interval.
func (in *pollInstance) Next(_ context.Context, _ string, tools ToolCaller) (*WorkItem, error) {
	if in.regErr != nil {
		return nil, in.regErr
	}
	now := in.now()
	var best *pollItem
	for _, q := range []struct {
		where  []string
		system bool
	}{{[]string{"type=Source", "source_shape=feed"}, false},
		// ingest_status: skipped is the operator's off switch for a system.
		{[]string{"type=Source", "source_shape=system", "ingest_status!=skipped"}, true}} {
		l, err := conceptList(tools, map[string]any{"where": q.where, "fields": feed.QueryFieldsWithHash(), "limit": pollFeedMax})
		if err != nil {
			return nil, err
		}
		for _, r := range l.Results {
			ent := feed.Entry{ID: r.ID, Fields: plainFields(r.Fields), ContentHash: r.ContentHash}
			f, perr := feed.Parse(ent, in.def, in.min)
			due := perr == nil && f.Due(now)
			if perr != nil {
				// A feed that does not parse is recorded as bad_field, at most once
				// per default interval.
				f = feed.Feed{ID: r.ID, ContentHash: r.ContentHash}
				if s, ok := ent.Fields[feed.KeyCheckedAt].(string); ok {
					f.CheckedAt, _ = time.Parse(time.RFC3339, s)
				}
				f.Interval = in.def
				due = f.Due(now)
			}
			if due && (best == nil || f.CheckedAt.Before(best.f.CheckedAt) || (f.CheckedAt.Equal(best.f.CheckedAt) && f.ID < best.f.ID)) {
				best = &pollItem{f: f, perr: perr, system: q.system}
			}
		}
	}
	if best == nil {
		return nil, nil
	}
	return &WorkItem{Key: best.f.ID, Data: *best}, nil
}

// pollRun is one feed's run and its counters.
type pollRun struct {
	in   *pollInstance
	rt   Runtime
	f    feed.Feed
	hash string // if_match of the next patch of the feed Source
	now  time.Time
	res  Result

	seen, sources, created, reused, skipped, removed int
	superseded                                       int
	code                                             string // feed_error written, when skipped
	summary                                          string // set by the system poll
}

// Run polls one feed.
func (in *pollInstance) Run(ctx context.Context, item *WorkItem, rt Runtime) (Result, error) {
	pi := item.Data.(pollItem)
	r := &pollRun{in: in, rt: rt, f: pi.f, hash: pi.f.ContentHash, now: in.now()}
	err := r.run(ctx, pi)
	r.res.Units = r.created + r.reused
	r.res.Summary = fmt.Sprintf("poll %s: %d entries, %d sources registered, %d units created, %d reused, %d already handled, %d removals seen", r.f.ID, r.seen, r.sources, r.created, r.reused, r.skipped, r.removed)
	if r.summary != "" && r.code == "" {
		r.res.Summary = r.summary
	}
	if r.code != "" {
		r.res.Summary = fmt.Sprintf("poll %s: skipped, %s", r.f.ID, r.code)
	}
	return r.res, err
}

func (r *pollRun) patch(args map[string]any, reason string) (string, error) {
	args["reason"] = reason
	text, err := callOK(r.rt.Tools, "concept_patch", args)
	if err != nil {
		if isStale(err) {
			return "", feed.ErrLeaseLost
		}
		return "", err
	}
	r.res.Commits++
	var o struct {
		Hash string `json:"content_hash"`
	}
	_ = json.Unmarshal([]byte(text), &o)
	return o.Hash, nil
}

// fail records why the feed was not polled. The detail may come from a recipe
// or a connector, so the values of the connectors' credential variables are
// replaced before it reaches the Source (feed_error is KB text).
func (r *pollRun) fail(code, detail string) error {
	for _, k := range sortedKeys(r.in.secret) {
		detail = connector.RedactEnv(detail, map[string]string{k: r.in.secret[k]})
	}
	args, err := r.f.Fail(r.hash, code, detail, r.now)
	if err != nil {
		return err
	}
	r.code = code
	if _, err := r.patch(args, "poll "+r.f.ID); err != nil && !errors.Is(err, feed.ErrLeaseLost) {
		return err
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	k := make([]string, 0, len(m))
	for n := range m {
		k = append(k, n)
	}
	sort.Strings(k)
	return k
}

func (r *pollRun) run(ctx context.Context, pi pollItem) error {
	in, f, tools := r.in, r.f, r.rt.Tools
	reason := "poll " + f.ID
	// 1. Preconditions, before any lease (feedPreconditions: the dry run of a
	// recipe calls the same function).
	rec, pf := feedPreconditions(in.reg, in.hosts, f, pi.perr, pi.system)
	if pf != nil {
		return r.fail(pf.Code, pf.Detail)
	}
	// 2. Lease: the loser of the compare-and-swap skips the feed silently.
	args, err := f.Lease(r.now)
	if err != nil {
		return err
	}
	if r.hash, err = r.patch(args, reason); err != nil {
		if errors.Is(err, feed.ErrLeaseLost) {
			r.code = "lease_lost"
			return nil
		}
		return err
	}
	// 3. Plan and unit map.
	plans, err := conceptList(tools, map[string]any{"where": []string{"type=IngestPlan", "sources=" + f.ID}, "fields": []string{"status", "maps"}})
	if err != nil {
		return err
	}
	switch len(plans.Results) {
	case 0:
		return r.fail(codeNoPlan, "no IngestPlan lists this feed in sources")
	case 1:
	default:
		return r.fail(codeAmbiguousPlan, fmt.Sprintf("%d IngestPlans list this feed in sources", len(plans.Results)))
	}
	planID, maps := plans.Results[0].ID, strList(plans.Results[0].Fields["maps"])
	units, err := conceptList(tools, map[string]any{"where": []string{"type=IngestUnit", "plan=" + planID}, "fields": []string{"status", "content_asset"}})
	if err != nil {
		return err
	}
	// have maps an existing unit ID to whether it is a staged unit of ours that
	// still lacks its content (WP7): draft with a content_asset.
	have := map[string]bool{}
	var ids []string
	for _, u := range units.Results {
		have[u.ID] = str(u.Fields, "status") == "draft" && str(u.Fields, "content_asset") != ""
		ids = append(ids, u.ID)
	}
	sort.Strings(ids)
	unitMap, _, _ := strings.Cut(planID, "/")
	if len(ids) > 0 {
		unitMap, _, _ = strings.Cut(ids[0], "/")
	}
	if !strings.Contains(planID, "/") && len(ids) == 0 {
		return r.fail(codeUnitRefused, "the plan is not inside a map, so there is no map for its units")
	}
	if pi.system {
		return r.runSystem(ctx, rec, planID, maps, unitMap)
	}
	// 4. List (no model; listFeed is shared with the dry run of a recipe).
	listing, pf, lerr := listFeed(ctx, rec, f, in.maxEntries)
	if lerr != nil {
		return lerr
	}
	if pf != nil {
		return r.fail(pf.Code, pf.Detail)
	}
	// 5. Entries, in listing order. The unit is the idempotency token: the order
	// per entry (unit exists -> skip; register the Source; reset it to pending;
	// create the unit) lets a crash anywhere re-list the same entries and
	// converge, and never repeats a model call for an entry that has its unit.
	plan, _ := readConcept(tools, planID)
	for _, e := range listing.Entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.seen++
		if e.Change == recipe.Removed {
			r.removed++ // never acted on: the worker deletes and archives nothing
			continue
		}
		locator := e.Locator
		if locator == "" {
			locator = f.Locator + "#" + url.PathEscape(e.Key)
		}
		id := poll.UnitID(unitMap, f.ID, locator, e.Revision, e.Key)
		if staged, exists := have[id]; exists && !(staged && e.Content != "") {
			r.skipped++ // handled; a staged unit with no content to redo it from stays inert
			continue
		} else if exists {
			// Resume a unit a crash left staged: redo the idempotent steps.
			opened, err := r.stage(id, e.Content, "poll "+f.ID)
			if err != nil {
				return r.fail(codeUnitRefused, "entry "+oneLineClip(e.Key, 80)+": "+oneLineClip(err.Error(), 200))
			}
			if opened {
				r.created++
			} else {
				r.skipped++
			}
			continue
		}
		stop, err := r.entry(ctx, rec, e, locator, id, unitMap, planID, plan.Body, maps)
		if err != nil || stop {
			return err
		}
	}
	// 7. Advance, only after every entry is handled (the cursor is opaque).
	args, err = f.Advance(r.hash, listing.NextCursor, in.now())
	if err != nil {
		return err
	}
	if _, err := r.patch(args, reason); err != nil && !errors.Is(err, feed.ErrLeaseLost) {
		return err
	}
	return nil
}

// pollFailure is a reason the poll does not handle a feed: the feed_error it
// writes.
type pollFailure struct{ Code, Detail string }

// feedPreconditions is step 1 of the poll, before any lease: the Source parsed,
// its recipe known and of the shape the Source declares (feed or system; the
// recipe's shape decides, a mismatch either way is recipe_shape), a built-in's
// locator on its host list. The dry run of a recipe (SourceTest) calls this same
// function, never a copy: a copy would pass scripts that the worker then refuses.
func feedPreconditions(reg *recipe.Registry, hosts map[string][]string, f feed.Feed, perr error, system bool) (recipe.Recipe, *pollFailure) {
	if perr != nil {
		var fe *feed.Error
		code, detail := feed.CodeBadField, perr.Error()
		if errors.As(perr, &fe) {
			code, detail = fe.Code, fe.Detail
		}
		return nil, &pollFailure{code, detail}
	}
	rec, ok := reg.Get(f.Recipe)
	switch {
	case !ok:
		return nil, &pollFailure{codeRecipeUnknown, fmt.Sprintf("recipe %q is not known to this worker", f.Recipe)}
	case system && rec.Shape() != recipe.ShapeSystem:
		// The recipe's shape, not the Source's label, decides whether the preflight
		// applies: a mismatch either way is refused, so a KB writer cannot dodge it.
		return nil, &pollFailure{codeRecipeShape, fmt.Sprintf("recipe %q does not list a system", f.Recipe)}
	case !system && rec.Shape() != recipe.ShapeFeed:
		return nil, &pollFailure{codeRecipeShape, fmt.Sprintf("recipe %q does not list a feed", f.Recipe)}
	}
	if hs, builtin := hosts[f.Recipe]; builtin {
		if u, err := url.Parse(f.Locator); err != nil || !recipe.HostAllowed(u.Hostname(), hs) {
			return nil, &pollFailure{recipe.CodeHostNotAllowed, "the locator's host is not on the allowed list of this recipe"}
		}
	}
	return rec, nil
}

// feedRequest is the Request the poll (and the dry run) send to a feed recipe.
func feedRequest(f feed.Feed, maxEntries int) recipe.Request {
	return recipe.Request{ID: f.ID, Locator: f.Locator, SourceKind: f.SourceKind, Include: f.Include, Cursor: f.Cursor, MaxEntries: maxEntries}
}

// listFeed is step 4 of the poll: one recipe listing and the checks on it. A
// non-nil error is the context ending; a *pollFailure is what the poll writes to
// feed_error.
func listFeed(ctx context.Context, rec recipe.Recipe, f feed.Feed, maxEntries int) (recipe.Listing, *pollFailure, error) {
	lctx, cancel := context.WithTimeout(ctx, pollListTimeout)
	listing, lerr := rec.List(lctx, feedRequest(f, maxEntries))
	cancel()
	if lerr != nil {
		if ctx.Err() != nil {
			return listing, nil, ctx.Err()
		}
		code, detail := codeListFailed, lerr.Error()
		var re *recipe.Error
		if errors.As(lerr, &re) {
			detail = re.Detail
			if re.Code == recipe.CodeCursorInvalid || re.Code == recipe.CodeHostNotAllowed {
				code = re.Code
			}
		}
		return listing, &pollFailure{code, detail}, nil
	}
	if len(listing.NextCursor) > feed.MaxCursorBytes {
		return listing, &pollFailure{codeProtocol, "the listing's cursor is too long"}, nil
	}
	for _, e := range listing.Entries {
		if e.Key == "" || !slices.Contains([]string{recipe.Added, recipe.Modified, recipe.Removed}, e.Change) {
			return listing, &pollFailure{codeProtocol, "the listing has an entry with no key or an unknown change"}, nil
		}
	}
	return listing, nil, nil
}

func oneLineClip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = truncateBytes(s, n)
	}
	return s
}

// entry handles one added or modified entry. stop means the run ends here with
// the cursor untouched (a failure written, or the grant spent).
func (r *pollRun) entry(ctx context.Context, rec recipe.Recipe, e recipe.Entry, locator, id, unitMap, planID, planBody string, maps []string) (stop bool, err error) {
	f, tools := r.f, r.rt.Tools
	reason := "poll " + f.ID
	title := oneLineClip(e.Title, 120)
	if title == "" {
		title = oneLineClip(e.Key, 120)
	}
	kind := f.SourceKind
	if kind == "" {
		kind = "document"
	}
	// b. Register the document Source: no sha256, which would make dedup by hash
	// split a modified document into a second Source.
	text, err := callOK(tools, "source_register", map[string]any{
		"title": title, "source_kind": kind, "locator": locator, "source_shape": "document",
		"connector": r.in.connector[f.Recipe], "recipe": f.Recipe, "ingest_status": "pending",
		"map": strings.SplitN(f.ID, "/", 2)[0], "reason": reason,
	})
	if err != nil {
		var te *toolError
		if errors.As(err, &te) {
			return true, r.fail(codeProtocol, "entry "+oneLineClip(e.Key, 80)+": "+oneLineClip(te.text, 200))
		}
		return true, err
	}
	var reg struct {
		ID           string `json:"id"`
		Duplicate    bool   `json:"duplicate"`
		IngestStatus string `json:"ingest_status"`
	}
	if json.Unmarshal([]byte(text), &reg) != nil || reg.ID == "" {
		return true, r.fail(codeProtocol, "source_register answered nothing usable")
	}
	r.res.Commits++
	if !reg.Duplicate {
		r.sources++
	} else if reg.IngestStatus == "ingested" {
		r.reset(reg.ID, reason)
	}
	// c. Shape the unit: reuse the previous unit of this Source, else one model call.
	fm := map[string]any{"type": "IngestUnit", "shape": "ingest-unit", "title": title, "source": reg.ID, "plan": planID,
		"status": "open", "timestamp": r.in.now().UTC().Format(time.RFC3339)}
	if e.Content != "" {
		// Inline content (WP7): staged as draft, which work_list never offers,
		// until the asset exists.
		fm["status"], fm["content_asset"] = "draft", poll.ContentAsset
	}
	var prev *prevUnit
	if reg.Duplicate {
		prev = r.previous(unitMap, reg.ID)
	}
	var problem string
	for attempt := 0; attempt < 2; attempt++ {
		var body string
		if prev != nil {
			for k, v := range prev.fm {
				fm[k] = v
			}
			body = "Revision " + oneLineClip(e.Revision, 80) + " of this document changed since the previous unit of this Source; the brief below is that unit's.\n\n" + prev.body
		} else {
			ans, cut, bad, err := r.model(ctx, rec, e, planBody, maps, problem)
			if err != nil {
				return true, err
			}
			if cut {
				// Recorded, so the operator sees why the feed stalled; the cursor is
				// untouched and the next poll skips the finished entries.
				return true, r.fail(codeGrant, "the grant cannot cover another model call; entries already handled are kept")
			}
			if bad != "" {
				problem = bad // one retry with the problem, then unit_refused
				continue
			}
			fm["targets"], fm["extract"], fm["verify"], fm["executor"] = ans.Targets, strings.TrimSpace(ans.Extract), strings.TrimSpace(ans.Verify), ans.Executor
			body = ans.Body
		}
		// d. Create the unit, create-only (no if_match): a lost race is "exists".
		out, err := tools.CallTool("concept_batch", map[string]any{"reason": reason, "operations": []map[string]any{{"op": "write", "id": id, "frontmatter": fm, "body": body}}})
		if err != nil {
			return true, err
		}
		if !out.IsError {
			r.res.Commits++
			if prev != nil {
				r.reused++
			} else {
				r.created++
			}
			return false, r.finishStage(id, e.Content, reason, e)
		}
		if _, rerr := readConcept(tools, id); rerr == nil {
			r.skipped++ // exists: another replica made it first
			return false, nil
		}
		problem = oneLineClip(out.Text, 400)
		if prev != nil {
			break
		}
	}
	return true, r.fail(codeUnitRefused, "entry "+oneLineClip(e.Key, 80)+": "+problem)
}

// reset returns an ingested Source to pending so a changed document is ingested
// again; a stale hash re-reads once, then leaves it (the unit is created anyway).
func (r *pollRun) reset(id, reason string) {
	for try := 0; try < 2; try++ {
		s, err := readConcept(r.rt.Tools, id)
		if err != nil || str(s.Frontmatter, "ingest_status") != "ingested" {
			return
		}
		_, err = callOK(r.rt.Tools, "concept_patch", map[string]any{"id": id, "if_match": s.ContentHash, "reason": reason,
			"frontmatter": map[string]any{"ingest_status": "pending"}, "unset": []string{"ingested_at"}})
		if !isStale(err) {
			if err == nil {
				r.res.Commits++
			}
			return
		}
	}
}

type prevUnit struct {
	fm   map[string]any
	body string
}

// previous is the latest unit of the Source (by timestamp, ties and absent by
// ID): a changed document keeps its targets, extract, verify and executor, in
// whatever status that unit ended.
func (r *pollRun) previous(unitMap, source string) *prevUnit {
	l, err := conceptList(r.rt.Tools, map[string]any{"scope": unitMap, "where": []string{"type=IngestUnit", "source=" + source},
		"fields": []string{"targets", "extract", "verify", "executor", "timestamp"}})
	if err != nil || len(l.Results) == 0 {
		return nil
	}
	best := 0
	for i, u := range l.Results {
		bt, _ := l.Results[best].Fields["timestamp"].(string)
		ut, _ := u.Fields["timestamp"].(string)
		if ut > bt || (ut == bt && u.ID > l.Results[best].ID) {
			best = i
		}
	}
	u := l.Results[best]
	targets := strList(u.Fields["targets"])
	ex, vf, exec := str(u.Fields, "extract"), str(u.Fields, "verify"), str(u.Fields, "executor")
	if len(targets) == 0 || ex == "" || vf == "" || exec == "" {
		return nil
	}
	c, err := readConcept(r.rt.Tools, u.ID)
	if err != nil {
		return nil
	}
	return &prevUnit{fm: map[string]any{"targets": targets, "extract": ex, "verify": vf, "executor": exec}, body: truncateBytes(strings.TrimSpace(c.Body), poll.MaxBodyByte)}
}

// model is the poll's one model call for a new unit: no tools, the plan, the feed
// and the entry as delimited data, an answer validated by the code (bad is the
// problem text, sent back once). cut means the grant cannot cover another call:
// the run stops, cursor untouched.
func (r *pollRun) model(ctx context.Context, rec recipe.Recipe, e recipe.Entry, planBody string, maps []string, problem string) (ans poll.Answer, cut bool, bad string, err error) {
	feedData, _ := json.Marshal(map[string]string{"id": r.f.ID, "locator": oneLineClip(r.f.Locator, 300), "source_kind": r.f.SourceKind, "recipe": r.f.Recipe})
	entry, _ := json.Marshal(map[string]string{"key": oneLineClip(e.Key, 300), "title": oneLineClip(e.Title, 300), "revision": oneLineClip(e.Revision, 100), "change": e.Change, "locator": oneLineClip(e.Locator, 300)})
	tok := dataToken() // per-run delimiter token: data cannot close its own block
	user := fmt.Sprintf("Shape the IngestUnit for this entry. The plan's maps: %s.\n\n%s\n\n%s\n\n%s",
		strings.Join(maps, ", "), dataBlock("plan", tok, clip(planBody)), dataBlock("feed", tok, string(feedData)), dataBlock("entry", tok, string(entry)))
	if problem != "" {
		user += "\n\nYour previous answer was refused: " + problem + "\nAnswer again with the corrected JSON object."
	}
	system := poll.Instructions + "\n\nKind of source:\n" + rec.Instructions()
	need := (len(system)+len(user))/4 + pollMaxOutput
	if r.rt.Grant-r.res.Tokens < need || r.rt.DailyLeft-r.res.Tokens < need {
		return poll.Answer{}, true, "", nil
	}
	resp, err := r.rt.Model.Complete(ctx, Request{System: system, Messages: []Message{{Role: RoleUser, Text: user}}, MaxOutputTokens: pollMaxOutput})
	r.res.Tokens += resp.Usage.Total()
	if err != nil {
		return poll.Answer{}, false, "", err
	}
	ans, perr := poll.ParseAnswer(resp.Text, maps)
	if perr != nil {
		return poll.Answer{}, false, perr.Error(), nil
	}
	return ans, false, "", nil
}

// finishStage completes a unit written as draft; a failure ends the run like a
// refused unit (the staged unit is resumed by the next poll).
func (r *pollRun) finishStage(id, content, reason string, e recipe.Entry) error {
	if content == "" {
		return nil
	}
	if _, err := r.stage(id, content, reason); err != nil {
		return r.fail(codeUnitRefused, "entry "+oneLineClip(e.Key, 80)+": "+oneLineClip(err.Error(), 200))
	}
	return nil
}

// stage makes a draft unit complete (WP7): expand it, write the asset, then
// open it. Every step is idempotent, so a crash after any of them is resumed by
// redoing all: already_expanded and an existing asset are success. The asset
// already staged is kept even when it differs from content: the revision is the
// identity of the entry, and failing here would stall the feed on every poll.
// opened reports that this call patched the unit to open; r.res.Commits counts
// only the writes that happened.
func (r *pollRun) stage(id, content, reason string) (opened bool, err error) {
	tools := r.rt.Tools
	if _, err := callOK(tools, "concept_expand", map[string]any{"id": id}); err != nil {
		if !strings.Contains(err.Error(), "already_expanded") {
			return false, err
		}
	} else {
		r.res.Commits++
	}
	if _, err := callOK(tools, "asset_write", map[string]any{"concept_id": id, "path": poll.ContentAsset, "content": content, "encoding": "text"}); err != nil {
		if !strings.Contains(err.Error(), "already_exists") {
			return false, err
		}
	} else {
		r.res.Commits++
	}
	u, err := readConcept(tools, id)
	if err != nil {
		return false, err
	}
	if str(u.Frontmatter, "status") != "draft" {
		return false, nil
	}
	if _, err = callOK(tools, "concept_patch", map[string]any{"id": id, "if_match": u.ContentHash, "reason": reason,
		"frontmatter": map[string]any{"status": "open"}}); err != nil {
		return false, err
	}
	r.res.Commits++
	return true, nil
}
