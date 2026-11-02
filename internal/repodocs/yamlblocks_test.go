package repodocs

// Every ```yaml block of the deployment and configurator pages must decode: the
// recipes there (manifests, compose files) are copied by operators and no Go
// test loads them as anything but the two documented config shapes.

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var yamlFence = regexp.MustCompile("(?m)^```yaml[ \t]*\n((?s:.*?))^```[ \t]*$")

func TestDocumentedYAMLBlocksDecode(t *testing.T) {
	root := repoRoot(t)
	for _, page := range []string{"docs/deployment.md", "docs/configurator.md"} {
		text := readFile(t, root, page)
		for _, m := range yamlFence.FindAllStringSubmatchIndex(text, -1) {
			body := text[m[2]:m[3]]
			line := strings.Count(text[:m[0]], "\n") + 1
			dec := yaml.NewDecoder(bytes.NewBufferString(body))
			for {
				var v any
				err := dec.Decode(&v)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Errorf("%s:%d: yaml block does not decode: %v", page, line, err)
					break
				}
			}
		}
	}
}
