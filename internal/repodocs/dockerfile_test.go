package repodocs

import (
	"regexp"
	"strings"
	"testing"
)

// dockerStage is one FROM ... block of the Dockerfile, its instructions joined
// (continuations folded) one per element.
type dockerStage struct {
	name  string
	from  string
	lines []string
}

func parseDockerfile(src string) []dockerStage {
	var stages []dockerStage
	var cur *dockerStage
	var pending string
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasSuffix(line, "\\") {
			pending += strings.TrimSuffix(line, "\\") + " "
			continue
		}
		line = pending + line
		pending = ""
		if strings.HasPrefix(line, "FROM ") {
			f := strings.Fields(line)
			st := dockerStage{from: f[1]}
			if len(f) >= 4 && strings.EqualFold(f[2], "AS") {
				st.name = f[3]
			}
			stages = append(stages, st)
			cur = &stages[len(stages)-1]
			continue
		}
		if cur != nil {
			cur.lines = append(cur.lines, line)
		}
	}
	return stages
}

func (s dockerStage) has(prefix string) (string, bool) {
	for _, l := range s.lines {
		if strings.HasPrefix(l, prefix) {
			return l, true
		}
	}
	return "", false
}

func (s dockerStage) apkPackages() []string {
	var pkgs []string
	for _, l := range s.lines {
		if i := strings.Index(l, "apk add"); i >= 0 {
			for _, f := range strings.Fields(l[i+len("apk add"):]) {
				if !strings.HasPrefix(f, "-") {
					pkgs = append(pkgs, f)
				}
			}
		}
	}
	return pkgs
}

// The last stage is the default build target and must stay the server image;
// the Surveyor stage (D1440) sits before it, without the server's secret and ssh
// tooling, without the MCP Registry label, non-root, with its own entrypoint.
func TestDockerfileStagesServerLastSurveyorBefore(t *testing.T) {
	stages := parseDockerfile(readFile(t, repoRoot(t), "Dockerfile"))
	if len(stages) < 2 {
		t.Fatalf("Dockerfile has %d stages", len(stages))
	}
	server := stages[len(stages)-1]
	if _, ok := server.has("LABEL io.modelcontextprotocol.server.name="); !ok {
		t.Error("the last stage is not the server image: it lacks the MCP Registry label (the last stage is the default target)")
	}
	have := strings.Join(server.apkPackages(), " ")
	for _, p := range []string{"git", "sops", "age", "openssh-client"} {
		if !regexp.MustCompile(`(^| )` + p + `( |$)`).MatchString(have) {
			t.Errorf("the server stage does not install %s", p)
		}
	}

	idx := -1
	for i, s := range stages {
		if s.name == "surveyor" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("no stage named surveyor")
	}
	if idx >= len(stages)-1 {
		t.Fatal("the surveyor stage is the last one: it must come before the server stage")
	}
	sv := stages[idx]
	if l, ok := sv.has("LABEL"); ok {
		t.Errorf("the surveyor stage carries a label (%s): the MCP Registry label is the server's", l)
	}
	pkgs := sv.apkPackages()
	hasGit := false
	for _, p := range pkgs {
		if p == "git" {
			hasGit = true
		}
		if p == "sops" || p == "age" || strings.HasPrefix(p, "openssh") {
			t.Errorf("the surveyor stage installs %s", p)
		}
	}
	if !hasGit {
		t.Error("the surveyor stage does not install git")
	}
	userAt, epAt := -1, -1
	for i, l := range sv.lines {
		if strings.HasPrefix(l, "USER ") && sv.lines[i] != "USER root" {
			userAt = i
		}
		if strings.HasPrefix(l, "ENTRYPOINT ") {
			epAt = i
			if l != `ENTRYPOINT ["cartographer", "surveyor"]` {
				t.Errorf("surveyor entrypoint is %q, want [\"cartographer\", \"surveyor\"]", l)
			}
		}
	}
	if userAt < 0 || epAt < 0 || userAt > epAt {
		t.Error("the surveyor stage must switch to a non-root USER before its ENTRYPOINT")
	}
	if sv.from != server.from {
		t.Errorf("surveyor and server stages pin different bases:\n  %s\n  %s", sv.from, server.from)
	}
	if !strings.Contains(sv.from, "@sha256:") {
		t.Errorf("the surveyor base %q is not pinned by digest", sv.from)
	}
}
