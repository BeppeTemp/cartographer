package configurator

import "testing"

func TestCheckTOMLSyntax(t *testing.T) {
	valid := []string{
		"",
		"# only a comment\n",
		"model = \"gpt\"\n[mcp_servers.cartographer]\nurl = \"http://x/mcp?kb=a&b=c\" # c\nenabled = true\n",
		"a.b = 1\n\"quoted key\" = 'lit'\n[[arr]]\nx = 1979-05-27 07:32:00\n",
		"list = [\n  \"a\", # c\n  [1, 2],\n]\ninline = { a = 1, b = [\"x\"] }\n",
		"s = \"\"\"\nmulti ] [ \"quoted\"\n\"\"\"\nt = '''\nraw ' \"\n'''\nafter = 1\n",
		"[hooks.state.\"/p/config.toml:x\"]\ntrusted_hash = \"sha256:ab\"\r\n",
		"e = \"a \\\" b\"\n",
	}
	for _, c := range valid {
		if err := CheckTOMLSyntax(c); err != nil {
			t.Errorf("valid TOML refused: %v\n%q", err, c)
		}
	}
	invalid := []string{
		"this is not toml\n",
		"[table\nx = 1\n",
		"[a] junk\n",
		"key =\n",
		"key = \"unterminated\n",
		"key = [1, 2\nother = 3\n",
		"key = \"\"\"never closed\n",
		"= 1\n",
		"key = \"a\" \"b\"\n",
		"key = 1]\n",
	}
	for _, c := range invalid {
		if err := CheckTOMLSyntax(c); err == nil {
			t.Errorf("invalid TOML accepted: %q", c)
		}
	}
}
