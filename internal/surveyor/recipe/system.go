package recipe

import (
	"context"
	"errors"
)

// ErrNeedsPreflight is what the generic List of a system recipe answers: a live
// system is listed only after its identity proved read-only (ListSystem).
var ErrNeedsPreflight = errors.New("recipe: a system recipe is listed only after a clean preflight")

// Verified is the proof that a preflight of one recipe answered a clean
// read_only: true. Its fields are unexported, so the only way to hold a valid
// one is SystemRecipe.Preflight; it is a value of one poll, never cached.
type Verified struct {
	recipe   string
	ok       bool
	identity string
	detail   string
}

// Identity and Detail are the sanitized labels the script printed.
func (v Verified) Identity() string { return v.identity }
func (v Verified) Detail() string   { return v.detail }

// SystemRecipe is a recipe of shape system: Preflight, then ListSystem with
// what Preflight returned. Every refusal of the preflight is an *Error with the
// code CodeIdentityNotReadOnly.
type SystemRecipe interface {
	Recipe
	Preflight(ctx context.Context, req Request) (Verified, error)
	ListSystem(ctx context.Context, v Verified, req Request) (Listing, error)
}
