package ingest

import "ctdbtool/internal/toc"

// SourceSegment represents a contiguous audio segment from a file.
// This mirrors CueTools' SourceInfo struct.
type SourceSegment struct {
	FilePath string // Path to audio file (relative to CUE dir, empty for silence/pregap)
	Offset   int64  // Offset in samples within file (0 = file start)
	Length   int64  // Length in samples (0 = to end of file)
}

// CueSheet extends Layout with source file mapping for split track support.
type CueSheet struct {
	toc.Layout
	CueDir      string          // Directory containing CUE file
	Sources     []SourceSegment // Ordered audio segments (one per track typically)
	AudioLayout toc.Layout      // Layout with audio-only lengths for CRC calculation (may differ from Layout for split tracks)
}

// IsSplitTrack returns true if CUE uses multiple audio files.
func (c CueSheet) IsSplitTrack() bool {
	if len(c.Sources) == 0 {
		return false
	}
	first := c.Sources[0].FilePath
	for _, s := range c.Sources[1:] {
		if s.FilePath != first {
			return true
		}
	}
	return false
}

// SingleFilePath returns the audio file path if this is a single-file CUE,
// or empty string if split tracks or no sources.
func (c CueSheet) SingleFilePath() string {
	if len(c.Sources) == 0 {
		return ""
	}
	if c.IsSplitTrack() {
		return ""
	}
	return c.Sources[0].FilePath
}

// GetAudioLayout returns the layout to use for CRC calculation.
// For split tracks, this may have different track lengths than Layout (which is for TOC).
func (c CueSheet) GetAudioLayout() toc.Layout {
	if c.AudioLayout.AudioTracks > 0 {
		return c.AudioLayout
	}
	return c.Layout
}
