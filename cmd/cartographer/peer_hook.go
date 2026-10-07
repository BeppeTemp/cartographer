package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/peers"
)

// hookInput is the part of a hook's stdin the peer hooks read. Claude Code,
// Codex and Kiro all name the session `session_id` (a Codex thread id is what
// `codex queue --thread` takes) and the working directory `cwd`.
type hookInput struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
}

// peerHook is `cartographer peer hook <provider> <event>`. It always returns 0
// and prints nothing on any failure: the agent sees either its intro, its mail,
// or nothing at all.
func peerHook(args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) != 2 || !slices.Contains(peers.Providers, args[0]) {
		return 0
	}
	provider, event := args[0], args[1]
	data, _ := io.ReadAll(io.LimitReader(stdin, 1<<20))
	var in hookInput
	if json.Unmarshal(data, &in) != nil || in.SessionID == "" {
		return 0
	}
	env, err := loadPeerEnv()
	if err != nil || !env.cfg.Peers {
		return 0
	}
	switch event {
	case "SessionStart":
		roster, err := peerRegister(env, provider, in)
		if err != nil {
			return 0
		}
		if provider == "codex" {
			addRelaySession(env.dir, relaySession{Provider: provider, ID: in.SessionID})
			ensureRelay(env.dir)
		}
		io.WriteString(stdout, peerIntro(in.SessionID, roster))
	case "Stop":
		got, err := env.c.PeerTake([]string{in.SessionID}, true, peerHTTPTimeout)
		var he *client.PeerHTTPError
		if errors.As(err, &he) && he.Status == http.StatusNotFound {
			// The server restarted, or the session outlived its TTL: rejoin, so
			// the session can be reached again from the next message on.
			peerRegister(env, provider, in)
			return 0
		}
		if err != nil || len(got[in.SessionID]) == 0 {
			return 0
		}
		out, _ := json.Marshal(map[string]string{
			"decision": "block",
			"reason":   peerMessagesText(in.SessionID, got[in.SessionID]),
		})
		stdout.Write(append(out, '\n'))
	}
	return 0
}

func peerRegister(env *peerEnv, provider string, in hookInput) (*client.PeerRoster, error) {
	kbs := peerKBs(env.cfg, provider, in.CWD)
	if len(kbs) == 0 {
		return nil, errors.New("no KB bound")
	}
	return env.c.PeerRegister(peerSession(provider, in.SessionID, in.CWD, kbs), peerHTTPTimeout)
}
