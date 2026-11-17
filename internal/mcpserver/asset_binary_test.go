package mcpserver

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// #2191: a binary asset (invalid UTF-8 or NUL bytes) is returned as base64 and
// says so; asking for text explicitly is refused and names base64.
func TestAssetReadBinaryUsesBase64(t *testing.T) {
	t.Parallel()
	freshnessBackends(t, func(t *testing.T, k *kb.KB, s *Server) {
		expandOwner(t, k)
		callOK(t, s, "asset_write", `{"concept_id":"`+assetOwner+`","path":"blob.bin","content":"AQAC","encoding":"base64"}`)
		out := callOK(t, s, "asset_read", `{"concept_id":"`+assetOwner+`","path":"blob.bin"}`)
		if !strings.Contains(out, `"encoding":"base64"`) || !strings.Contains(out, "AQAC") {
			t.Fatalf("binary with NUL must come back as base64: %s", out)
		}
		res := callTool(t, s, "asset_read", `{"concept_id":"`+assetOwner+`","path":"blob.bin","encoding":"text"}`)
		if !res.IsError || !strings.Contains(resultText(res), "encoding: base64") {
			t.Fatalf("explicit text on a binary: want a refusal naming base64, got %+v", res)
		}
		callOK(t, s, "asset_write", `{"concept_id":"`+assetOwner+`","path":"a.txt","content":"plain"}`)
		if out := callOK(t, s, "asset_read", `{"concept_id":"`+assetOwner+`","path":"a.txt"}`); !strings.Contains(out, `"encoding":"text"`) {
			t.Fatalf("text stays text: %s", out)
		}
		res = callTool(t, s, "asset_read", `{"concept_id":"`+assetOwner+`","path":"nope.txt"}`)
		if !res.IsError || !strings.Contains(resultText(res), "asset not found") {
			t.Fatalf("missing asset: %+v", res)
		}
	})
}
