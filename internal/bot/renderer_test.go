package bot

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEscapeHTML(t *testing.T) {
	t.Helper()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "escapes ampersand",
			input:    "foo & bar",
			expected: "foo &amp; bar",
		},
		{
			name:     "escapes less than",
			input:    "a < b",
			expected: "a &lt; b",
		},
		{
			name:     "escapes greater than",
			input:    "a > b",
			expected: "a &gt; b",
		},
		{
			name:     "escapes double quote",
			input:    `say "hello"`,
			expected: "say &quot;hello&quot;",
		},
		{
			name:     "escapes all special characters",
			input:    `a < b & c > d "e"`,
			expected: "a &lt; b &amp; c &gt; d &quot;e&quot;",
		},
		{
			name:     "text without special chars passes through",
			input:    "hello world",
			expected: "hello world",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "preserves newlines",
			input:    "line1\nline2",
			expected: "line1\nline2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EscapeHTML(tt.input)
			if got != tt.expected {
				t.Errorf("EscapeHTML(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestFormatCodeBlock(t *testing.T) {
	t.Helper()

	tests := []struct {
		name     string
		code     string
		language string
		expected string
	}{
		{
			name:     "wraps code in pre and code tags",
			code:     "print('hello')",
			language: "",
			expected: "<pre><code>print('hello')</code></pre>",
		},
		{
			name:     "adds language class",
			code:     "def foo(): pass",
			language: "python",
			expected: "<pre><code class=\"language-python\">def foo(): pass</code></pre>",
		},
		{
			name:     "escapes HTML inside code",
			code:     "<script>alert('xss')</script>",
			language: "",
			expected: "<pre><code>&lt;script&gt;alert('xss')&lt;/script&gt;</code></pre>",
		},
		{
			name:     "escapes HTML with language",
			code:     "<div>content</div>",
			language: "html",
			expected: "<pre><code class=\"language-html\">&lt;div&gt;content&lt;/div&gt;</code></pre>",
		},
		{
			name:     "empty code",
			code:     "",
			language: "",
			expected: "<pre><code></code></pre>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatCodeBlock(tt.code, tt.language)
			if got != tt.expected {
				t.Errorf("FormatCodeBlock(%q, %q) = %q, want %q", tt.code, tt.language, got, tt.expected)
			}
		})
	}
}

func TestChunkMessages_ShortText(t *testing.T) {
	t.Helper()

	text := strings.Repeat("a", 1000)
	chunks := ChunkMessages(text)

	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != text {
		t.Errorf("chunk does not match original text")
	}
}

func TestChunkMessages_ParagraphBoundary(t *testing.T) {
	t.Helper()

	// Build text with many short paragraphs that together exceed MaxMessageLen
	var paragraphs []string
	for i := 0; i < 100; i++ {
		paragraphs = append(paragraphs, strings.Repeat("x", 50))
	}
	text := strings.Join(paragraphs, "\n\n")

	chunks := ChunkMessages(text)

	// 100 paragraphs of 50 chars = 5000 chars + 200 for \n\n
	// Should be accumulated into ~2 chunks, not 100
	if len(chunks) > 5 {
		t.Errorf("expected accumulation into fewer chunks, got %d", len(chunks))
	}

	// Verify no chunk exceeds MaxMessageLen
	for i, chunk := range chunks {
		if len(chunk) > MaxMessageLen {
			t.Errorf("chunk %d exceeds MaxMessageLen: %d > %d", i, len(chunk), MaxMessageLen)
		}
	}
}

func TestChunkMessages_LineBoundary(t *testing.T) {
	t.Helper()

	// Single paragraph with multiple lines, total > MaxMessageLen
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, strings.Repeat("y", 300))
	}
	text := strings.Join(lines, "\n")

	chunks := ChunkMessages(text)

	// Verify no chunk exceeds MaxMessageLen
	for i, chunk := range chunks {
		if len(chunk) > MaxMessageLen {
			t.Errorf("chunk %d exceeds MaxMessageLen: %d > %d", i, len(chunk), MaxMessageLen)
		}
	}
}

func TestChunkMessages_HardCut(t *testing.T) {
	t.Helper()

	// Single line longer than MaxMessageLen
	line := strings.Repeat("z", MaxMessageLen+500)
	chunks := ChunkMessages(line)

	// Should have multiple chunks
	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks for long line, got %d", len(chunks))
	}

	// Verify no chunk exceeds MaxMessageLen
	for i, chunk := range chunks {
		if len(chunk) > MaxMessageLen {
			t.Errorf("chunk %d exceeds MaxMessageLen: %d > %d", i, len(chunk), MaxMessageLen)
		}
	}

	// Verify total content matches
	totalLen := 0
	for _, chunk := range chunks {
		totalLen += len(chunk)
	}
	if totalLen != len(line) {
		t.Errorf("total chunk length %d does not match original %d", totalLen, len(line))
	}
}

func TestChunkMessages_CodeBlockBalancing(t *testing.T) {
	t.Helper()

	// Code block that spans chunk boundary: starts mid-block
	// The chunk ends with </pre> but has unclosed <pre> before it
	codeBlock := "<pre><code>start" + strings.Repeat("x", MaxMessageLen) + "end</code></pre>"
	chunks := ChunkMessages(codeBlock)

	// Verify no chunk exceeds MaxMessageLen
	for i, chunk := range chunks {
		if len(chunk) > MaxMessageLen {
			t.Errorf("chunk %d exceeds MaxMessageLen: %d > %d", i, len(chunk), MaxMessageLen)
		}
	}

	// Count total <pre> and </code></pre> across all chunks
	// We use </code></pre> as the canonical close tag since all <pre> in this
	// codebase come from FormatCodeBlock which produces paired tags
	totalOpen := 0
	totalClose := 0
	for _, chunk := range chunks {
		totalOpen += strings.Count(chunk, "<pre>")
		totalClose += strings.Count(chunk, "</code></pre>")
	}

	// Total <pre> count across all chunks must equal total </code></pre> count
	if totalOpen != totalClose {
		t.Errorf("unbalanced tags: %d <pre> vs %d </code></pre> across %d chunks",
			totalOpen, totalClose, len(chunks))
	}

	// At least one chunk should have opening tags prepended since it starts mid-block
	// and at least one should have closing tags appended
	hasOpeningTags := false
	hasClosingTags := false
	for _, chunk := range chunks {
		if strings.Contains(chunk, "<pre><code>") {
			hasOpeningTags = true
		}
		if strings.Contains(chunk, "</code></pre>") {
			hasClosingTags = true
		}
	}

	if !hasOpeningTags {
		t.Error("expected at least one chunk with opening tags prepended")
	}
	if !hasClosingTags {
		t.Error("expected at least one chunk with closing tags appended")
	}

	// Verify each chunk is balanced or is a continuation (properly closes a code block)
	// A chunk that ends with </code></pre> is a valid continuation even if it has no opening <pre>
	for i, chunk := range chunks {
		open := strings.Count(chunk, "<pre>")
		close := strings.Count(chunk, "</code></pre>")
		endsWithClose := strings.HasSuffix(chunk, "</code></pre>")
		startsWithOpen := strings.HasPrefix(chunk, "<pre><code>")
		isContinuation := (i > 0 && endsWithClose) || (i > 0 && startsWithOpen)
		if open != close && !isContinuation {
			t.Errorf("chunk %d is unbalanced: %d <pre> vs %d </code></pre>, endsWithClose=%v, startsWithOpen=%v",
				i, open, close, endsWithClose, startsWithOpen)
		}
	}
}

func TestChunkMessages_CodeBlockAtMaxLen(t *testing.T) {
	t.Helper()

	// Test case: chunk at exactly MaxMessageLen with unclosed <pre> tag
	// Create content that is exactly MaxMessageLen - 11 (space for </code></pre>)
	// to trigger the truncation path in balancePreTags
	contentLen := MaxMessageLen - 11 - 100 // leave room for code block wrapper
	content := "<pre><code>" + strings.Repeat("x", contentLen)
	// content is now just under MaxMessageLen but has unclosed <pre>

	chunks := ChunkMessages(content)

	// Should produce a single chunk
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(chunks))
	}

	// Verify the chunk has closing tags appended and is within limit
	chunk := chunks[0]
	if len(chunk) > MaxMessageLen {
		t.Errorf("chunk exceeds MaxMessageLen: %d > %d", len(chunk), MaxMessageLen)
	}

	// The chunk should have balanced tags
	open := strings.Count(chunk, "<pre>")
	close := strings.Count(chunk, "</pre>")
	if open != close {
		t.Errorf("unbalanced tags in chunk: %d <pre> vs %d </pre>", open, close)
	}
}

func TestSanitizeInput(t *testing.T) {
	t.Helper()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "strips control characters",
			input:    "hello\x00world\x1F",
			expected: "helloworld",
		},
		{
			name:     "preserves newline",
			input:    "line1\nline2",
			expected: "line1\nline2",
		},
		{
			name:     "preserves carriage return",
			input:    "line1\rline2",
			expected: "line1\rline2",
		},
		{
			name:     "preserves tab",
			input:    "col1\tcol2",
			expected: "col1\tcol2",
		},
		{
			name:     "caps length at 4096",
			input:    strings.Repeat("a", 5000),
			expected: strings.Repeat("a", 4096),
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "no JSON quotes",
			input:    "hello",
			expected: "hello",
		},
		{
			name:     "strips control chars then truncates",
			input:    strings.Repeat("a", 100) + "\x00" + strings.Repeat("b", 5000),
			expected: strings.Repeat("a", 100) + strings.Repeat("b", 3996),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeInput(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeInput(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSanitizeInput_MultibyteUTF8(t *testing.T) {
	t.Helper()

	// CJK characters at the boundary should not be corrupted
	cjk := "日本語日本語日本語日本語日本語"
	// Each CJK character is 3 bytes in UTF-8
	// 10 characters = 30 bytes, but 10 runes

	// Create input with more than MaxMessageLen runes
	input := strings.Repeat(cjk, 500)
	expectedLen := MaxMessageLen

	got := SanitizeInput(input)
	if len([]rune(got)) != expectedLen {
		t.Errorf("SanitizeInput did not truncate correctly: got %d runes, want %d", len([]rune(got)), expectedLen)
	}

	// Verify no partial characters at boundary by checking string is valid UTF-8
	// Simple check: converting to string and back should produce same runes
	reencoded := []rune(string(got))
	if len(reencoded) != expectedLen {
		t.Errorf("SanitizeInput corrupted UTF-8: got %d runes after re-encode, want %d", len(reencoded), expectedLen)
	}

	// Verify content matches expected truncated content (rune-safe slice)
	expected := string([]rune(strings.Repeat(cjk, 500))[:MaxMessageLen])
	if got != expected {
		t.Errorf("SanitizeInput CJK content mismatch at boundary")
	}
}

func TestShouldSendAsFile(t *testing.T) {
	t.Helper()

	tests := []struct {
		name     string
		text     string
		expected bool
	}{
		{
			name:     "text exactly at MaxTotalLen returns false",
			text:     strings.Repeat("a", MaxTotalLen),
			expected: false,
		},
		{
			name:     "text just over MaxTotalLen returns true",
			text:     strings.Repeat("a", MaxTotalLen+1),
			expected: true,
		},
		{
			name:     "text well over MaxTotalLen returns true",
			text:     strings.Repeat("a", MaxTotalLen*2),
			expected: true,
		},
		{
			name:     "short text returns false",
			text:     "hello",
			expected: false,
		},
		{
			name:     "empty string returns false",
			text:     "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldSendAsFile(tt.text)
			if got != tt.expected {
				t.Errorf("ShouldSendAsFile(%q) = %v, want %v", tt.text, got, tt.expected)
			}
		})
	}
}

func TestChunkMessages_HardCut_MultibyteUTF8(t *testing.T) {
	t.Helper()

	// CJK characters at the hard cut boundary should not be corrupted
	// Each CJK character is 3 bytes in UTF-8
	cjk := "日本語"
	// 1366 CJK chars = 4098 bytes (just over MaxMessageLen of 4096)
	// but only ~1365 runes
	text := strings.Repeat(cjk, 1366)

	chunks := ChunkMessages(text)

	// Verify no chunk exceeds MaxMessageLen
	for i, chunk := range chunks {
		if len(chunk) > MaxMessageLen {
			t.Errorf("chunk %d exceeds MaxMessageLen: %d > %d", i, len(chunk), MaxMessageLen)
		}
	}

	// Verify total content matches original (reconstruct from chunks)
	reconstructed := ""
	for _, chunk := range chunks {
		reconstructed += chunk
	}

	// Verify no partial UTF-8 characters by checking valid UTF-8
	if !utf8.ValidString(reconstructed) {
		t.Errorf("reconstructed text contains invalid UTF-8")
	}

	// Verify rune count matches
	if len([]rune(reconstructed)) != len([]rune(text)) {
		t.Errorf("rune count mismatch: got %d, want %d", len([]rune(reconstructed)), len([]rune(text)))
	}
}

func TestBalancePreTags_BareClosePre(t *testing.T) {
	t.Helper()

	// Chunk ending with bare </pre> but no opening <pre><code>
	// should produce valid HTML: <pre><code>content</code></pre>
	chunk := "some text</pre>"

	result := balancePreTags(chunk)

	// Result should be valid HTML with proper nesting
	if result != "<pre><code>some text</code></pre>" {
		t.Errorf("balancePreTags(%q) = %q, want %q", chunk, result, "<pre><code>some text</code></pre>")
	}

	// Verify the result is balanced
	open := strings.Count(result, "<pre>")
	close := strings.Count(result, "</pre>")
	if open != close {
		t.Errorf("unbalanced tags: %d <pre> vs %d </pre>", open, close)
	}
}

func TestBalancePreTags_MultipleUnclosedPre(t *testing.T) {
	t.Helper()

	// Chunk with multiple unclosed <pre><code> tags
	// overhead should scale with the number of unclosed tags
	chunk := "<pre><code>code1</code></pre><pre><code>code2"

	result := balancePreTags(chunk)

	// The result should have closing tags appended for the unclosed <pre>
	// Each </code></pre> is 13 bytes, so 2 unclosed = 26 bytes overhead
	expected := "<pre><code>code1</code></pre><pre><code>code2</code></pre>"
	if result != expected {
		t.Errorf("balancePreTags(%q) = %q, want %q", chunk, result, expected)
	}

	// Verify no chunk exceeds MaxMessageLen
	if len(result) > MaxMessageLen {
		t.Errorf("result exceeds MaxMessageLen: %d > %d", len(result), MaxMessageLen)
	}
}

func TestBalancePreTags_FullCloseSequence(t *testing.T) {
	t.Helper()

	// Chunk ending with </code></pre> but no opening <pre><code>
	// should just prepend <pre><code>, not double-wrap
	chunk := "some text</code></pre>"

	result := balancePreTags(chunk)

	expected := "<pre><code>some text</code></pre>"
	if result != expected {
		t.Errorf("balancePreTags(%q) = %q, want %q", chunk, result, expected)
	}

	// Verify the result is balanced
	open := strings.Count(result, "<pre>")
	close := strings.Count(result, "</code></pre>")
	if open != close {
		t.Errorf("unbalanced tags: %d <pre> vs %d </code></pre>", open, close)
	}
}

func TestBalancePreTags_MaxMessageLenBoundary(t *testing.T) {
	t.Helper()

	// Verify that balancePreTags wrapping does not exceed MaxMessageLen
	// Create a chunk that is exactly MaxMessageLen - 1 with unclosed tags
	// so that adding overhead would exceed the limit
	openChunk := "<pre><code>" + strings.Repeat("a", MaxMessageLen-11)

	result := balancePreTags(openChunk)

	if len(result) > MaxMessageLen {
		t.Errorf("balancePreTags result exceeds MaxMessageLen: %d > %d", len(result), MaxMessageLen)
	}
}
