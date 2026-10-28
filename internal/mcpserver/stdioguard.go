package mcpserver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// maxStdioLine bounds one inbound line, as the SDK's own frame limit does.
const maxStdioLine = 16 << 20

// lockedWriter serializes whole Write calls: the SDK writes each message in one
// call, and the guard's error lines share the same stdout.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// guardStdio sits between stdin and the SDK. The SDK's stdio reader decodes a
// JSON stream and ends the session on the first value it cannot take; here
// every line is checked first, a bad one is answered with the JSON-RPC error
// HTTP gives it (-32700 not JSON, -32600 not a valid request, id null unless
// the request carried a scalar one) and dropped, and the loop reads the next
// line. Only an I/O error on either side ends the session. Messages are one
// per line, as docs/transport-auth.md says.
func guardStdio(in io.Reader, out io.Writer) (io.Reader, io.Writer) {
	lw := &lockedWriter{w: out}
	pr, pw := io.Pipe()
	go func() {
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 0, 64<<10), maxStdioLine)
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 {
				continue
			}
			if code, msg := classifyStdioLine(line); code != 0 {
				var id json.RawMessage
				if code != ErrCodeParseError {
					id = requestID(line)
				}
				b, _ := json.Marshal(errorResponse(id, code, msg))
				if _, err := lw.Write(append(b, '\n')); err != nil {
					pw.CloseWithError(err)
					return
				}
				continue
			}
			if _, err := pw.Write(append(append([]byte(nil), line...), '\n')); err != nil {
				return
			}
		}
		pw.CloseWithError(sc.Err()) // nil is io.EOF for the reader
	}()
	return pr, lw
}

// classifyStdioLine returns the JSON-RPC error code and text for a line that is
// not a valid message, or 0. The text is worded as the SDK words it and goes
// through protocolErrorCode, so both transports classify alike.
func classifyStdioLine(line []byte) (int, string) {
	if !json.Valid(line) {
		return protocolErrorCode("malformed payload: not valid JSON"), "malformed payload: not valid JSON"
	}
	var batch []json.RawMessage
	if json.Unmarshal(line, &batch) == nil {
		if len(batch) == 0 {
			return protocolErrorCode("empty batch"), "empty batch"
		}
	} else {
		batch = []json.RawMessage{line}
	}
	for _, raw := range batch {
		if _, err := jsonrpc.DecodeMessage(raw); err != nil {
			code := protocolErrorCode(err.Error())
			if code == 0 || code == ErrCodeMethodNotFound {
				code = ErrCodeInvalidRequest
			}
			return code, fmt.Sprint(err)
		}
	}
	return 0, ""
}
