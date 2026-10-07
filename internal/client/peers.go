package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/peers"
)

// peerAPIPath is the server's agent-messaging API (D341).
const peerAPIPath = "/api/peer/v1"

// PeerRoster is what a registration returns: the session as the server stored
// it and, per KB it joined, everyone currently there.
type PeerRoster struct {
	Session peers.Session              `json:"session"`
	Peers   map[string][]peers.Session `json:"peers"`
}

// PeerRegister registers or refreshes s.
func (c *MCPClient) PeerRegister(s peers.Session, timeout time.Duration) (*PeerRoster, error) {
	var out PeerRoster
	if err := c.peerPost("/register", s, &out, timeout); err != nil {
		return nil, err
	}
	return &out, nil
}

// PeerList returns the sessions currently registered on kb.
func (c *MCPClient) PeerList(kb string, timeout time.Duration) ([]peers.Session, error) {
	var out struct {
		Sessions []peers.Session `json:"sessions"`
	}
	if err := c.peerPost("/list", map[string]string{"kb": kb}, &out, timeout); err != nil {
		return nil, err
	}
	return out.Sessions, nil
}

// PeerLeave removes a session and its pending mail.
func (c *MCPClient) PeerLeave(id string, timeout time.Duration) error {
	return c.peerPost("/leave", map[string]string{"id": id}, nil, timeout)
}

// PeerTake collects the pending mail of ids without waiting.
func (c *MCPClient) PeerTake(ids []string, touch bool, timeout time.Duration) (map[string][]peers.Message, error) {
	var out struct {
		Messages map[string][]peers.Message `json:"messages"`
	}
	if err := c.peerPost("/take", map[string]any{"ids": ids, "touch": touch}, &out, timeout); err != nil {
		return nil, err
	}
	return out.Messages, nil
}

// PeerWait blocks server-side for up to wait until one of ids has mail. The
// HTTP timeout is wait plus a margin, so a quiet wait ends with an empty map
// rather than a client-side timeout.
func (c *MCPClient) PeerWait(ids []string, wait time.Duration, touch bool) (map[string][]peers.Message, error) {
	var out struct {
		Messages map[string][]peers.Message `json:"messages"`
	}
	body := map[string]any{"ids": ids, "touch": touch, "timeout_seconds": int(wait / time.Second)}
	if err := c.peerPost("/wait", body, &out, wait+15*time.Second); err != nil {
		return nil, err
	}
	return out.Messages, nil
}

// PeerHTTPError is a non-2xx answer from the peer API; Status lets a caller
// tell a vanished session (404) from a transient failure.
type PeerHTTPError struct {
	Status  int
	Message string
}

func (e *PeerHTTPError) Error() string {
	return fmt.Sprintf("peer API: HTTP %d: %s", e.Status, e.Message)
}

func (c *MCPClient) peerPost(path string, in, out any, timeout time.Duration) error {
	u, err := url.Parse(c.ServerURL)
	if err != nil {
		return fmt.Errorf("client: invalid server URL %q: %w", c.ServerURL, err)
	}
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/mcp")
	u.Path = strings.TrimRight(u.Path, "/") + peerAPIPath + path
	u.RawQuery, u.Fragment = "", ""
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("client: encode peer request: %w", err)
	}
	hc := *c.HTTP
	hc.Timeout = timeout
	req, err := http.NewRequest(http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("client: build peer request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return &RemoteError{State: RemoteUnavailable, Code: classifyDialErr(err),
			Message: fmt.Sprintf("could not reach %s", u.Redacted()), Cause: err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("client: read peer response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return &RemoteError{State: RemoteUnavailable, Code: CodeUnauthorized,
			Message: fmt.Sprintf("%s rejected the request", u.Redacted()), Cause: c.unauthorizedCauseFor()}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		msg := strings.TrimSpace(string(data))
		if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
			msg = e.Error.Message
		}
		return &PeerHTTPError{Status: resp.StatusCode, Message: msg}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("client: decode peer response: %w", err)
	}
	return nil
}
