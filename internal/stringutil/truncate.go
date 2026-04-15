// Package stringutil provides UTF-8 safe string manipulation utilities
// shared across the wuu codebase.
package stringutil

import "unicode/utf8"

// Truncate returns s shortened to at most maxBytes bytes without
// breaking a multi-byte UTF-8 character at the boundary. If the
// string was shortened, suffix is appended (the total result may
// exceed maxBytes by len(suffix)).
//
// This replaces the naive s[:n] pattern that corrupts emoji, CJK,
// and other multi-byte text. All truncation call-sites in the
// codebase should use this function.
func Truncate(s string, maxBytes int, suffix string) string {
	if len(s) <= maxBytes {
		return s
	}
	// Walk backward from maxBytes until we land on a valid UTF-8
	// character start (i.e. not a continuation byte 10xxxxxx).
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + suffix
}

// TruncateRunes returns s shortened to at most maxRunes runes
// without breaking characters, appending suffix if shortened.
func TruncateRunes(s string, maxRunes int, suffix string) string {
	n := utf8.RuneCountInString(s)
	if n <= maxRunes {
		return s
	}
	i := 0
	for count := 0; count < maxRunes; count++ {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return s[:i] + suffix
}

// HeadTail returns the first headBytes and last tailBytes of s,
// joined by a middle marker, without breaking UTF-8 boundaries.
// If s fits in headBytes+tailBytes, it is returned as-is.
func HeadTail(s string, headBytes, tailBytes int, middle string) string {
	if len(s) <= headBytes+tailBytes {
		return s
	}
	head := safePrefix(s, headBytes)
	tail := safeSuffix(s, tailBytes)
	return head + middle + tail
}

// safePrefix returns at most maxBytes from the start of s, not
// splitting a multi-byte character.
func safePrefix(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// safeSuffix returns at most maxBytes from the end of s, not
// splitting a multi-byte character.
func safeSuffix(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	// Walk forward until we land on a character start.
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}
