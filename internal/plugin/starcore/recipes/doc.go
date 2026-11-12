// Package recipes is the surveyor side of the Starlark runtime: the recipe()
// registrar of surveyor.star and the call of a recipe's verbs (spec, check,
// list, fetch). It sits under internal/plugin/starcore, so the Surveyor links
// it (it reaches neither the apply half nor the server's config, as starcore
// itself) and the test seams of the runtime stay reachable only through
// startest.
//
// A recipe is a function that returns a dict. The host turns the dict into the
// JSON lines an exec recipe prints and hands them to the same parser, so there
// is one definition of a valid listing (internal/surveyor/recipe). Its
// capabilities are the runtime's http, env, log and settings and the pure
// modules; the files and kb modules do not exist here, so a script that names
// one does not load. A recipe lists; it never writes a KB.
package recipes
