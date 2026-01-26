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
	"ctdbtools/internal/network"
	"ctdbtools/internal/parity"
	"ctdbtools/internal/progress"
	"ctdbtools/internal/toc"
)

// RepairResult contains the outcome of a repair operation.
type RepairResult struct {
	// Input file path
	InputPath string

	// Output directory path
	OutputDir string

	// CTDB entry used for repair
	Entry *network.CTDBEntry

	// Detected drive offset in samples
	Offset int

	// Total number of errors found
	TotalErrors int

	// Corrections to apply (sorted by position)
	Corrections []parity.ErrorCorrection

	// Per-track repair results
	TrackResults []TrackRepairResult

	// Whether repair was successful
	Success bool

	// Whether the errors are correctable
	CanRepair bool

	// Error message if repair failed
	ErrorMessage string
}

// TrackRepairResult contains repair information for a single track.
type TrackRepairResult struct {
	// Track number (1-indexed)
	Track int

	// Number of errors in this track
	ErrorCount int

	// Error positions formatted as MM:SS:FF
	Positions string

	// Whether this track was repaired
	Repaired bool
}

// RepairCandidate represents a CTDB entry that can be used for repair.
type RepairCandidate struct {
	// The CTDB entry
	Entry *network.CTDBEntry

	// Entry index in the response
	Index int

	// Detected offset for this entry
	Offset int

	// Number of errors detected
	ErrorCount int

	// Formatted error positions
	ErrorPositions string

	// Whether errors can be corrected
	CanRepair bool

	// Per-track error counts
	TrackErrors []int
}

// OutputFiles contains paths to generated output files.
type OutputFiles struct {
	// Path to output WAV file
	WAVPath string

	// Path to output CUE file
	CUEPath string

	// Path to repair log file
	LogPath string

	// Path to AccurateRip verification file (optional)
	AccurateRipPath string
}

// RepairOptions configures a repair operation.
type RepairOptions struct {
	// Input path (CUE file or directory)
	InputPath string

	// Output directory path
	OutputDir string

	// TOC layout from input
	Layout toc.Layout

	// Parity stride
	Stride int

	// Number of parity symbols
	Npar int

	// Auto-select highest confidence entry
	Auto bool

	// Show what would be repaired without writing
	DryRun bool

	// Overwrite existing output
	Force bool

	// Verbose output
	Verbose bool

	// Show progress bar
	ShowProgress bool

	// Progress reporter for status updates
	Reporter *progress.Reporter
}
