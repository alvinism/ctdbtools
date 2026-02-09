// Package metadata provides audio metadata extraction and normalization.
package metadata

import "strings"

// NormalizeTagKey converts format-specific tag names to Vorbis comment format.
// This allows consistent handling of tags from different source formats.
//
// Mapping:
// - M4A (iTunes): ©ART, ©ALB, ©NAM, etc. -> ARTIST, ALBUM, TITLE
// - WAV (RIFF INFO): IART, IPRD, INAM, etc. -> ARTIST, ALBUM, TITLE
// - ID3 (MP3): TPE1, TALB, TIT2, etc. -> ARTIST, ALBUM, TITLE
// - FLAC (Vorbis): Already normalized, pass through
func NormalizeTagKey(key string) string {
	// Normalize to uppercase for consistent lookup
	keyUpper := strings.ToUpper(key)

	// Check mapping table
	if normalized, ok := tagMappings[keyUpper]; ok {
		return normalized
	}

	// Pass through unknown keys as uppercase
	return keyUpper
}

// tagMappings maps format-specific tags to normalized Vorbis comment names.
var tagMappings = map[string]string{
	// M4A / iTunes tags (©XXX format, stored without © in ffprobe output)
	"©ART":        "ARTIST",
	"©ALB":        "ALBUM",
	"©NAM":        "TITLE",
	"©WRT":        "COMPOSER",
	"©DAY":        "DATE",
	"©GEN":        "GENRE",
	"©CMT":        "COMMENT",
	"©TRK":        "TRACKNUMBER",
	"©TOO":        "ENCODER",
	"AART":        "ALBUMARTIST",
	"CPRT":        "COPYRIGHT",
	"DISK":        "DISCNUMBER",
	"TRKN":        "TRACKNUMBER",
	"SOAR":        "ARTISTSORT",
	"SOAL":        "ALBUMSORT",
	"SONM":        "TITLESORT",
	"SOAA":        "ALBUMARTISTSORT",

	// ffprobe often reports these without the copyright symbol
	"ART":     "ARTIST",
	"ALB":     "ALBUM",
	"NAM":     "TITLE",
	"WRT":     "COMPOSER",
	"DAY":     "DATE",
	"GEN":     "GENRE",
	"CMT":     "COMMENT",
	"TRK":     "TRACKNUMBER",
	"TOO":     "ENCODER",

	// WAV RIFF INFO tags (IXXXX format)
	"IART": "ARTIST",
	"IPRD": "ALBUM",
	"INAM": "TITLE",
	"IGNR": "GENRE",
	"ICRD": "DATE",
	"ICMT": "COMMENT",
	"ITRK": "TRACKNUMBER",
	"ISFT": "ENCODER",
	"ICOP": "COPYRIGHT",
	"ISBJ": "DESCRIPTION",

	// ID3v2 tags (MP3)
	"TPE1": "ARTIST",
	"TPE2": "ALBUMARTIST",
	"TALB": "ALBUM",
	"TIT2": "TITLE",
	"TCOM": "COMPOSER",
	"TYER": "DATE",
	"TDRC": "DATE",
	"TCON": "GENRE",
	"COMM": "COMMENT",
	"TRCK": "TRACKNUMBER",
	"TPOS": "DISCNUMBER",
	"TENC": "ENCODER",
	"TCOP": "COPYRIGHT",
	"TSOA": "ALBUMSORT",
	"TSOP": "ARTISTSORT",
	"TSOT": "TITLESORT",

	// Common variations (lowercase from some tools)
	"ARTIST":      "ARTIST",
	"ALBUM":       "ALBUM",
	"TITLE":       "TITLE",
	"DATE":        "DATE",
	"YEAR":        "DATE",
	"GENRE":       "GENRE",
	"COMPOSER":    "COMPOSER",
	"COMMENT":     "COMMENT",
	"TRACK":       "TRACKNUMBER",
	"TRACKNUMBER": "TRACKNUMBER",
	"DISCNUMBER":  "DISCNUMBER",
	"DISC":        "DISCNUMBER",
	"ALBUMARTIST": "ALBUMARTIST",
	"ALBUM_ARTIST": "ALBUMARTIST",
	"ALBUM ARTIST": "ALBUMARTIST",
	"ENCODER":     "ENCODER",
	"COPYRIGHT":   "COPYRIGHT",

	// ReplayGain tags (preserve as-is but normalize case)
	"REPLAYGAIN_TRACK_GAIN": "REPLAYGAIN_TRACK_GAIN",
	"REPLAYGAIN_TRACK_PEAK": "REPLAYGAIN_TRACK_PEAK",
	"REPLAYGAIN_ALBUM_GAIN": "REPLAYGAIN_ALBUM_GAIN",
	"REPLAYGAIN_ALBUM_PEAK": "REPLAYGAIN_ALBUM_PEAK",
}

// IsTrackSpecificTag returns true if the tag should be overwritten per-track
// rather than copied from album metadata.
func IsTrackSpecificTag(key string) bool {
	normalized := NormalizeTagKey(key)
	switch normalized {
	case "TITLE", "TRACKNUMBER", "ISRC",
		"REPLAYGAIN_TRACK_GAIN", "REPLAYGAIN_TRACK_PEAK":
		return true
	}
	return false
}

// IsAlbumTag returns true if the tag applies to the entire album.
func IsAlbumTag(key string) bool {
	normalized := NormalizeTagKey(key)
	switch normalized {
	case "ALBUM", "ALBUMARTIST", "ARTIST", "DATE", "GENRE", "COMPOSER",
		"COPYRIGHT", "DISCNUMBER", "TOTALDISCS", "TOTALTRACKS",
		"REPLAYGAIN_ALBUM_GAIN", "REPLAYGAIN_ALBUM_PEAK":
		return true
	}
	return false
}
