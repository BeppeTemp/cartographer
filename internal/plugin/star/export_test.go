package star

import (
	"context"

	"go.starlark.net/starlark"

	"github.com/BeppeTemp/cartographer/internal/plugin/starcore/startest"
)

// callOpts are the seams of tests: a lower step cap, the network seams and
// extra predeclared names.
type callOpts struct {
	steps uint64
	net   *startest.NetSeams
	extra starlark.StringDict
}

// call is Call with the seams of o.
func (p *Program) call(ctx context.Context, req Request, o callOpts) (Result, error) {
	steps := o.steps
	if steps == 0 {
		steps = defaultSteps(req.Kind)
	}
	spec := p.spec(req, steps)
	if o.net != nil || o.extra != nil {
		spec.Seams = &startest.Seams{Net: o.net, Extra: o.extra}
	}
	return p.exec(ctx, spec)
}
