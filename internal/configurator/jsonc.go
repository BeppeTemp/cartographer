package configurator

import "encoding/json"

// UnmarshalProviderJSON decodes a provider-owned JSON configuration. Antigravity
// accepts JSONC in its native files, so comments and trailing commas are removed
// lexically before standard JSON decoding. Rewrites normalize the file to JSON
// while preserving its semantic content and all unknown keys.
func UnmarshalProviderJSON(provider Provider, data []byte, v any) error {
	if provider == ProviderAntigravity {
		data = normalizeJSONC(data)
	}
	return json.Unmarshal(data, v)
}

func normalizeJSONC(data []byte) []byte {
	out := append([]byte(nil), data...)
	inString := false
	escaped := false
	for i := 0; i < len(out); i++ {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if out[i] == '\\' {
				escaped = true
			} else if out[i] == '"' {
				inString = false
			}
			continue
		}
		if out[i] == '"' {
			inString = true
			continue
		}
		if out[i] != '/' || i+1 >= len(out) {
			continue
		}
		switch out[i+1] {
		case '/':
			out[i], out[i+1] = ' ', ' '
			for i += 2; i < len(out) && out[i] != '\n' && out[i] != '\r'; i++ {
				out[i] = ' '
			}
			i--
		case '*':
			out[i], out[i+1] = ' ', ' '
			for i += 2; i < len(out); i++ {
				if out[i] == '*' && i+1 < len(out) && out[i+1] == '/' {
					out[i], out[i+1] = ' ', ' '
					i++
					break
				}
				if out[i] != '\n' && out[i] != '\r' {
					out[i] = ' '
				}
			}
		}
	}

	inString, escaped = false, false
	for i := 0; i < len(out); i++ {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if out[i] == '\\' {
				escaped = true
			} else if out[i] == '"' {
				inString = false
			}
			continue
		}
		if out[i] == '"' {
			inString = true
			continue
		}
		if out[i] != ',' {
			continue
		}
		j := i + 1
		for j < len(out) && (out[j] == ' ' || out[j] == '\t' || out[j] == '\n' || out[j] == '\r') {
			j++
		}
		if j < len(out) && (out[j] == '}' || out[j] == ']') {
			out[i] = ' '
		}
	}
	return out
}
