package client

import "testing"

func TestDecodeResultKeepsTheFirstValueOnly(t *testing.T) {
	block := "\n" + `{"last_error":"","sync_state":"pending"}`
	var v struct{ N int }
	if err := DecodeResult(`{"N":4}`+block, &v); err != nil || v.N != 4 {
		t.Fatalf("v=%+v err=%v", v, err)
	}
	for _, bad := range []string{"", "  ", "not json" + block} {
		if DecodeResult(bad, &v) == nil {
			t.Errorf("%q decoded", bad)
		}
	}
}
