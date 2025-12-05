package ingest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"ctdbtools/internal/toc"
)

// audioExtensions lists supported audio file extensions
var audioExtensions = map[string]bool{
	".flac": true,
	".wav":  true,
	".ape":  true,
	".wv":   true,
	".m4a":  true,
	".tta":  true,
}

// trackNumberRegex matches track numbers at the start of filenames
var trackNumberRegex = regexp.MustCompile(`^(\d+)`)

// DiscoverDirectory scans a directory for audio files and builds a CueSheet.
// If a CUE file is found in the directory, it uses that instead.
// Otherwise, it auto-discovers numbered audio files and builds a layout from their durations.
func DiscoverDirectory(ctx context.Context, dirPath string) (CueSheet, error) {
	// First, check if there's a CUE file in the directory
	// We use ReadDir instead of Glob because Glob has issues with special characters like [ and ]
	cueFile, err := findCueFile(dirPath)
	if err != nil {
		return CueSheet{}, fmt.Errorf("failed to search for CUE files: %w", err)
	}

	if cueFile != "" {
		return ParseCueSheetFile(cueFile)
	}

	// No CUE file found - auto-discover audio files
	return discoverAudioFiles(ctx, dirPath)
}

// findCueFile searches for a .cue file in the given directory
func findCueFile(dirPath string) (string, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(strings.ToLower(name), ".cue") {
			return filepath.Join(dirPath, name), nil
		}
	}
	return "", nil
}

// discoverAudioFiles scans for audio files and builds a CueSheet from them
func discoverAudioFiles(ctx context.Context, dirPath string) (CueSheet, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return CueSheet{}, fmt.Errorf("failed to read directory: %w", err)
	}

	type audioFile struct {
		path    string
		name    string
		trackNo int
	}

	var audioFiles []audioFile

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if !audioExtensions[ext] {
			continue
		}

		// Extract track number from filename
		trackNo := 0
		if matches := trackNumberRegex.FindStringSubmatch(name); len(matches) > 1 {
			fmt.Sscanf(matches[1], "%d", &trackNo)
		}

		audioFiles = append(audioFiles, audioFile{
			path:    filepath.Join(dirPath, name),
			name:    name,
			trackNo: trackNo,
		})
	}

	if len(audioFiles) == 0 {
		return CueSheet{}, fmt.Errorf("no audio files found in directory: %s", dirPath)
	}

	// Sort by track number, then by filename
	sort.Slice(audioFiles, func(i, j int) bool {
		if audioFiles[i].trackNo != audioFiles[j].trackNo {
			return audioFiles[i].trackNo < audioFiles[j].trackNo
		}
		return audioFiles[i].name < audioFiles[j].name
	})

	// Probe durations and build layout
	var sources []SourceSegment
	var tracks []toc.Track
	currentFrame := 0

	for _, af := range audioFiles {
		frames, err := ProbeDurationFrames(ctx, af.path)
		if err != nil {
			return CueSheet{}, fmt.Errorf("failed to probe %s: %w", af.name, err)
		}

		// Add source segment (each track is a separate file)
		// Use just the filename, CueDir will be joined later
		sources = append(sources, SourceSegment{
			FilePath: af.name,
			Offset:   0,
			Length:   0, // 0 means entire file
		})

		// Add track to layout
		tracks = append(tracks, toc.Track{
			Start:   currentFrame,
			Length:  frames,
			Pregap:  0, // No pregap info without CUE
			IsAudio: true,
		})

		currentFrame += frames
	}

	layout := toc.Layout{
		FirstAudio:  1,
		Leadout:     currentFrame,
		AudioTracks: len(tracks),
		Tracks:      tracks,
	}

	return CueSheet{
		Layout:      layout,
		CueDir:      dirPath,
		Sources:     sources,
		AudioLayout: layout, // Same as Layout for auto-discovered files
	}, nil
}
