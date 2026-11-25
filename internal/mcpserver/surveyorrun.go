package mcpserver

import (
	"context"
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/componentproto"
	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// commitSurveyorRunKey is the git trailer key that carries the Surveyor run a
// write belongs to (D2370). It is written by
// commitTrailerBlock, right before Cartographer-Host/Token, and a caller copy
// is dropped by stripTokenTrailers.
//
// The id is self-declared by whoever sends the Cartographer-Surveyor-Run
// header: any client with a token can send one. It is a label the Atlas shows
// next to the commit, never authorization: nothing in the authorizer, the
// policy or the audit reads it (TestSurveyorRunNeverAuthorizes).
const commitSurveyorRunKey = "Surveyor-Run"

type callerSurveyorRunKey struct{}

// withSurveyorRun records on ctx the Cartographer-Surveyor-Run header of the
// request (the HTTP transport; stdio has none). Exactly one value that
// satisfies componentproto.ValidRunID is kept; a repeated or invalid one is
// dropped silently (it is a hint, the write is never refused for it). A valid
// id holds no whitespace, so it cannot inject a newline or a second trailer.
func withSurveyorRun(ctx context.Context, req any) context.Context {
	type headered interface {
		GetExtra() *sdk.RequestExtra
	}
	r, ok := req.(headered)
	if !ok {
		return ctx
	}
	extra := r.GetExtra()
	if extra == nil {
		return ctx
	}
	return withSurveyorRunHeader(ctx, extra.Header)
}

func withSurveyorRunHeader(ctx context.Context, h http.Header) context.Context {
	vals := h.Values(client.SurveyorRunHeader)
	if len(vals) != 1 || !componentproto.ValidRunID(vals[0]) {
		return ctx
	}
	return context.WithValue(ctx, callerSurveyorRunKey{}, vals[0])
}

// callerSurveyorRun is the validated run id the request declared, or "".
func callerSurveyorRun(ctx context.Context) string {
	id, _ := ctx.Value(callerSurveyorRunKey{}).(string)
	return id
}

// commitSurveyorKey is the git trailer that names the Surveyor a write is by,
// "<perimeter>/<replica>" (D-new surveyor-trailer-identity). The Surveyor's
// commits keep the KB's identity as author and committer (D950), so this
// trailer is what the Atlas and concept_history show as the author. Like the
// run id it is self-declared by whoever sends the Cartographer-Surveyor
// header: a label, never authorization. commitTrailerBlock writes it and
// stripTokenTrailers drops a caller copy.
const commitSurveyorKey = gitx.SurveyorTrailer

type callerSurveyorKey struct{}

// withSurveyor records on ctx the Cartographer-Surveyor header of the request
// (the HTTP transport; stdio has none). Exactly one value that satisfies
// componentproto.ValidSurveyorID is kept; anything else is dropped silently.
func withSurveyor(ctx context.Context, req any) context.Context {
	type headered interface {
		GetExtra() *sdk.RequestExtra
	}
	r, ok := req.(headered)
	if !ok {
		return ctx
	}
	extra := r.GetExtra()
	if extra == nil {
		return ctx
	}
	return withSurveyorHeader(ctx, extra.Header)
}

func withSurveyorHeader(ctx context.Context, h http.Header) context.Context {
	vals := h.Values(client.SurveyorHeader)
	if len(vals) != 1 || !componentproto.ValidSurveyorID(vals[0]) {
		return ctx
	}
	return context.WithValue(ctx, callerSurveyorKey{}, vals[0])
}

// callerSurveyor is the validated Surveyor identity the request declared, or "".
func callerSurveyor(ctx context.Context) string {
	id, _ := ctx.Value(callerSurveyorKey{}).(string)
	return id
}
