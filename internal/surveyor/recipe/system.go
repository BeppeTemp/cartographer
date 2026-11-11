package recipe

import (
	"context"
	"errors"
)

// ErrNeedsCheck is what the generic List of a system recipe answers: a live
// system is listed only after its identity proved read-only (ListSystem).
var ErrNeedsCheck = errors.New("recipe: a system recipe is listed only after a clean check")

// Verified is the proof that a check of one recipe answered ok (and, for a
// system recipe, read_only: true). Its fields are unexported, so the only way to
// hold a valid one is Checker.Check; it is a value of one poll, never cached.
type Verified struct {
	recipe   string
	ok       bool
	identity string
	detail   string
}

// Identity and Detail are the sanitized labels the script printed.
func (v Verified) Identity() string { return v.identity }
func (v Verified) Detail() string   { return v.detail }

// SystemRecipe is a recipe of shape system: Check, then ListSystem with what
// Check returned. A check that did not prove the identity read-only is an *Error
// with the code CodeIdentityNotReadOnly; any other failure of the check is
// CodeCheckFailed.
type SystemRecipe interface {
	Recipe
	Checker
	ListSystem(ctx context.Context, v Verified, req Request) (Listing, error)
}
