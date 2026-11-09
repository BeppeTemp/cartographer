package recipe

import "github.com/BeppeTemp/cartographer/internal/worker/connector"

// CheckEnv reports the first listed variable that is unset or empty, naming it
// and never a value: the dry run of a recipe calls it before running the script,
// so the failure is a start error and not a listing failure.
func (c ExecConfig) CheckEnv() error {
	_, err := connector.ChildEnv(c.Env)
	return err
}
