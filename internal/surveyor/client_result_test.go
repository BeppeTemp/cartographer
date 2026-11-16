package surveyor

import (
	"context"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
)

// syncBlock is the block the server appends to a write result while the KB's git
// sync is not clean, as the client joins it: after a newline (#2148).
const syncBlock = "\n" + `{"last_error":"","sync_state":"pending"}`

// blockTools answers every call with the scripted text plus the sync block.
type blockTools struct{ text map[string]string }

func (b blockTools) ListTools() ([]client.ToolInfo, error) { return nil, nil }
func (b blockTools) CallTool(name string, _ any) (client.ToolOutput, error) {
	return client.ToolOutput{Text: b.text[name] + syncBlock}, nil
}

func TestRepairAcceptsTheSyncBlockAfterTheResult(t *testing.T) {
	now := time.Now()
	r := newRepair(&now)
	tools := blockTools{map[string]string{"kb_repair": `{"applied_total":3}`}}
	res, err := r.Run(context.Background(), &WorkItem{Key: "repair"}, Runtime{KB: "kb-a", Tools: tools})
	if err != nil || res.Commits != 1 || res.Units != 3 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestDoctorRepairAndStatusAcceptTheSyncBlock(t *testing.T) {
	tools := blockTools{map[string]string{
		"kb_repair": `{"applied_total":2}`,
		"kb_status": `{"conformance":{"repairable":{"x":1}}}`,
		"kb_review": `{"items":[{"kind":"k"}],"next_offset":0}`,
	}}
	if n := (&doctorInstance{}).repair(tools, []string{"x"}); n != 2 {
		t.Errorf("repair applied %d, want 2", n)
	}
	st, err := readDoctorStatus(tools)
	if err != nil || st.Conformance.Repairable["x"] != 1 {
		t.Errorf("status = %+v, %v", st, err)
	}
	items, err := reviewItems(tools, "k")
	if err != nil || len(items) != 1 {
		t.Errorf("items = %v, %v", items, err)
	}
}

func TestIngestAndPollReadsAcceptTheSyncBlock(t *testing.T) {
	tools := blockTools{map[string]string{
		"concept_read": `{"content_hash":"h1","frontmatter":{"type":"Source"},"body":"b"}`,
		"concept_list": `{"results":[{"id":"a","content_hash":"h"}],"total":1}`,
	}}
	c, err := readConcept(tools, "a")
	if err != nil || c.ContentHash != "h1" {
		t.Errorf("readConcept = %+v, %v", c, err)
	}
	l, err := conceptList(tools, map[string]any{})
	if err != nil || len(l.Results) != 1 {
		t.Errorf("conceptList = %+v, %v", l, err)
	}
}
