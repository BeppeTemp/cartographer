package kb

import (
	"reflect"
	"testing"
)

func TestTemplatePageBody(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want string
	}{
		{"no fence is the whole body", "# T\n\n## A\n\n## B\n", "# T\n\n## A\n\n## B\n"},
		{
			"fenced sample replaces the prose",
			"Guidance for the writer.\n\n```markdown\n# T\n\n## A\n\n## B\n```\n\nMore guidance.\n",
			"# T\n\n## A\n\n## B\n",
		},
		{
			"sections outside the fence win over a fenced sample",
			"## Real\n\n```markdown\n## Sample\n```\n",
			"## Real\n\n```markdown\n## Sample\n```\n",
		},
		{
			"a fence without a section is not a sample",
			"Prose.\n\n```sh\necho hi\n```\n",
			"Prose.\n\n```sh\necho hi\n```\n",
		},
		{
			"only the first fence is the sample",
			"```markdown\n## A\n```\n\n```markdown\n## B\n```\n",
			"## A\n",
		},
		{
			"an unclosed sample runs to the end",
			"Prose.\n\n```markdown\n## A\n\ntext\n",
			"## A\n\ntext\n",
		},
		{
			"an opener with an info string ends an unclosed sample",
			"```markdown\n## A\n```markdown\n## B\n```\n",
			"## A\n",
		},
		{
			"an indented fence delimits the sample like templateH2 reads it",
			"  ```markdown\n## A\n  ```\n",
			"## A\n",
		},
		{"empty body", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := TemplatePageBody(tc.body); got != tc.want {
				t.Errorf("TemplatePageBody() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The page concept_new renders must satisfy the contract the section check
// reads, so the sections of the rendered body are the sections the template
// promises, whatever its shape.
func TestTemplatePageBodyKeepsTheTemplateSections(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		"# T\n\n## A\n\n## B\n",
		"Guidance.\n\n```markdown\n# T\n\n## A\n\n## B\n```\n",
		"## Real\n\n```markdown\n## Sample\n```\n",
		"Prose.\n\n```sh\necho hi\n```\n",
		"```markdown\n## A\n```\n\n```markdown\n## B\n```\n",
		"```markdown\n## A\n```markdown\n## B\n```\n",
		"```markdown\n## A\n\ntext\n",
		"",
	} {
		want := templateH2(body)
		if got := templateH2(TemplatePageBody(body)); !reflect.DeepEqual(got, want) {
			t.Errorf("sections of the rendered body %v != sections of the template %v for %q", got, want, body)
		}
	}
}
