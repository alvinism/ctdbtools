// Package repair implements CD audio repair using CTDB parity data.
//
// This package provides functionality to correct errors in CD rips using
// Reed-Solomon error correction with parity data from the CUETools Database.
// The key algorithms are:
//
//   - Berlekamp-Massey: Finds the error locator polynomial
//   - Chien Search: Finds error positions
//   - Forney Algorithm: Computes error magnitudes
//
// The repair process reads audio data, calculates corrections, and writes
// a corrected output file.
package repair

import (
	"ctdbtools/internal/audio"
	"ctdbtools/internal/ingest"
	"ctdbtools/internal/network"
	"ctdbtools/internal/parity"
	"ctdbtools/internal/progress"
	"ctdbtools/internal/toc"
)

// RepairResult contains the outcome of a repair operation.
type RepairResult struct {
	InputPath    string
	OutputDir    string
	Entry        *network.CTDBEntry
	Offset       int // Detected drive offset in samples
	TotalErrors  int
	Corrections  []parity.ErrorCorrection // Sorted by position
	TrackResults []TrackRepairResult
	Success      bool
	CanRepair    bool
	ErrorMessage string
}

// TrackRepairResult contains repair information for a single track.
type TrackRepairResult struct {
	Track      int // 1-indexed
	ErrorCount int
	Positions  string // Error positions formatted as MM:SS:FF
	Repaired   bool
}

// RepairCandidate represents a CTDB entry that can be used for repair.
type RepairCandidate struct {
	Entry          *network.CTDBEntry
	Index          int
	Offset         int
	ErrorCount     int
	ErrorPositions string
	CanRepair      bool
	TrackErrors    []int
}

// OutputFiles contains paths to generated output files.
type OutputFiles struct {
	AudioPath       string   // Single-file mode (primary audio file)
	AudioPaths      []string // Split-track mode (one per track)
	WAVPath         string   // Deprecated: use AudioPath
	WAVPaths        []string // Deprecated: use AudioPaths
	CUEPath         string
	LogPath         string
	AccurateRipPath string
}

// RepairOptions configures a repair operation.
type RepairOptions struct {
	InputPath       string
	OutputDir       string
	Layout          toc.Layout
	Stride          int
	Npar            int
	Auto            bool
	DryRun          bool
	Force           bool
	Verbose         bool
	ShowProgress    bool
	Reporter        *progress.Reporter
	SampleCache     *ingest.SampleCache // Cached samples from Pass 1 to avoid re-decoding
	OriginalCuePath string              // Path to original CUE file for metadata preservation
	IsSplitTrack    bool
	SourceFiles     []string // Original source file paths for preserving filenames

	// Output format options
	Format       audio.OutputFormat      // Output format (wav, flac)
	Encoder      audio.EncoderPreference // FLAC encoder preference (auto, native, ffmpeg)
	Compression  int                     // FLAC compression level 0-8 (default 5)
	CopyMetadata bool                    // Copy metadata from source files to output

	// Per-track metadata (populated for split-track mode)
	PerTrackMetadata []*audio.Metadata // Metadata for each source file (split-track mode)
}
