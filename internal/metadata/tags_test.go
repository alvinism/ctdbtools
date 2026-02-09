package metadata

import "testing"

func TestNormalizeTagKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// M4A iTunes tags
		{"©ART", "ARTIST"},
		{"©ALB", "ALBUM"},
		{"©NAM", "TITLE"},
		{"AART", "ALBUMARTIST"},

		// WAV RIFF INFO tags
		{"IART", "ARTIST"},
		{"IPRD", "ALBUM"},
		{"INAM", "TITLE"},

		// ID3v2 tags
		{"TPE1", "ARTIST"},
		{"TALB", "ALBUM"},
		{"TIT2", "TITLE"},
		{"TRCK", "TRACKNUMBER"},

		// Already normalized (Vorbis)
		{"ARTIST", "ARTIST"},
		{"ALBUM", "ALBUM"},
		{"TITLE", "TITLE"},

		// Common variations
		{"YEAR", "DATE"},
		{"TRACK", "TRACKNUMBER"},
		{"ALBUM_ARTIST", "ALBUMARTIST"},

		// Unknown key (pass through uppercase)
		{"CUSTOM_TAG", "CUSTOM_TAG"},
		{"custom_tag", "CUSTOM_TAG"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := NormalizeTagKey(tt.input)
			if result != tt.expected {
				t.Errorf("NormalizeTagKey(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsTrackSpecificTag(t *testing.T) {
	trackTags := []string{"TITLE", "TRACKNUMBER", "ISRC", "REPLAYGAIN_TRACK_GAIN"}
	albumTags := []string{"ARTIST", "ALBUM", "DATE", "GENRE"}

	for _, tag := range trackTags {
		if !IsTrackSpecificTag(tag) {
			t.Errorf("IsTrackSpecificTag(%q) = false, want true", tag)
		}
	}

	for _, tag := range albumTags {
		if IsTrackSpecificTag(tag) {
			t.Errorf("IsTrackSpecificTag(%q) = true, want false", tag)
		}
	}
}

func TestIsAlbumTag(t *testing.T) {
	albumTags := []string{"ALBUM", "ALBUMARTIST", "DATE", "GENRE", "DISCNUMBER"}
	trackTags := []string{"TITLE", "TRACKNUMBER"}

	for _, tag := range albumTags {
		if !IsAlbumTag(tag) {
			t.Errorf("IsAlbumTag(%q) = false, want true", tag)
		}
	}

	for _, tag := range trackTags {
		if IsAlbumTag(tag) {
			t.Errorf("IsAlbumTag(%q) = true, want false", tag)
		}
	}
}
