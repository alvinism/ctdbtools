package audio

import (
	"strings"
	"testing"
)

func TestMetadata_Clone(t *testing.T) {
	original := &Metadata{
		Title:  "Test Title",
		Artist: "Test Artist",
		Album:  "Test Album",
		Tags:   map[string]string{"GENRE": "Rock", "DATE": "2024"},
	}

	clone := original.Clone()

	// Verify values are copied
	if clone.Title != original.Title {
		t.Errorf("Title mismatch: got %q, want %q", clone.Title, original.Title)
	}
	if clone.Artist != original.Artist {
		t.Errorf("Artist mismatch: got %q, want %q", clone.Artist, original.Artist)
	}
	if clone.Album != original.Album {
		t.Errorf("Album mismatch: got %q, want %q", clone.Album, original.Album)
	}

	// Verify tags are copied
	for k, v := range original.Tags {
		if clone.Tags[k] != v {
			t.Errorf("Tag %q mismatch: got %q, want %q", k, clone.Tags[k], v)
		}
	}

	// Verify it's a deep copy (modifying clone doesn't affect original)
	clone.Title = "Modified"
	clone.Tags["GENRE"] = "Jazz"
	if original.Title == "Modified" {
		t.Error("Clone is not independent - Title was modified")
	}
	if original.Tags["GENRE"] == "Jazz" {
		t.Error("Clone is not independent - Tags map was modified")
	}
}

func TestMetadata_Get(t *testing.T) {
	meta := &Metadata{
		Title:  "Test Title",
		Artist: "Test Artist",
		Album:  "Test Album",
		Tags:   map[string]string{"GENRE": "Rock", "DATE": "2024"},
	}

	tests := []struct {
		key      string
		expected string
	}{
		{"TITLE", "Test Title"},
		{"title", "Test Title"},
		{"ARTIST", "Test Artist"},
		{"artist", "Test Artist"},
		{"ALBUM", "Test Album"},
		{"GENRE", "Rock"},
		{"genre", "Rock"},
		{"DATE", "2024"},
		{"UNKNOWN", ""},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			result := meta.Get(tt.key)
			if result != tt.expected {
				t.Errorf("Get(%q) = %q, want %q", tt.key, result, tt.expected)
			}
		})
	}
}

func TestMetadata_Set(t *testing.T) {
	meta := NewMetadata()

	meta.Set("TITLE", "Test Title")
	meta.Set("ARTIST", "Test Artist")
	meta.Set("GENRE", "Rock")

	if meta.Title != "Test Title" {
		t.Errorf("Title not set correctly: got %q", meta.Title)
	}
	if meta.Artist != "Test Artist" {
		t.Errorf("Artist not set correctly: got %q", meta.Artist)
	}
	if meta.Tags["GENRE"] != "Rock" {
		t.Errorf("GENRE tag not set correctly: got %q", meta.Tags["GENRE"])
	}

	// Test case normalization
	meta.Set("title", "Lower Case Title")
	if meta.Title != "Lower Case Title" {
		t.Errorf("lowercase title not set correctly: got %q", meta.Title)
	}
}

func TestMetadata_ToFFmpegArgs(t *testing.T) {
	meta := &Metadata{
		Title:  "Test Title",
		Artist: "Test Artist",
		Tags:   map[string]string{"GENRE": "Rock"},
	}

	args := meta.ToFFmpegArgs()

	// Check that args contain expected patterns
	argsStr := strings.Join(args, " ")
	if !strings.Contains(argsStr, "-metadata title=Test Title") {
		t.Error("Missing title metadata in FFmpeg args")
	}
	if !strings.Contains(argsStr, "-metadata artist=Test Artist") {
		t.Error("Missing artist metadata in FFmpeg args")
	}
	if !strings.Contains(argsStr, "-metadata GENRE=Rock") {
		t.Error("Missing GENRE metadata in FFmpeg args")
	}
}

func TestMetadata_ToVorbisCommentFormat(t *testing.T) {
	meta := &Metadata{
		Title:  "Test Title",
		Artist: "Test Artist",
		Album:  "Test Album",
		Tags:   map[string]string{"GENRE": "Rock", "DATE": "2024"},
	}

	output := meta.ToVorbisCommentFormat()

	// Check expected format
	if !strings.Contains(output, "TITLE=Test Title\n") {
		t.Error("Missing TITLE in Vorbis comment format")
	}
	if !strings.Contains(output, "ARTIST=Test Artist\n") {
		t.Error("Missing ARTIST in Vorbis comment format")
	}
	if !strings.Contains(output, "ALBUM=Test Album\n") {
		t.Error("Missing ALBUM in Vorbis comment format")
	}
	if !strings.Contains(output, "GENRE=Rock\n") {
		t.Error("Missing GENRE in Vorbis comment format")
	}
}

func TestMetadata_IsEmpty(t *testing.T) {
	tests := []struct {
		name     string
		meta     *Metadata
		expected bool
	}{
		{"nil", nil, true},
		{"empty", NewMetadata(), true},
		{"with title", &Metadata{Title: "Test"}, false},
		{"with artist", &Metadata{Artist: "Test"}, false},
		{"with album", &Metadata{Album: "Test"}, false},
		{"with tags", &Metadata{Tags: map[string]string{"GENRE": "Rock"}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.meta.IsEmpty()
			if result != tt.expected {
				t.Errorf("IsEmpty() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestSanitizeVorbisValue(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Normal text", "Normal text"},
		{"Text with \"quotes\"", "Text with \"quotes\""},
		{"Text with\nnewline", "Text with\nnewline"},
		{"Text with\x00null", "Text withnull"},
		{"日本語テキスト", "日本語テキスト"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := sanitizeVorbisValue(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeVorbisValue(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
