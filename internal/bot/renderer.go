package bot

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxMessageLen is the maximum length of a Telegram message.
	MaxMessageLen = 4096

	// MaxTotalLen is the threshold above which content should be sent as a file attachment.
	MaxTotalLen = 16000
)

// SanitizeInput strips Unicode control characters from input while preserving
// \n, \r, and \t. It caps the length at 4096 characters (rune-safe).
func SanitizeInput(input string) string {
	// Strip control characters first (rune-safe)
	var builder strings.Builder
	builder.Grow(len(input))

	for _, r := range input {
		if r == '\n' || r == '\r' || r == '\t' {
			builder.WriteRune(r)
		} else if !unicode.IsControl(r) {
			builder.WriteRune(r)
		}
	}

	sanitized := builder.String()

	// Rune-safe truncation
	runes := []rune(sanitized)
	if len(runes) > MaxMessageLen {
		sanitized = string(runes[:MaxMessageLen])
	}

	return sanitized
}

// EscapeHTML escapes characters that have special meaning in Telegram HTML
// parse mode: &, <, >, and ".
func EscapeHTML(text string) string {
	var builder strings.Builder
	builder.Grow(len(text) + 32)

	for _, r := range text {
		switch r {
		case '&':
			builder.WriteString("&amp;")
		case '<':
			builder.WriteString("&lt;")
		case '>':
			builder.WriteString("&gt;")
		case '"':
			builder.WriteString("&quot;")
		default:
			builder.WriteRune(r)
		}
	}

	return builder.String()
}

// FormatCodeBlock wraps code in <pre><code> tags with an optional language
// class. HTML characters inside the code are escaped to prevent tag injection.
func FormatCodeBlock(code string, language string) string {
	escaped := EscapeHTML(code)

	var builder strings.Builder
	builder.WriteString("<pre><code")

	if language != "" {
		builder.WriteString(" class=\"language-")
		builder.WriteString(EscapeHTML(language))
		builder.WriteString("\"")
	}

	builder.WriteString(">")
	builder.WriteString(escaped)
	builder.WriteString("</code></pre>")

	return builder.String()
}

// countOpenCodeTags returns the number of unclosed <pre><code> tag pairs in text.
// This is more precise for detecting mid-block chunks that need <pre><code> prepended.
func countOpenCodeTags(text string) int {
	open := strings.Count(text, "<pre><code>")
	close := strings.Count(text, "</code></pre>")
	return open - close
}

const (
	preTagBalanceOverhead  = 13                                        // len("</code></pre>")
	preCodeBalanceOverhead = len("<pre><code>") + len("</code></pre>") // 24 bytes
	preCodeOpenOverhead    = len("<pre><code>")                        // 11 bytes
)

// runeSafeSlice returns s truncated to at most maxLen bytes,
// ensuring we only cut at valid UTF-8 rune boundaries.
func runeSafeSlice(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	// Find the last valid rune start position at or before maxLen
	for maxLen > 0 && !utf8.RuneStart(s[maxLen]) {
		maxLen--
	}
	if maxLen == 0 {
		return ""
	}
	return s[:maxLen]
}

// balancePreTags ensures <pre> tags are balanced in a chunk.
// If the chunk has unclosed <pre> tags at the end, it appends </code></pre>.
// If the chunk was cut mid-code-block (ends with </pre> but has no opening <pre><code>),
// it prepends <pre><code> to complete the broken close tag.
// If adding balancing tags would exceed MaxMessageLen, content is trimmed.
func balancePreTags(chunk string) string {
	// Detect if chunk ends with </code></pre> but has no opening <pre><code>
	// This happens when ChunkMessages cuts through a </code></pre> sequence
	endsWithFullClose := strings.HasSuffix(chunk, "</code></pre>")
	hasOpeningCode := strings.Contains(chunk, "<pre><code>")

	if endsWithFullClose && !hasOpeningCode {
		// Chunk ends with full close sequence but no opening — just prepend <pre><code>
		overhead := preCodeOpenOverhead
		if len(chunk)+overhead > MaxMessageLen {
			chunk = runeSafeSlice(chunk, MaxMessageLen-overhead)
		}
		chunk = "<pre><code>" + chunk
		return chunk
	}

	// Detect if chunk ends with bare </pre> but has no opening <pre><code>
	endsWithBareClose := strings.HasSuffix(chunk, "</pre>")
	if endsWithBareClose && !hasOpeningCode {
		// Chunk was cut mid-tag sequence, trim the bare </pre> and wrap properly
		chunk = strings.TrimSuffix(chunk, "</pre>")
		overhead := preCodeBalanceOverhead
		if len(chunk)+overhead > MaxMessageLen {
			// Trim content to make room (rune-safe)
			chunk = runeSafeSlice(chunk, MaxMessageLen-overhead)
		}
		chunk = "<pre><code>" + chunk + "</code></pre>"
	}

	// Handle any remaining unclosed tags
	open := countOpenCodeTags(chunk)
	if open > 0 {
		// Append </code></pre> for remaining unclosed tags
		// Trim content BEFORE appending closing tags to avoid truncating the tags
		overhead := preTagBalanceOverhead * open
		if len(chunk)+overhead > MaxMessageLen {
			chunk = runeSafeSlice(chunk, MaxMessageLen-overhead)
		}
		chunk += strings.Repeat("</code></pre>", open)
	}
	return chunk
}

// ChunkMessages splits text into chunks that respect Telegram's message length
// limit while preferring to break at paragraph boundaries (\n\n), then line
// boundaries (\n), and finally at the hard limit. Chunks are balanced for
// <pre> tag closures.
func ChunkMessages(text string) []string {
	if len(text) == 0 {
		return nil
	}

	if len(text) <= MaxMessageLen {
		return []string{balancePreTags(text)}
	}

	var chunks []string
	var accum strings.Builder

	for len(text) > 0 {
		// Calculate how much space we have in current chunk
		space := MaxMessageLen - accum.Len()

		if space <= 0 {
			// Flush current chunk
			chunk := balancePreTags(accum.String())
			chunks = append(chunks, chunk)
			accum.Reset()
			continue // Recalculate space with fresh accum
		}

		if len(text) <= space {
			// Remaining text fits in current chunk
			accum.WriteString(text)
			break
		}

		// Need to find a cut point within the space
		searchText := text[:space]

		// Try to find paragraph boundary first
		paraIdx := strings.LastIndex(searchText, "\n\n")
		if paraIdx >= 0 {
			accum.WriteString(text[:paraIdx+2])
			text = text[paraIdx+2:]
		} else {
			// Try line boundary
			lineIdx := strings.LastIndex(searchText, "\n")
			if lineIdx >= 0 {
				accum.WriteString(text[:lineIdx+1])
				text = text[lineIdx+1:]
			} else {
				// Hard cut - use rune-safe slicing to avoid splitting UTF-8
				safeEnd := len(runeSafeSlice(searchText, space))
				accum.WriteString(searchText[:safeEnd])
				text = text[safeEnd:]
			}
		}
	}

	// Flush remaining accumulated text
	if accum.Len() > 0 {
		chunks = append(chunks, balancePreTags(accum.String()))
	}

	return chunks
}

// ShouldSendAsFile returns true if the text length exceeds MaxTotalLen,
// indicating it should be sent as a file attachment instead of inline.
func ShouldSendAsFile(text string) bool {
	return len(text) > MaxTotalLen
}
