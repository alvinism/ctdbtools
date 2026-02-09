package audio

import (
	"bytes"
	"strings"
)

// Metadata holds audio file metadata for future use.
// This is a stub for future extensibility to support metadata preservation
// during repair operations.
type Metadata struct {
	// Basic track info
	Title  string
	Artist string
	Album  string

	// Additional tags (format-specific)
	Tags map[string]string
}

// NewMetadata creates a new empty Metadata instance.
func NewMetadata() *Metadata {
	return &Metadata{
		Tags: make(map[string]string),
	}
}

// Clone creates a deep copy of the Metadata.
func (m *Metadata) Clone() *Metadata {
	if m == nil {
		return nil
	}
	clone := &Metadata{
		Title:  m.Title,
		Artist: m.Artist,
		Album:  m.Album,
		Tags:   make(map[string]string, len(m.Tags)),
	}
	for k, v := range m.Tags {
		clone.Tags[k] = v
	}
	return clone
}

// Get retrieves a tag value by key (case-insensitive).
// Returns empty string if not found.
func (m *Metadata) Get(key string) string {
	if m == nil {
		return ""
	}
	keyUpper := strings.ToUpper(key)
	// Check built-in fields first
	switch keyUpper {
	case "TITLE":
		return m.Title
	case "ARTIST":
		return m.Artist
	case "ALBUM":
		return m.Album
	}
	// Check Tags map
	if v, ok := m.Tags[keyUpper]; ok {
		return v
	}
	// Try original case
	if v, ok := m.Tags[key]; ok {
		return v
	}
	return ""
}

// Set sets a tag value by key (normalized to uppercase).
func (m *Metadata) Set(key, value string) {
	if m == nil {
		return
	}
	keyUpper := strings.ToUpper(key)
	// Set built-in fields
	switch keyUpper {
	case "TITLE":
		m.Title = value
	case "ARTIST":
		m.Artist = value
	case "ALBUM":
		m.Album = value
	default:
		if m.Tags == nil {
			m.Tags = make(map[string]string)
		}
		m.Tags[keyUpper] = value
	}
}

// ToFFmpegArgs returns FFmpeg -metadata arguments for all tags.
// Format: ["-metadata", "KEY=VALUE", "-metadata", "KEY2=VALUE2", ...]
func (m *Metadata) ToFFmpegArgs() []string {
	if m == nil {
		return nil
	}
	var args []string

	// Add built-in fields
	if m.Title != "" {
		args = append(args, "-metadata", "title="+m.Title)
	}
	if m.Artist != "" {
		args = append(args, "-metadata", "artist="+m.Artist)
	}
	if m.Album != "" {
		args = append(args, "-metadata", "album="+m.Album)
	}

	// Add all additional tags
	for key, value := range m.Tags {
		// Skip duplicates of built-in fields
		keyUpper := strings.ToUpper(key)
		if keyUpper == "TITLE" || keyUpper == "ARTIST" || keyUpper == "ALBUM" {
			continue
		}
		args = append(args, "-metadata", key+"="+value)
	}

	return args
}

// ToVorbisCommentFormat returns tags in Vorbis comment format (KEY=VALUE lines).
// Used for metaflac --import-tags-from.
// Sanitizes values by removing null bytes (invalid in Vorbis comments).
func (m *Metadata) ToVorbisCommentFormat() string {
	if m == nil {
		return ""
	}
	var buf bytes.Buffer

	// Add built-in fields
	if m.Title != "" {
		buf.WriteString("TITLE=")
		buf.WriteString(sanitizeVorbisValue(m.Title))
		buf.WriteByte('\n')
	}
	if m.Artist != "" {
		buf.WriteString("ARTIST=")
		buf.WriteString(sanitizeVorbisValue(m.Artist))
		buf.WriteByte('\n')
	}
	if m.Album != "" {
		buf.WriteString("ALBUM=")
		buf.WriteString(sanitizeVorbisValue(m.Album))
		buf.WriteByte('\n')
	}

	// Add all additional tags
	for key, value := range m.Tags {
		// Skip duplicates of built-in fields
		keyUpper := strings.ToUpper(key)
		if keyUpper == "TITLE" || keyUpper == "ARTIST" || keyUpper == "ALBUM" {
			continue
		}
		buf.WriteString(strings.ToUpper(key))
		buf.WriteByte('=')
		buf.WriteString(sanitizeVorbisValue(value))
		buf.WriteByte('\n')
	}

	return buf.String()
}

// sanitizeVorbisValue removes null bytes from a string.
// Null bytes are invalid in Vorbis comments but all other characters
// (including UTF-8, quotes, newlines) are valid.
func sanitizeVorbisValue(s string) string {
	return strings.ReplaceAll(s, "\x00", "")
}

// IsEmpty returns true if the metadata has no values.
func (m *Metadata) IsEmpty() bool {
	if m == nil {
		return true
	}
	return m.Title == "" && m.Artist == "" && m.Album == "" && len(m.Tags) == 0
}
