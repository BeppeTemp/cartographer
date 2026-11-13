package sops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Rule states InitRule reports.
const (
	RuleCreated   = "created"
	RuleUnchanged = "unchanged"
	RuleReplaced  = "replaced"
)

// ErrRuleExists is returned (wrapped in a *RuleExistsError) by InitRule when the
// KB already has a .sops.yaml whose creation rule is not the requested one and
// force is false.
var ErrRuleExists = errors.New("the KB already has a different .sops.yaml")

// RuleExistsError carries the public recipients of the rule that is in the way.
type RuleExistsError struct{ Recipients []string }

func (e *RuleExistsError) Error() string {
	if len(e.Recipients) == 0 {
		return ErrRuleExists.Error()
	}
	return fmt.Sprintf("%s (recipients: %s)", ErrRuleExists, strings.Join(e.Recipients, ", "))
}

func (e *RuleExistsError) Unwrap() error { return ErrRuleExists }

// agePublicKey is the shape of an age public key: "age1" and 58 bech32 characters.
var agePublicKey = regexp.MustCompile(`^age1[qpzry9x8gf2tvdw0s3jn54khce6mua7l]{58}$`)

// ValidRecipient reports whether s has the shape of an age public key.
func ValidRecipient(s string) bool { return agePublicKey.MatchString(s) }

// InitRule writes the root .sops.yaml of a KB with the one creation rule
// Cartographer documents (secrets/*.sops.yaml encrypted to recipient). It is the
// only writer of that file (D1191, D2039): the caller is the
// admin-only secrets_init tool, as the recipients are the operator's decision.
// A different existing rule is kept unless force is set, and previous holds its
// recipients. The caller commits; a replaced rule does not re-encrypt the files
// already under secrets/.
func InitRule(kbRoot, recipient string, force bool) (state string, previous []string, err error) {
	if !ValidRecipient(recipient) {
		return "", nil, errors.New("recipient is not an age public key (age1...)")
	}
	path := filepath.Join(kbRoot, ".sops.yaml")
	fi, err := os.Lstat(path)
	exists := err == nil
	switch {
	case err != nil && !os.IsNotExist(err):
		return "", nil, err
	case exists && !fi.Mode().IsRegular():
		return "", nil, errors.New(".sops.yaml must be a plain file, not a link or a directory")
	}
	if exists {
		rec, matched, rerr := RuleRecipients(kbRoot, "secrets/x.sops.yaml")
		if rerr == nil && matched && slices.Equal(rec, []string{recipient}) {
			return RuleUnchanged, rec, nil
		}
		previous = rec
		if !force {
			return "", previous, &RuleExistsError{Recipients: previous}
		}
	}
	content := "creation_rules:\n  - path_regex: secrets/.*\\.sops\\.yaml$\n    age: " + recipient + "\n"
	tmp, err := os.CreateTemp(kbRoot, ".sops.yaml.tmp-*")
	if err != nil {
		return "", nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return "", nil, err
	}
	if err := tmp.Close(); err != nil {
		return "", nil, err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return "", nil, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", nil, err
	}
	if exists {
		return RuleReplaced, previous, nil
	}
	return RuleCreated, nil, nil
}
