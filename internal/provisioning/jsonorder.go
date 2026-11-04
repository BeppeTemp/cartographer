package provisioning

import (
	"errors"
	"fmt"
	"os"

	"github.com/BeppeTemp/cartographer/internal/jsonorder"
)

// jsonLayout is what an ordinary map round trip of a JSON object loses: the key
// order of every object read, the indent unit and whether the file ended with a
// newline (#1198). The mechanism lives in internal/jsonorder, which the
// configurator uses too (#1502).
type jsonLayout = jsonorder.Layout

// loadOrderedJSONObject is loadJSONObject plus the layout saveOrderedJSONObject
// writes back. Numbers are kept as written (json.Number). A missing file is an
// empty object, written with a two-space indent and a final newline.
func loadOrderedJSONObject(path string) (map[string]interface{}, *jsonLayout, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]interface{}{}, jsonorder.NewLayout(), nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("provisioning: read %s: %w", path, err)
	}
	m, layout, err := jsonorder.Parse(data)
	if err != nil {
		return nil, nil, fmt.Errorf("provisioning: parse %s: %w", path, err)
	}
	return m, layout, nil
}

// saveOrderedJSONObject is saveJSONObject writing m in layout: the keys of an
// object read from the file in their original order (a key added to it goes
// after them, sorted), the file's indent and its final newline. HTML characters
// are not escaped: a hook command's `&&` stays as written.
func saveOrderedJSONObject(baseDir, path string, m map[string]interface{}, layout *jsonLayout) error {
	data, err := jsonorder.Marshal(m, layout)
	if err != nil {
		return fmt.Errorf("provisioning: serialize %s: %w", path, err)
	}
	return writeJSONFile(baseDir, path, data)
}
