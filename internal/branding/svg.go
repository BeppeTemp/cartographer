package branding

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// activeElements never appear in a logo: they run code, load a document or
// animate an attribute the checks below judged static.
var activeElements = map[string]bool{
	"script": true, "foreignobject": true, "iframe": true, "object": true, "embed": true, "set": true,
}

// svgProblem reports why the bytes are not a static SVG, or "". An SVG is
// opened only through <img> and icon links, where script never runs, but the
// route serves it as a document too: it must hold no active content at all.
func svgProblem(data []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	first := true
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "not valid XML: " + err.Error()
		}
		switch t := tok.(type) {
		case xml.Directive:
			return "holds a DOCTYPE or entity declaration"
		case xml.ProcInst:
			if t.Target != "xml" {
				return "holds the processing instruction " + t.Target
			}
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if first && name != "svg" {
				return "the root element is not svg"
			}
			first = false
			if activeElements[name] || strings.HasPrefix(name, "animate") {
				return "holds the element " + t.Name.Local
			}
			for _, a := range t.Attr {
				if msg := attrProblem(t.Name.Local, a); msg != "" {
					return msg
				}
			}
		case xml.CharData:
			// style elements are judged by their text below.
		}
	}
	if first {
		return "no root element"
	}
	// <style> is judged by its text, in a second pass that knows the open element.
	return styleProblem(data)
}

func attrProblem(el string, a xml.Attr) string {
	n := strings.ToLower(a.Name.Local)
	switch {
	case strings.HasPrefix(n, "on"):
		return "holds the event attribute " + a.Name.Local
	case n == "href" && !strings.HasPrefix(a.Value, "#"):
		return fmt.Sprintf("the %s of %s points outside the file (%q)", a.Name.Local, el, a.Value)
	case cssProblem(a.Value):
		return fmt.Sprintf("the %s attribute of %s refers outside the file", a.Name.Local, el)
	}
	return ""
}

// styleProblem judges each <style> element as the browser reads it: the text
// nodes from its start to its end tag are one stylesheet, so a comment or a
// CDATA section cannot split `@import` or `url(` across tokens. A comment, a
// processing instruction, a directive or a child element inside <style> is
// refused: there is no honest reason for one in a logo.
func styleProblem(data []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	in := false
	var css strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if in {
				return "the style element holds the element " + t.Name.Local
			}
			if in = strings.EqualFold(t.Name.Local, "style"); in {
				css.Reset()
			}
		case xml.EndElement:
			if in && cssProblem(css.String()) {
				return "the style element refers outside the file (@import, url(), a loading function or an escape)"
			}
			in = false
		case xml.CharData:
			if in {
				css.Write(t)
			}
		case xml.Comment, xml.ProcInst, xml.Directive:
			if in {
				return "the style element holds a comment or an instruction"
			}
		}
	}
}

// cssLoaders are CSS functions that load a resource without a url( token.
var cssLoaders = []string{"@import", "image-set(", "src(", "cross-fade("}

// cssProblem reports CSS that can refer outside the file: any backslash (an
// escape hides url( and @import from a literal check), a loading function, or a
// url( whose target is not a #fragment.
func cssProblem(s string) bool {
	l := strings.ToLower(s)
	if strings.Contains(l, "\\") {
		return true
	}
	for _, f := range cssLoaders {
		if strings.Contains(l, f) {
			return true
		}
	}
	for {
		i := strings.Index(l, "url(")
		if i < 0 {
			return false
		}
		l = l[i+4:]
		t := strings.TrimLeft(l, " \t\r\n'\"")
		if !strings.HasPrefix(t, "#") {
			return true
		}
	}
}
