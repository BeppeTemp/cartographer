package configurator

import (
	"bytes"
	"encoding/json"
)

// marshalJSONIndent is json.MarshalIndent with two spaces and without HTML
// escaping: a URL with a query ("?kb=a&x=b") is written with its "&", the form
// jsonorder gives it when a later sync rewrites the file, so a connect and the
// first sync do not disagree on the bytes (#2195).
func marshalJSONIndent(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}
