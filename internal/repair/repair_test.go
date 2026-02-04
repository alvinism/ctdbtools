package repair

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ctdbtools/internal/audio"
	"ctdbtools/internal/parity"
	"ctdbtools/internal/toc"
)

// TestSortCorrections verifies corrections are sorted by position.
func TestSortCorrections(t *testing.T) {
	corrections := []parity.ErrorCorrection{
		{Position: 100, Magnitude: 0x1234},
		{Position: 50, Magnitude: 0x5678},
		{Position: 200, Magnitude: 0x9ABC},
		{Position: 75, Magnitude: 0xDEF0},
	}

	sortCorrections(corrections)

	expectedOrder := []int{50, 75, 100, 200}
	for i, c := range corrections {
		if c.Position != expectedOrder[i] {
			t.Errorf("Position[%d] = %d, want %d", i, c.Position, expectedOrder[i])
		}
	}
}

// TestGetTrackSampleRange verifies track sample range calculation.
func TestGetTrackSampleRange(t *testing.T) {
	layout := toc.Layout{
		FirstAudio:  1,
		AudioTracks: 3,
		Tracks: []toc.Track{
			{Start: 0, Length: 1000, Pregap: 0, IsAudio: true},
			{Start: 1000, Length: 2000, Pregap: 0, IsAudio: true},
			{Start: 3000, Length: 1500, Pregap: 0, IsAudio: true},
		},
		Leadout: 4500,
	}

	tests := []struct {
		track    int
		wantMin  int
		wantMax  int
	}{
		{1, 0, 1000 * 588 * 2},
		{2, 1000 * 588 * 2, 3000 * 588 * 2},
		{3, 3000 * 588 * 2, 4500 * 588 * 2},
	}

	for _, tt := range tests {
		min, max := GetTrackSampleRange(layout, tt.track)
		if min != tt.wantMin {
			t.Errorf("Track %d: min = %d, want %d", tt.track, min, tt.wantMin)
		}
		if max != tt.wantMax {
			t.Errorf("Track %d: max = %d, want %d", tt.track, max, tt.wantMax)
		}
	}
}

// TestCalculateTrackResults verifies per-track error counting.
func TestCalculateTrackResults(t *testing.T) {
	layout := toc.Layout{
		FirstAudio:  1,
		AudioTracks: 3,
		Tracks: []toc.Track{
			{Start: 0, Length: 1000, Pregap: 0, IsAudio: true},
			{Start: 1000, Length: 1000, Pregap: 0, IsAudio: true},
			{Start: 2000, Length: 1000, Pregap: 0, IsAudio: true},
		},
		Leadout: 3000,
	}

	// Create corrections in different tracks
	// Track 1: 0 to 1000*588*2 = 0 to 1176000
	// Track 2: 1176000 to 2352000
	// Track 3: 2352000 to 3528000
	corrections := []parity.ErrorCorrection{
		{Position: 100, Magnitude: 0x1111},        // Track 1
		{Position: 500000, Magnitude: 0x2222},     // Track 1
		{Position: 1200000, Magnitude: 0x3333},    // Track 2
		{Position: 2400000, Magnitude: 0x4444},    // Track 3
		{Position: 2500000, Magnitude: 0x5555},    // Track 3
	}

	results := calculateTrackResults(layout, corrections, 11760, 11760, 3000*588, 0, 0)

	if len(results) != 3 {
		t.Fatalf("len(results) = %d, want 3", len(results))
	}

	// Track 1 should have 2 errors
	if results[0].ErrorCount != 2 {
		t.Errorf("Track 1 ErrorCount = %d, want 2", results[0].ErrorCount)
	}

	// Track 2 should have 1 error
	if results[1].ErrorCount != 1 {
		t.Errorf("Track 2 ErrorCount = %d, want 1", results[1].ErrorCount)
	}

	// Track 3 should have 2 errors
	if results[2].ErrorCount != 2 {
		t.Errorf("Track 3 ErrorCount = %d, want 2", results[2].ErrorCount)
	}
}

// TestRepairResultStructure verifies RepairResult fields.
func TestRepairResultStructure(t *testing.T) {
	result := RepairResult{
		InputPath:   "/path/to/input.cue",
		OutputDir:   "/path/to/output",
		TotalErrors: 5,
		Success:     true,
		CanRepair:   true,
	}

	if result.InputPath != "/path/to/input.cue" {
		t.Errorf("InputPath = %s, want /path/to/input.cue", result.InputPath)
	}

	if result.TotalErrors != 5 {
		t.Errorf("TotalErrors = %d, want 5", result.TotalErrors)
	}

	if !result.Success {
		t.Error("Success = false, want true")
	}

	if !result.CanRepair {
		t.Error("CanRepair = false, want true")
	}
}

// TestTrackRepairResult verifies TrackRepairResult fields.
func TestTrackRepairResult(t *testing.T) {
	tr := TrackRepairResult{
		Track:      5,
		ErrorCount: 10,
		Positions:  "01:23:45-01:23:50",
		Repaired:   true,
	}

	if tr.Track != 5 {
		t.Errorf("Track = %d, want 5", tr.Track)
	}

	if tr.ErrorCount != 10 {
		t.Errorf("ErrorCount = %d, want 10", tr.ErrorCount)
	}

	if !tr.Repaired {
		t.Error("Repaired = false, want true")
	}
}

// TestRepairCandidate verifies RepairCandidate fields.
func TestRepairCandidate(t *testing.T) {
	candidate := RepairCandidate{
		Index:          2,
		Offset:         667,
		ErrorCount:     100,
		ErrorPositions: "02:00:00-02:00:10",
		CanRepair:      true,
		TrackErrors:    []int{10, 20, 30, 40},
	}

	if candidate.Index != 2 {
		t.Errorf("Index = %d, want 2", candidate.Index)
	}

	if candidate.Offset != 667 {
		t.Errorf("Offset = %d, want 667", candidate.Offset)
	}

	if !candidate.CanRepair {
		t.Error("CanRepair = false, want true")
	}

	if len(candidate.TrackErrors) != 4 {
		t.Errorf("len(TrackErrors) = %d, want 4", len(candidate.TrackErrors))
	}
}

// TestRepairOptions verifies RepairOptions fields.
func TestRepairOptions(t *testing.T) {
	opts := RepairOptions{
		InputPath:       "/path/to/album.cue",
		OutputDir:       "/path/to/output",
		Stride:          11760,
		Npar:            8,
		Auto:            true,
		DryRun:          false,
		Force:           true,
		Verbose:         true,
		ShowProgress:    true,
		OriginalCuePath: "/path/to/original.cue",
		IsSplitTrack:    true,
		SourceFiles:     []string{"01.flac", "02.flac"},
	}

	if opts.Stride != 11760 {
		t.Errorf("Stride = %d, want 11760", opts.Stride)
	}

	if opts.Npar != 8 {
		t.Errorf("Npar = %d, want 8", opts.Npar)
	}

	if !opts.Auto {
		t.Error("Auto = false, want true")
	}

	if opts.DryRun {
		t.Error("DryRun = true, want false")
	}

	if opts.OriginalCuePath != "/path/to/original.cue" {
		t.Errorf("OriginalCuePath = %s, want /path/to/original.cue", opts.OriginalCuePath)
	}

	if !opts.IsSplitTrack {
		t.Error("IsSplitTrack = false, want true")
	}

	if len(opts.SourceFiles) != 2 {
		t.Errorf("len(SourceFiles) = %d, want 2", len(opts.SourceFiles))
	}
}

// TestDeriveOutputFilename verifies output filename derivation.
func TestDeriveOutputFilename(t *testing.T) {
	tests := []struct {
		original string
		trackNum int
		want     string
	}{
		{"01 - Song Title.flac", 1, "01 - Song Title.wav"},
		{"Track01.ape", 1, "Track01.wav"},
		{"/path/to/02 - Another Song.m4a", 2, "02 - Another Song.wav"},
		{"", 3, "03.wav"},
		{"song.FLAC", 1, "song.wav"},
		{"01.wv", 1, "01.wav"},
	}

	for _, tt := range tests {
		got := deriveOutputFilename(tt.original, tt.trackNum)
		if got != tt.want {
			t.Errorf("deriveOutputFilename(%q, %d) = %q, want %q", tt.original, tt.trackNum, got, tt.want)
		}
	}
}

// TestTransformCueSheet verifies CUE sheet transformation preserves metadata.
func TestTransformCueSheet(t *testing.T) {
	// Create temp directory for test files
	tmpDir, err := os.MkdirTemp("", "cue-transform-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create original CUE content with rich metadata
	originalCue := `TITLE "Test Album"
PERFORMER "Test Artist"
CATALOG 1234567890123
REM GENRE "Rock"
REM DATE "2024"
REM DISCID AB123456
FILE "01 - First Track.flac" WAVE
  TRACK 01 AUDIO
    TITLE "First Track"
    PERFORMER "Artist 1"
    ISRC USTEST000001
    INDEX 01 00:00:00
FILE "02 - Second Track.flac" WAVE
  TRACK 02 AUDIO
    TITLE "Second Track"
    PERFORMER "Artist 2"
    ISRC USTEST000002
    INDEX 01 00:00:00
FILE "03 - Third Track.flac" WAVE
  TRACK 03 AUDIO
    TITLE "Third Track"
    PERFORMER "Artist 3"
    ISRC USTEST000003
    INDEX 01 00:00:00
`

	originalPath := filepath.Join(tmpDir, "original.cue")
	outputPath := filepath.Join(tmpDir, "output.cue")

	if err := os.WriteFile(originalPath, []byte(originalCue), 0644); err != nil {
		t.Fatalf("Failed to write original CUE: %v", err)
	}

	// Transform CUE
	newRefs := []string{
		"01 - First Track.wav",
		"02 - Second Track.wav",
		"03 - Third Track.wav",
	}

	if err := transformCueSheet(originalPath, outputPath, newRefs, audio.FormatWAV); err != nil {
		t.Fatalf("transformCueSheet failed: %v", err)
	}

	// Read transformed CUE
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read output CUE: %v", err)
	}

	transformed := string(content)

	// Verify metadata is preserved
	metadataChecks := []string{
		`TITLE "Test Album"`,
		`PERFORMER "Test Artist"`,
		`CATALOG 1234567890123`,
		`REM GENRE "Rock"`,
		`REM DATE "2024"`,
		`REM DISCID AB123456`,
		`TITLE "First Track"`,
		`PERFORMER "Artist 1"`,
		`ISRC USTEST000001`,
		`TITLE "Second Track"`,
		`PERFORMER "Artist 2"`,
		`ISRC USTEST000002`,
		`TITLE "Third Track"`,
		`PERFORMER "Artist 3"`,
		`ISRC USTEST000003`,
	}

	for _, check := range metadataChecks {
		if !strings.Contains(transformed, check) {
			t.Errorf("Transformed CUE missing metadata: %s", check)
		}
	}

	// Verify FILE references are updated
	fileChecks := []string{
		`FILE "01 - First Track.wav" WAVE`,
		`FILE "02 - Second Track.wav" WAVE`,
		`FILE "03 - Third Track.wav" WAVE`,
	}

	for _, check := range fileChecks {
		if !strings.Contains(transformed, check) {
			t.Errorf("Transformed CUE missing file reference: %s", check)
		}
	}

	// Verify old file references are NOT present
	oldFileChecks := []string{
		`"01 - First Track.flac"`,
		`"02 - Second Track.flac"`,
		`"03 - Third Track.flac"`,
	}

	for _, check := range oldFileChecks {
		if strings.Contains(transformed, check) {
			t.Errorf("Transformed CUE still contains old file reference: %s", check)
		}
	}
}

// TestOutputFilesWAVPaths verifies WAVPaths field in OutputFiles.
func TestOutputFilesWAVPaths(t *testing.T) {
	files := OutputFiles{
		WAVPath:  "/path/to/album.wav",
		WAVPaths: []string{"/path/to/01.wav", "/path/to/02.wav", "/path/to/03.wav"},
		CUEPath:  "/path/to/album.cue",
		LogPath:  "/path/to/album.log",
	}

	if len(files.WAVPaths) != 3 {
		t.Errorf("len(WAVPaths) = %d, want 3", len(files.WAVPaths))
	}

	if files.WAVPaths[0] != "/path/to/01.wav" {
		t.Errorf("WAVPaths[0] = %s, want /path/to/01.wav", files.WAVPaths[0])
	}
}
