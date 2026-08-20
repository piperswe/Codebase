package views

import (
	"bytes"

	"github.com/a-h/templ"
	"github.com/yuin/goldmark"
)

var markdownRenderer = goldmark.New()

func renderMarkdown(s string) templ.Component {
	var buf bytes.Buffer
	_ = markdownRenderer.Convert([]byte(s), &buf)
	return templ.Raw(buf.String())
}
