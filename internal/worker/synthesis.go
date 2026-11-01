package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/worker/synthesis"
	"github.com/BeppeTemp/cartographer/internal/workerconfig"
)

// The synthesis task kind (D1263): the third kind, the lowest priority. With
// nothing left for the kinds listed before it in tasks[], it reads what the KB
// holds and proposes what it infers as Contradiction concepts of a gap kind.
// The shape, the write guard and the candidate choice live in package synthesis;
// this file is the task: one model call per candidate behind the guard.

const (
	synthesisKindName = "synthesis"
	// synthesisMaxSteps bounds the tool turns of one candidate.
	synthesisMaxSteps = 8
	// synthesisCeiling is how many times the estimated cost of a candidate its
	// loop may spend: the estimate is one call, a loop makes several.
	synthesisCeiling = 3
	// synthesisMaxModelErrors consecutive model errors end the task.
	synthesisMaxModelErrors = 2
	// synthesisBytesPerToken converts synthesis_context_tokens into the byte cap
	// of the context block placed in the prompt.
	synthesisBytesPerToken = 4
)

// schemaOption declares an option from its row of the key table, the one
// place its description and default are written (CheckSchema pins the pair).
func schemaOption(name string) KindOption {
	k, ok := workerconfig.Schema.Lookup("tasks[]." + name)
	if !ok {
		panic("worker: no key table row for tasks[]." + name)
	}
	return KindOption{Key: k}
}

// SynthesisKind is the registration of the synthesis task kind.
func SynthesisKind() Kind {
	return Kind{
		Name: synthesisKindName,
		Last: true,
		Options: []KindOption{
			schemaOption("synthesis_map"), schemaOption("synthesis_max_candidates"), schemaOption("synthesis_max_new"),
			schemaOption("synthesis_context_tokens"), schemaOption("synthesis_reserve"),
		},
		Validate: func(o map[string]any) error {
			for _, k := range []string{"synthesis_max_candidates", "synthesis_max_new"} {
				if n, _ := o[k].(int); n < 1 {
					return fmt.Errorf("%s must be at least 1", k)
				}
			}
			if n, _ := o["synthesis_context_tokens"].(int); n < 500 {
				return errors.New("synthesis_context_tokens must be at least 500")
			}
			if _, err := synthesisReserve(o); err != nil {
				return err
			}
			if m, _ := o["synthesis_map"].(string); m != "" && !synthesis.ValidMap(m) {
				return errors.New("synthesis_map must be the name of a top-level map")
			}
			return nil
		},
		Build: func(o map[string]any) (Instance, error) {
			reserve, err := synthesisReserve(o)
			if err != nil {
				return nil, err
			}
			m, _ := o["synthesis_map"].(string)
			mc, _ := o["synthesis_max_candidates"].(int)
			mn, _ := o["synthesis_max_new"].(int)
			ct, _ := o["synthesis_context_tokens"].(int)
			return &synthesisInstance{
				cfg:    synthesis.Config{Map: m, MaxCandidates: mc, MaxNew: mn, ContextTokens: ct, Reserve: reserve},
				system: synthesis.Instructions,
				now:    time.Now,
			}, nil
		},
	}
}

func synthesisReserve(o map[string]any) (float64, error) {
	s, _ := o["synthesis_reserve"].(string)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 || f >= 1 {
		return 0, fmt.Errorf("synthesis_reserve %q must be a fraction above 0 and below 1", s)
	}
	return f, nil
}

type synthesisInstance struct {
	cfg    synthesis.Config
	system string
	now    func() time.Time
}

// Next offers an item only when a candidate exists, found by the same read-only
// selection Run repeats with the budget in hand: a KB with nothing to propose
// leaves no closing entry (the runner touches a KB only for an item it runs).
// The tried set of the runner keeps the item from being offered twice in a run.
// The budget is not known here, so the check ignores it; Run applies it.
func (in *synthesisInstance) Next(_ context.Context, kb string, tools ToolCaller) (*WorkItem, error) {
	cands, _, err := synthesis.Candidates(tools, in.cfg, synthesis.Budget{Remaining: 1 << 40}, in.now())
	switch {
	case errors.Is(err, synthesis.ErrNoMap), errors.Is(err, synthesis.ErrIncomplete):
		return nil, nil
	case err != nil:
		return nil, err
	case len(cands) == 0:
		return nil, nil
	}
	return &WorkItem{Key: "synthesis", Data: kb}, nil
}

// Run examines the candidates in order, one model call each, until a cap or the
// budget stops it. It leaves no state: the run record is the summary the runner
// puts in the closing log_append (D13).
func (in *synthesisInstance) Run(ctx context.Context, _ *WorkItem, rt Runtime) (Result, error) {
	now := in.now()
	cands, cfg, err := synthesis.Candidates(rt.Tools, in.cfg, synthesis.Budget{Daily: rt.Profile.DailyTokens, Remaining: rt.DailyLeft}, now)
	switch {
	case errors.Is(err, synthesis.ErrNoMap), errors.Is(err, synthesis.ErrIncomplete):
		return Result{Summary: err.Error()}, nil
	case err != nil:
		return Result{}, err
	}
	var (
		res      Result
		examined []string
		written  []string
		modelErr int
		lastErr  error
	)
	per := cfg.ContextTokens + synthesis.AnswerCap
	reserve := int(cfg.Reserve * float64(rt.Profile.DailyTokens))
	for _, c := range cands {
		// What this candidate may spend: the run's grant, and the daily budget
		// down to the reserve (D12), never more than the loop's ceiling.
		remaining := min(rt.Grant-res.Tokens, rt.DailyLeft-res.Tokens-reserve)
		if ctx.Err() != nil || remaining < per || len(written) >= cfg.MaxNew {
			break
		}
		examined = append(examined, c.Key())
		g := &synthesis.Guard{Inner: rt.Tools, Target: c.ID, Reason: "synthesis: " + c.Key()}
		agent := NewAgent(rt.Model, g, NewAllowlist(synthesis.Tools()...), Limits{MaxSteps: synthesisMaxSteps, MaxTotalTokens: min(remaining, synthesisCeiling*per)})
		out, runErr := agent.Run(ctx, Task{System: in.system, User: in.prompt(rt.Tools, c, cfg, now)})
		res.Tokens += out.Usage.Total()
		if runErr != nil {
			if errors.Is(runErr, ErrModelAuth) || ctx.Err() != nil {
				res.Summary = in.record(examined, written, res.Tokens)
				return res, runErr
			}
			lastErr = runErr
			if modelErr++; modelErr >= synthesisMaxModelErrors {
				break
			}
			continue
		}
		modelErr = 0
		if g.Written != "" {
			written = append(written, g.Written)
			res.Commits++
		}
		// A refused call, a cut loop or a barren answer ends that candidate only.
	}
	res.Units = len(written)
	res.Summary = in.record(examined, written, res.Tokens)
	if modelErr >= synthesisMaxModelErrors && lastErr != nil {
		return res, fmt.Errorf("synthesis: %d consecutive model errors: %w", modelErr, lastErr)
	}
	return res, nil
}

// record is the synthesis line of the run record: what was written, what it
// cost, and last the candidate keys examined (the next run reads them back).
func (in *synthesisInstance) record(examined, written []string, tokens int) string {
	w := "none"
	if len(written) > 0 {
		w = strings.Join(written, ",")
	}
	s := fmt.Sprintf("synthesis wrote %s; %d tokens", w, tokens)
	if len(examined) > 0 {
		s += "; " + synthesis.ExaminedLine(examined)
	}
	return s
}

// prompt places the candidate and its bounded context as quoted data.
func (in *synthesisInstance) prompt(tools ToolCaller, c synthesis.Candidate, cfg synthesis.Config, now time.Time) string {
	ctxText := ""
	if out, err := tools.CallTool("graph_context", map[string]any{"ids": c.Involves, "limit": 10}); err == nil && !out.IsError {
		ctxText = truncateBytes(out.Text, cfg.ContextTokens*synthesisBytesPerToken)
	}
	// A per-run token on the delimiters: data cannot close its own block.
	tok := dataToken()
	cand, _ := json.Marshal(map[string]any{"source": c.Source, "contradiction_kind": c.Kind, "subject": c.Subject, "involves": c.Involves})
	return fmt.Sprintf("Examine this candidate. Today is %s. The hypothesis, if you write one, is created at the id %q with contradiction_kind %q and exactly these involves.\n\n%s\n\n%s",
		now.UTC().Format("2006-01-02"), c.ID, c.Kind, dataBlock("candidate", tok, string(cand)), dataBlock("context", tok, ctxText))
}
