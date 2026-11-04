package gitx

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A remote with userinfo never reaches error text: the clone and the probes
// name the redacted form, and git output echoing the raw URL is scrubbed.
const credRemote = "http://user:s3cr3t@127.0.0.1:1/x.git"

func TestCloneErrorRedactsRemoteCredentials(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := Clone(ctx, credRemote, filepath.Join(t.TempDir(), "dest"))
	if err == nil {
		t.Fatal("clone of an unreachable remote succeeded")
	}
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("clone error leaks the credential: %v", err)
	}
	if !strings.Contains(err.Error(), "127.0.0.1:1/x.git") {
		t.Fatalf("clone error lost the redacted remote: %v", err)
	}
}

func TestProbeErrorsRedactRemoteCredentials(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := lsRemoteProbe(ctx, credRemote); err == nil || strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("lsRemoteProbe error = %v, want a redacted error", err)
	}
	if _, err := ProbeRemoteContent(ctx, credRemote); err == nil || strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("ProbeRemoteContent error = %v, want a redacted error", err)
	}
}

func TestCloneErrorScrubsRawRemoteFromOutputAndTimeout(t *testing.T) {
	out := cloneError(context.Background(), credRemote, "/d", context.DeadlineExceeded, "fatal: unable to access '"+credRemote+"'")
	if strings.Contains(out.Error(), "s3cr3t") {
		t.Fatalf("cloneError leaks: %v", out)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cloneError(ctx, credRemote, "/d", context.Canceled, ""); strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("timeout cloneError leaks: %v", err)
	}
}

func TestRedactRemoteText(t *testing.T) {
	if got := RedactRemoteText("see "+credRemote+" now", credRemote); strings.Contains(got, "s3cr3t") {
		t.Fatalf("RedactRemoteText = %q", got)
	}
	if got := RedactRemoteText("plain", "git@host:o/r.git"); got != "plain" {
		t.Fatalf("got %q", got)
	}
}

func TestRedactRemoteTextCoversEchoedForms(t *testing.T) {
	remote := "https://u:p4ss@h.example.com/r.git?token=T0K"
	for _, echoed := range []string{remote, "https://h.example.com/r.git?token=T0K"} {
		got := RedactRemoteText("fatal: unable to access '"+echoed+"/': nope", remote)
		if strings.Contains(got, "p4ss") || strings.Contains(got, "T0K") {
			t.Errorf("RedactRemoteText(%q) = %q", echoed, got)
		}
	}
}

func TestScrubWriterRedactsAcrossWrites(t *testing.T) {
	var out strings.Builder
	w := &scrubWriter{w: &out, remote: credRemote}
	line := "fatal: unable to access '" + credRemote + "'\r"
	for i := 0; i < len(line); i += 7 {
		end := i + 7
		if end > len(line) {
			end = len(line)
		}
		_, _ = w.Write([]byte(line[i:end]))
	}
	w.Write([]byte("tail " + credRemote))
	w.Flush()
	if strings.Contains(out.String(), "s3cr3t") || !strings.Contains(out.String(), "127.0.0.1:1/x.git") {
		t.Fatalf("scrubWriter output = %q", out.String())
	}
}
