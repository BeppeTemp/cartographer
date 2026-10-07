package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/peers"
)

// The agent-facing half of peer messaging (D341). A session's id is not
// derivable from a stateless MCP request, so every tool names the caller's
// own session explicitly; the hub checks that the token calling is the one
// that registered it.

const (
	defaultPeerWait = 50 * time.Second
	maxPeerWait     = 10 * time.Minute
)

func peerPrincipal(ctx requestContext) string {
	return auth.PrincipalFromContext(ctx).ID
}

func peerError(tool string, err error) ToolResult {
	return errorResult(fmt.Sprintf("%s: %v", tool, err))
}

func toolPeerList(k *kb.KB, hub *peers.Hub) Tool {
	return Tool{
		Name:        "peer_list",
		Description: "Agent sessions currently working on this KB through this server (beta): id, provider, label, cwd, last_seen, pending. Your own id is in the session-start context. Read-only.",
		ReadOnly:    true,
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			data, err := json.Marshal(map[string]any{"kb": kbName(k), "sessions": hub.List(kbName(k))})
			if err != nil {
				return ToolResult{}, err
			}
			return textResult(string(data)), nil
		},
	}
}

func toolPeerSend(k *kb.KB, hub *peers.Hub) Tool {
	return Tool{
		Name:        "peer_send",
		Description: "Send a message to another agent session on this KB (beta); to=\"*\" reaches every other session. from is your own session id. Delivered into the recipient's session when its client allows it, otherwise at its next turn or peer_wait.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"from": {"type": "string", "description": "your own session id"},
				"to": {"type": "string", "description": "recipient session id, or * for all"},
				"text": {"type": "string"}
			},
			"required": ["from", "to", "text"]
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var p struct {
				From string `json:"from"`
				To   string `json:"to"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			sent, err := hub.Send(peerPrincipal(ctx), kbName(k), p.From, p.To, p.Text)
			if err != nil {
				return peerError("peer_send", peerHint(err)), nil
			}
			to := make([]string, 0, len(sent))
			for _, m := range sent {
				to = append(to, m.To)
			}
			data, err := json.Marshal(map[string]any{"sent": len(sent), "to": to})
			if err != nil {
				return ToolResult{}, err
			}
			return textResult(string(data)), nil
		},
	}
}

func toolPeerWait(k *kb.KB, hub *peers.Hub) Tool {
	return Tool{
		Name:        "peer_wait",
		Description: "Wait for messages from other agent sessions on this KB (beta) and return them, or an empty list on timeout. session is your own id; timeout_seconds defaults to 50, max 600 — keep it under your client's tool timeout and call again to keep listening.",
		ReadOnly:    true,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"session": {"type": "string", "description": "your own session id"},
				"timeout_seconds": {"type": "integer"}
			},
			"required": ["session"]
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var p struct {
				Session string `json:"session"`
				Timeout int    `json:"timeout_seconds"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			timeout := defaultPeerWait
			if p.Timeout > 0 {
				timeout = min(time.Duration(p.Timeout)*time.Second, maxPeerWait)
			}
			got, err := hub.Wait(ctx, peerPrincipal(ctx), []string{p.Session}, timeout, true)
			if err != nil {
				return peerError("peer_wait", peerHint(err)), nil
			}
			msgs := got[p.Session]
			if msgs == nil {
				msgs = []peers.Message{}
			}
			data, err := json.Marshal(map[string]any{"messages": msgs})
			if err != nil {
				return ToolResult{}, err
			}
			return textResult(string(data)), nil
		},
	}
}

// peerHint adds the one thing an agent can do about the error it got.
func peerHint(err error) error {
	switch {
	case errors.Is(err, peers.ErrUnknown):
		return fmt.Errorf("%w (expired, or never registered: sessions register at session start; peer_list shows who is here)", err)
	case errors.Is(err, peers.ErrForbidden):
		return fmt.Errorf("%w (use your own session id)", err)
	}
	return err
}
