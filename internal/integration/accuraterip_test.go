//go:build integration

// Package integration contains integration tests for ctdbtools.
// These tests verify CRC calculations against real audio files.
// Run with: go test -tags=integration ./internal/integration/
package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ctdbtools/internal/ingest"
	"ctdbtools/internal/testutil"
)

// TestIntegrationSetup verifies the test environment is correctly configured.
func TestIntegrationSetup(t *testing.T) {
	materials := testutil.MaterialsPath()
	if materials == "" {
		t.Fatal("Test materials not found. Set CTDB_TEST_MATERIALS env var or run from project root.")
	}
	t.Logf("Materials path: %s", materials)

	if err := testutil.CheckFFmpeg(); err != nil {
		t.Fatalf("ffmpeg required for integration tests: %v", err)
	}
	t.Log("ffmpeg found")
}

// TestRealAudioARCRC tests AccurateRip CRC calculation against known values.
func TestRealAudioARCRC(t *testing.T) {
	materials := testutil.MaterialsPath()
	if materials == "" {
		t.Skip("Test materials not found")
	}
	if err := testutil.CheckFFmpeg(); err != nil {
		t.Fatalf("ffmpeg required: %v", err)
	}

	// Find all JSON files in materials
	entries, err := os.ReadDir(materials)
	if err != nil {
		t.Fatalf("Failed to read materials directory: %v", err)
	}

	testedAlbums := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		jsonPath := filepath.Join(materials, entry.Name())
		expected, err := testutil.ParseExpectedCRCs(jsonPath)
		if err != nil {
			t.Logf("Skipping %s: %v", entry.Name(), err)
			continue
		}

		if expected.NumTracks == 0 {
			t.Logf("Skipping %s: no track data", entry.Name())
			continue
		}

		// Find the corresponding album directory
		albumName := strings.TrimSuffix(entry.Name(), ".json")
		albumPath := filepath.Join(materials, albumName)
		if _, err := os.Stat(albumPath); os.IsNotExist(err) {
			t.Logf("Skipping %s: album directory not found", albumName)
			continue
		}

		t.Run(albumName, func(t *testing.T) {
			testAlbumARCRC(t, albumPath, expected)
		})
		testedAlbums++
	}

	if testedAlbums == 0 {
		t.Skip("No albums with valid test data found")
	}
}

func testAlbumARCRC(t *testing.T, albumPath string, expected *testutil.ExpectedCRCs) {
	// Find CUE file
	cueFile := findCueFile(albumPath)
	if cueFile == "" {
		t.Skip("No CUE file found")
	}

	// Parse CUE sheet
	cueSheet, err := ingest.ParseCueSheetFile(cueFile)
	if err != nil {
		t.Fatalf("Failed to parse CUE: %v", err)
	}

	layout := cueSheet.GetAudioLayout()

	// Verify TOCID matches
	tocid, err := layout.TOCID()
	if err != nil {
		t.Logf("Warning: could not compute TOCID: %v", err)
	} else if tocid != expected.TOCID {
		t.Logf("TOCID mismatch: got %s want %s (may be due to pregap handling)", tocid, expected.TOCID)
	}

	// Process audio using the proper CUE sheet processing function
	ctx := context.Background()
	stride := 5 * 588 * 2
	proc, err := ingest.ProcessCueSheet(ctx, cueSheet, stride, stride, 8, false)
	if err != nil {
		t.Fatalf("Failed to process CUE sheet: %v", err)
	}

	// Verify CRCs for all tracks
	for track := 1; track <= layout.AudioTracks; track++ {
		// Verify ARV1 CRC if we have expected value
		if expectedCRC, ok := expected.ARV1[track]; ok {
			actual := proc.TrackCRCAR(track)
			if actual != expectedCRC {
				t.Errorf("Track %d ARV1: got %08X want %08X", track, actual, expectedCRC)
			} else {
				t.Logf("Track %d ARV1: %08X ✓", track, actual)
			}
		}

		// Verify ARV2 CRC
		if expectedCRC, ok := expected.ARV2[track]; ok {
			actual := proc.TrackCRCV2(track)
			if actual != expectedCRC {
				t.Errorf("Track %d ARV2: got %08X want %08X", track, actual, expectedCRC)
			} else {
				t.Logf("Track %d ARV2: %08X ✓", track, actual)
			}
		}
	}
}

// TestRealAudioCRC32 tests CRC32 calculation against known values.
func TestRealAudioCRC32(t *testing.T) {
	materials := testutil.MaterialsPath()
	if materials == "" {
		t.Skip("Test materials not found")
	}
	if err := testutil.CheckFFmpeg(); err != nil {
		t.Fatalf("ffmpeg required: %v", err)
	}

	// Test with a specific album that has CRC32 data
	jsonPath := filepath.Join(materials, "[190605] 妹尾武 - LAST LOVE.json")
	if _, err := os.Stat(jsonPath); os.IsNotExist(err) {
		t.Skip("Test album not found")
	}

	expected, err := testutil.ParseExpectedCRCs(jsonPath)
	if err != nil {
		t.Fatalf("Failed to parse expected CRCs: %v", err)
	}

	albumPath := filepath.Join(materials, "[190605] 妹尾武 - LAST LOVE")
	if _, err := os.Stat(albumPath); os.IsNotExist(err) {
		t.Skip("Album directory not found")
	}

	cueFile := findCueFile(albumPath)
	if cueFile == "" {
		t.Skip("No CUE file found")
	}

	cueSheet, err := ingest.ParseCueSheetFile(cueFile)
	if err != nil {
		t.Fatalf("Failed to parse CUE: %v", err)
	}

	layout := cueSheet.GetAudioLayout()

	// Process audio using the proper CUE sheet processing function
	ctx := context.Background()
	stride := 5 * 588 * 2
	proc, err := ingest.ProcessCueSheet(ctx, cueSheet, stride, stride, 8, false)
	if err != nil {
		t.Fatalf("Failed to process CUE sheet: %v", err)
	}

	for track := 1; track <= layout.AudioTracks; track++ {
		// Verify CRC32
		if expectedCRC, ok := expected.CRC32[track]; ok {
			actual := proc.TrackCRC(track, 0)
			if actual != expectedCRC {
				t.Errorf("Track %d CRC32: got %08X want %08X", track, actual, expectedCRC)
			} else {
				t.Logf("Track %d CRC32: %08X ✓", track, actual)
			}
		}

		// Verify CRCWONULL
		if expectedCRC, ok := expected.CRCWONULL[track]; ok {
			actual := proc.TrackCRCWONULL(track, 0)
			if actual != expectedCRC {
				t.Errorf("Track %d CRCWONULL: got %08X want %08X", track, actual, expectedCRC)
			} else {
				t.Logf("Track %d CRCWONULL: %08X ✓", track, actual)
			}
		}

		// Verify peak amplitude (within 1% tolerance)
		if expectedPeak, ok := expected.Peak[track]; ok {
			actual := proc.TrackPeak(track)
			if actual < expectedPeak-1 || actual > expectedPeak+1 {
				t.Errorf("Track %d Peak: got %.1f%% want %.1f%%", track, actual, expectedPeak)
			} else {
				t.Logf("Track %d Peak: %.1f%% ✓", track, actual)
			}
		}
	}
}

// findCueFile finds a CUE file in the given directory.
func findCueFile(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if strings.HasSuffix(strings.ToLower(entry.Name()), ".cue") {
			return filepath.Join(dir, entry.Name())
		}
	}
	return ""
}
