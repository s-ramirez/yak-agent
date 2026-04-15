package imessage

import "testing"

func TestDropMetaLines(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"[tokens: prompt=10 completion=5 total=15]", ""},
		{"[compacted: 4000 → 800 tokens]", ""},
		{"[compaction failed: context error]", ""},
		{"Hello!\n[tokens: prompt=10 completion=5 total=15]", "Hello!"},
		{"[tokens: prompt=10 completion=5 total=15]\nSure, here you go.", "Sure, here you go."},
		// Mid-sentence brackets should not be stripped.
		{"Normal reply with [bracketed] word inside.", "Normal reply with [bracketed] word inside."},
		{"Hello!\n\nHow are you?", "Hello!\n\nHow are you?"},
	}
	for _, tt := range tests {
		got := dropMetaLines(tt.in)
		if got != tt.want {
			t.Errorf("dropMetaLines(%q)\n  got  %q\n  want %q", tt.in, got, tt.want)
		}
	}
}

func TestStripMarkdown(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"Hello, world!", "Hello, world!"},
		{"**bold**", "bold"},
		{"__bold__", "bold"},
		{"*italic*", "italic"},
		{"_italic_", "italic"},
		{"~~strike~~", "strike"},
		{"# Header", "Header"},
		{"### Deep header", "Deep header"},
		{"> blockquote", "blockquote"},
		{"`inline code`", "inline code"},
		{"---", ""},
		{"***", ""},
		{"___", ""},
		{"**bold** and *italic*", "bold and italic"},
		{"First\n\n\n\nSecond", "First\n\nSecond"},
		{"  spaces  ", "spaces"},
	}
	for _, tt := range tests {
		got := stripMarkdown(tt.in)
		if got != tt.want {
			t.Errorf("stripMarkdown(%q)\n  got  %q\n  want %q", tt.in, got, tt.want)
		}
	}
}
