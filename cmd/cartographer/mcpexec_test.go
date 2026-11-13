package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/mcpexec"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// planted is built at run time so the file holds no secret-shaped literal (D1202).
var execPlanted = "tok" + "-" + strings.Repeat("Q7", 6)

type execFake struct {
	desc     string
	approved bool
	down     error
}

func (f execFake) Call(_ context.Context, tool string, _ map[string]any) (string, error) {
	if f.down != nil {
		return "", f.down
	}
	if tool == "secret_resolve" {
		return "TOKEN=" + execPlanted, nil
	}
	b, _ := json.Marshal(map[string]string{"content": f.desc})
	return string(b), nil
}

func (f execFake) Approved(_, slug string) (string, bool) {
	if !f.approved {
		return "", false
	}
	return contract.HashArtifactFiles([]contract.ArtifactFile{{Path: slug + ".json", Content: []byte(f.desc)}}), true
}

func (execFake) Environ() []string { return []string{"HOME=/h"} }

// runMCPExec runs cmdMCPExec against f and returns its exit code, its stderr
// and the launch handed to the process-replacing call (nil when none).
func runMCPExec(t *testing.T, f execFake, args ...string) (int, string, *mcpexec.Launch) {
	t.Helper()
	oldDeps, oldRun, oldErr := mcpExecDeps, mcpExecRun, os.Stderr
	var started *mcpexec.Launch
	mcpExecDeps = func() (mcpexec.Deps, error) { return f, nil }
	mcpExecRun = func(l *mcpexec.Launch) (int, error) { started = l; return 0, nil }
	r, w, _ := os.Pipe()
	os.Stderr = w
	code := cmdMCPExec(args)
	w.Close()
	os.Stderr = oldErr
	mcpExecDeps, mcpExecRun = oldDeps, oldRun
	out, _ := io.ReadAll(r)
	return code, string(out), started
}

const execDesc = `{"type":"stdio","command":"srv","env":{"API_TOKEN":"${secret:svc#TOKEN}"}}`

func TestMCPExecStartsApprovedDescriptor(t *testing.T) {
	code, stderr, l := runMCPExec(t, execFake{desc: execDesc, approved: true}, "kb-a/svc")
	if code != 0 || l == nil {
		t.Fatalf("code %d, launch %v, stderr %q", code, l, stderr)
	}
	if got := strings.Join(l.Env, "\n"); !strings.Contains(got, "API_TOKEN="+execPlanted) {
		t.Fatal("the child environment lacks the secret")
	}
	if strings.Contains(stderr, execPlanted) {
		t.Fatal("stderr carries the secret")
	}
}

func TestMCPExecRefusesWithoutStartingAnything(t *testing.T) {
	cases := map[string]struct {
		f    execFake
		args []string
		code int
		want string
	}{
		"not approved": {execFake{desc: execDesc}, []string{"kb-a/svc"}, 1, "not approved"},
		"unreachable":  {execFake{desc: execDesc, approved: true, down: errors.New("dial: refused")}, []string{"kb-a/svc"}, 1, "the MCP server was not started"},
		"bad ref":      {execFake{}, []string{"svc"}, 2, "not <kb>/<slug>"},
		"no argument":  {execFake{}, nil, 2, "usage"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			code, stderr, l := runMCPExec(t, c.f, c.args...)
			if code != c.code || l != nil || !strings.Contains(stderr, c.want) {
				t.Fatalf("code %d (want %d), started %v, stderr %q (want %q)", code, c.code, l != nil, stderr, c.want)
			}
		})
	}
}

// TestMCPExecReplacesTheProcess re-executes the test binary: the helper calls
// Launch.Exec on a child that prints its environment and exits with a code.
func TestMCPExecReplacesTheProcess(t *testing.T) {
	if os.Getenv("CARTO_EXEC_HELPER") == "parent" {
		l := &mcpexec.Launch{Command: os.Args[0], Args: []string{"-test.run=^TestMCPExecChild$"},
			Env: []string{"CARTO_EXEC_HELPER=child", "API_TOKEN=" + execPlanted}}
		code, err := l.Exec()
		if err != nil {
			os.Stderr.WriteString(err.Error())
		}
		os.Exit(code)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMCPExecReplacesTheProcess$")
	cmd.Env = append(os.Environ(), "CARTO_EXEC_HELPER=parent")
	out, err := cmd.Output()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 7 {
		t.Fatalf("exit code did not propagate: %v", err)
	}
	if !strings.Contains(string(out), "child sees "+execPlanted) {
		t.Fatalf("the child did not see the secret: %q", out)
	}
	if strings.Contains(strings.Join(cmd.Args, " "), execPlanted) {
		t.Fatal("the secret is in the arguments")
	}
}

func TestMCPExecChild(t *testing.T) {
	if os.Getenv("CARTO_EXEC_HELPER") != "child" {
		t.Skip("helper")
	}
	os.Stdout.WriteString("child sees " + os.Getenv("API_TOKEN") + "\n")
	os.Exit(7)
}

// TestMCPExecBridgesHTTPDescriptor: an http descriptor goes to the bridge, not
// to the process-replacing call, and a bridge failure is exit 1 on stderr.
func TestMCPExecBridgesHTTPDescriptor(t *testing.T) {
	desc := `{"type":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer ${secret:svc#TOKEN}"}}`
	old := mcpExecBridge
	defer func() { mcpExecBridge = old }()
	var got *mcpexec.Launch
	mcpExecBridge = func(_ context.Context, l *mcpexec.Launch) error { got = l; return nil }
	code, stderr, started := runMCPExec(t, execFake{desc: desc, approved: true}, "kb-a/svc")
	if code != 0 || got == nil || started != nil || got.Headers["Authorization"] != "Bearer "+execPlanted {
		t.Fatalf("code %d, bridged %v, exec'd %v, stderr %q", code, got != nil, started != nil, stderr)
	}
	mcpExecBridge = func(context.Context, *mcpexec.Launch) error { return errors.New("plain http refused") }
	code, stderr, _ = runMCPExec(t, execFake{desc: desc, approved: true}, "kb-a/svc")
	if code != 1 || !strings.Contains(stderr, "plain http refused") || strings.Contains(stderr, execPlanted) {
		t.Fatalf("code %d, stderr %q", code, stderr)
	}
}
