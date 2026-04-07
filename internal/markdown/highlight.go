package markdown

import (
	"strings"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// HighlightCode applies syntax highlighting to a code block.
// Returns the original code unchanged if the language is unknown or empty.
func HighlightCode(code, lang string) string {
	if strings.TrimSpace(lang) == "" {
		return code
	}

	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Fallback
	}

	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return code
	}

	formatter := formatters.Get("terminal16m")
	if formatter == nil {
		formatter = formatters.Fallback
	}

	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}

	var buf strings.Builder
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return code
	}
	return buf.String()
}
