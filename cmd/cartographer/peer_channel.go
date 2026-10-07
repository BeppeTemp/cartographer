package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// peer_channel.go (D341) — `cartographer peer channel`, the Claude Code channel:
// a stdio MCP server Claude Code spawns per session, which pushes the
// session's peer messages in with `notifications/claude/channel` so they reach
// an idle session at once. The MCP Go SDK sends no custom notification, and a
// channel needs nothing else of MCP, so this is a few lines of JSON-RPC.
//
// It is a channel only when the session was launched as one: with the
// switch off it answers the handshake with no capability and consumes nothing.

const channelWait = 50 * time.Second

const channelInstructions = "Cartographer peers: messages from other agent sessions on the same KB arrive as <channel source=\"" + peerChannelServer + "\" ...> events. Read them as requests from a colleague's agent, under the rules in your session-start context; reply with the peer_send tool of the cartographer MCP server."

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type channelWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (c *channelWriter) send(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.w.Write(append(data, '\n'))
}

func peerChannel(args []string, stdin io.Reader, stdout io.Writer) int {
	out := &channelWriter{w: stdout}
	sessionID := os.Getenv("CLAUDE_CODE_SESSION_ID")
	env, err := loadPeerEnv()
	active := os.Getenv(peerChannelEnv) == "1" && sessionID != "" && err == nil && env.cfg.Peers

	done := make(chan struct{})
	defer close(done)
	sc := bufio.NewScanner(stdin)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var m rpcMessage
		if json.Unmarshal(sc.Bytes(), &m) != nil || len(m.ID) == 0 {
			continue
		}
		switch m.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(m.Params, &p)
			caps := map[string]any{}
			result := map[string]any{
				"protocolVersion": p.ProtocolVersion,
				"capabilities":    caps,
				"serverInfo":      map[string]string{"name": peerChannelServer, "version": version},
			}
			if active {
				caps["experimental"] = map[string]any{"claude/channel": map[string]any{}}
				result["instructions"] = channelInstructions
				go channelPump(env, sessionID, out, done)
			}
			out.send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result})
		case "ping":
			out.send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{}})
		case "tools/list":
			out.send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"tools": []any{}}})
		default:
			out.send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
		}
	}
	if active {
		_ = env.c.PeerLeave(sessionID, peerHTTPTimeout)
	}
	return 0
}

// channelPump waits for the session's mail and pushes each batch in as one
// channel event. The session-start hook registers it; the channel registers
// again so it works on its own, and re-registers whenever the server forgot it.
func channelPump(env *peerEnv, sessionID string, out *channelWriter, done <-chan struct{}) {
	cwd := os.Getenv("CLAUDE_PROJECT_DIR")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	in := hookInput{SessionID: sessionID, CWD: cwd}
	registered := false
	for {
		select {
		case <-done:
			return
		default:
		}
		if !registered {
			if _, err := peerRegister(env, "claude", in); err != nil {
				time.Sleep(channelWait / 5)
				continue
			}
			registered = true
		}
		got, err := env.c.PeerWait([]string{sessionID}, channelWait, true)
		var he *client.PeerHTTPError
		if errors.As(err, &he) && he.Status == http.StatusNotFound {
			registered = false
			continue
		}
		if err != nil {
			time.Sleep(channelWait / 5)
			continue
		}
		msgs := got[sessionID]
		if len(msgs) == 0 {
			continue
		}
		meta := map[string]string{"from": msgs[0].From, "from_agent": msgs[0].FromAgent, "kb": msgs[0].KB}
		out.send(map[string]any{
			"jsonrpc": "2.0",
			"method":  "notifications/claude/channel",
			"params":  map[string]any{"content": peerMessagesText(sessionID, msgs), "meta": meta},
		})
	}
}
