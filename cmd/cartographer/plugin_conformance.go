package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/plugin"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/surveyor"
	"github.com/BeppeTemp/cartographer/internal/surveyor/connector"
	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe"
	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe/recipetest"
)

// pluginTestEnvPrefix names the process variables a test run maps a recipe's
// declared variables to: the scenario's values are set under them for the run.
const pluginTestEnvPrefix = "CARTOGRAPHER_PLUGIN_TEST_"

// pluginTest runs the recipes and connectors of a plugin folder against their scenarios
// (tests/<name>.yaml) and the conformance checks. It needs no server, token or
// model. A Starlark recipe runs in process and its http.request is answered from
// the recordings, so it touches no network; an exec recipe and a stdio connector
// are child processes that RUN AS THE USER, unapproved and with the user's
// network, so each command is printed on stderr before it runs. --recipe and
// --connector each narrow the run to one target and cannot be given together.
// Exit 0 all passed, 1 a check failed, 2 it could not run.
func pluginTest(args []string) int {
	output, args, err := outputFlag(args)
	var only, onlyConn string
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] != "--recipe" && args[i] != "--connector" {
			rest = append(rest, args[i])
			continue
		}
		if i++; i >= len(args) {
			return pluginFail(2, "%s needs a value\n%s", args[i-1], pluginUsage)
		}
		if args[i-1] == "--recipe" {
			only = args[i]
		} else {
			onlyConn = args[i]
		}
	}
	pos, _, ok := pluginArgs(rest)
	if err != nil || !ok || len(pos) != 1 {
		return pluginFail(2, "%s", pluginUsage)
	}
	if only != "" && onlyConn != "" {
		return pluginFail(2, "--recipe and --connector each pick the one target to test: give one of them, or neither to test every recipe and connector")
	}
	if fi, err := os.Stat(pos[0]); err != nil || !fi.IsDir() {
		return pluginFail(2, "%s is not a folder", pos[0])
	}
	src, err := plugin.ParseSource(pos[0])
	if err != nil {
		return pluginFail(2, "%v", err)
	}
	every := pluginOpts()
	every.Side = "" // every side must validate, as `plugin validate` holds it
	_, _, p, probs, err := pluginReadWith(src, every)
	if err != nil {
		return pluginFail(2, "%v", err)
	}
	if len(probs) > 0 {
		for _, pr := range probs {
			fmt.Fprintln(os.Stderr, escapeControl(pr))
		}
		return pluginFail(2, "%s is not a valid plugin: run `cartographer plugin validate` first", pos[0])
	}
	results, err := testTargets(&p, src.Path, only, onlyConn)
	if err != nil {
		return pluginFail(2, "%s", escapeControl(err.Error()))
	}
	failed := slices.ContainsFunc(results, func(r recipetest.Result) bool { return r.Status == recipetest.Fail })
	if output == "json" {
		printJSON(map[string]any{"schema": pluginSchema, "passed": !failed, "results": results})
	} else {
		for _, r := range results {
			line := fmt.Sprintf("%-28s %-14s %s", escapeControl(r.Target), escapeControl(r.Check), r.Status)
			if r.Detail != "" {
				line += "  " + escapeControl(r.Detail)
			}
			fmt.Println(line)
		}
	}
	if failed {
		return 1
	}
	return 0
}

// testTargets runs every recipe and connector of the plugin with its scenario;
// --recipe or --connector narrows it to the one named. Each is built by the
// Surveyor's own loader, once per scenario because the settings are the
// scenario's, so what runs is what a pinned plugin would run.
func testTargets(p *core.Plugin, root, onlyRecipe, onlyConn string) ([]recipetest.Result, error) {
	var perms *core.Permissions
	if c, ok := p.Manifest.Components[core.SideSurveyor]; ok {
		perms = c.Permissions
	}
	env := map[string]string{}
	if perms != nil {
		for _, n := range perms.Env {
			env[n] = pluginTestEnvPrefix + n
		}
	}
	// Naming one kind narrows the run to it.
	wantRecipes, wantConns := onlyConn == "", onlyRecipe == ""
	var results []recipetest.Result
	found := false
	scenario := func(kind, name string) (*recipetest.Scenario, *surveyor.Config, error) {
		sc, err := recipetest.ReadFile(filepath.Join(root, "tests"), name)
		if err != nil {
			return nil, nil, fmt.Errorf("%s %s: tests/%v", kind, name, err)
		}
		cfg, err := surveyor.PluginPoints(p, surveyor.PluginPin{Enabled: true, Settings: sc.Settings, Env: env}, root)
		return sc, cfg, err
	}
	for _, rc := range p.Contributions["recipes"] {
		if !wantRecipes || (onlyRecipe != "" && rc.Name != onlyRecipe) {
			continue
		}
		found = true
		sc, cfg, err := scenario("recipe", rc.Name)
		if err != nil {
			return nil, err
		}
		if len(sc.Steps) == 0 {
			return nil, fmt.Errorf("recipe %s: tests/%s.yaml has no steps", rc.Name, rc.Name)
		}
		in := recipetest.Input{Name: rc.Name, Permissions: perms, Declared: p.Manifest.Settings, Scenario: sc}
		q := recipe.Qualified(p.Manifest.Name, rc.Name)
		for i := range cfg.ExecRecipes {
			if cfg.ExecRecipes[i].Name == q {
				in.Exec = &cfg.ExecRecipes[i]
			}
		}
		for i := range cfg.StarRecipes {
			if cfg.StarRecipes[i].Name == q {
				in.Star = &cfg.StarRecipes[i]
			}
		}
		if in.Exec == nil && in.Star == nil {
			return nil, fmt.Errorf("recipe %s: the Surveyor loader did not build it", rc.Name)
		}
		if in.Exec != nil {
			fmt.Fprintln(os.Stderr, execAnnouncement(rc.Name, in.Exec))
		}
		results = append(results, recipetest.Run(context.Background(), in)...)
	}
	for _, ct := range p.Contributions["connectors"] {
		if !wantConns || (onlyConn != "" && ct.Name != onlyConn) {
			continue
		}
		found = true
		sc, cfg, err := scenario("connector", ct.Name)
		if err != nil {
			return nil, err
		}
		if len(sc.Calls) == 0 {
			return nil, fmt.Errorf("connector %s: tests/%s.yaml has no calls", ct.Name, ct.Name)
		}
		q := recipe.Qualified(p.Manifest.Name, ct.Name)
		i := slices.IndexFunc(cfg.Connectors, func(c connector.Config) bool { return c.Name == q })
		if i < 0 {
			return nil, fmt.Errorf("connector %s: the Surveyor loader did not build it", ct.Name)
		}
		if cfg.Connectors[i].Transport == connector.TransportStdio {
			fmt.Fprintln(os.Stderr, announcement("connector", ct.Name, cfg.Connectors[i].Command, cfg.Connectors[i].Args))
		}
		results = append(results, recipetest.RunConnector(context.Background(), recipetest.ConnectorInput{Name: ct.Name, Config: cfg.Connectors[i], Env: env, Scenario: sc})...)
	}
	if !found {
		if onlyRecipe != "" || onlyConn != "" {
			return nil, fmt.Errorf("the plugin has no such recipe or connector")
		}
		return nil, fmt.Errorf("the plugin has no recipes or connectors to test (on its surveyor side)")
	}
	return results, nil
}

// execAnnouncement is the line printed before a recipe's own command runs: it is
// a program of the folder, started as the user without the approval an install
// asks for, so the author sees what is about to run. Escaped like the preview.
func execAnnouncement(name string, c *recipe.ExecConfig) string {
	return announcement("recipe", name, c.Command, c.Args)
}

// announcement words the line for a recipe's command or a stdio connector's.
func announcement(kind, name, command string, args []string) string {
	argv := make([]string, 0, 1+len(args))
	for _, a := range append([]string{command}, args...) {
		argv = append(argv, escapeControl(a))
	}
	return fmt.Sprintf("running %s %s as you, unapproved: %s", kind, escapeControl(name), strings.Join(argv, " "))
}
