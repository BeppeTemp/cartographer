package worker

import (
	"errors"
	"fmt"
	"strings"
)

func containsCanary(s string) bool  { return strings.Contains(s, canary) }
func isAuth(err error) bool         { return errors.Is(err, ErrModelAuth) }
func sprint(f string, v any) string { return fmt.Sprintf(f, v) }
