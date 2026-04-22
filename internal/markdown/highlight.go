package markdown

import (
	"strings"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// HighlightCode applies syntax highlighting to a code block. Results are
// memoized by (lang, code) in a process-global LRU so repeated renders of
// the same source (common in streaming markdown, where every newline commit
// re-renders the accumulated buffer) are served in microseconds instead of
// re-running chroma's regex lexer.
//
// The style is hardcoded to "monokai" below and does not depend on the TUI
// theme, so theme switches do not require cache invalidation. If the style
// is ever made configurable, include it in highlightKey.
func HighlightCode(code, lang string) string {
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return code
	}
	if len(code) > highlightCacheMaxCode {
		return highlightUncached(code, lang)
	}
	key := highlightKey{lang: strings.ToLower(lang), code: code}
	if v, ok := highlightCacheSingleton.Get(key); ok {
		return v
	}
	v := highlightUncached(code, lang)
	highlightCacheSingleton.Put(key, v)
	return v
}

func highlightUncached(code, lang string) string {
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
