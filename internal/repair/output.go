package repair

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"ctdbtools/internal/audio"
	"ctdbtools/internal/toc"
	"ctdbtools/internal/version"
)

// WriteOutputFiles generates all output files for a repair operation.
func WriteOutputFiles(outputDir string, result *RepairResult, layout toc.Layout, opts RepairOptions) (*OutputFiles, error) {
	files := &OutputFiles{}

	// Determine output format extension
	format := opts.Format
	if format == "" {
		format = audio.FormatWAV
	}
	ext := format.Extension()

	// Determine base name for output files (preserve original CUE filename if available)
	baseName := "album"
	if opts.OriginalCuePath != "" {
		cueBase := filepath.Base(opts.OriginalCuePath)
		cueExt := filepath.Ext(cueBase)
		baseName = cueBase[:len(cueBase)-len(cueExt)]
	}

	// Set file paths based on split-track vs single-file mode
	if opts.IsSplitTrack {
		// Split-track mode: derive filenames from original source files
		files.AudioPaths = make([]string, len(opts.SourceFiles))
		for i, sourcePath := range opts.SourceFiles {
			audioName := deriveOutputFilenameWithExt(sourcePath, i+1, ext)
			files.AudioPaths[i] = filepath.Join(outputDir, audioName)
		}
		// Set AudioPath to first file for convenience
		if len(files.AudioPaths) > 0 {
			files.AudioPath = files.AudioPaths[0]
		}
		// Backwards compatibility
		files.WAVPaths = files.AudioPaths
		files.WAVPath = files.AudioPath
	} else {
		// Single-file mode
		files.AudioPath = filepath.Join(outputDir, baseName+ext)
		files.AudioPaths = []string{files.AudioPath}
		// Backwards compatibility
		files.WAVPath = files.AudioPath
		files.WAVPaths = files.AudioPaths
	}

	files.CUEPath = filepath.Join(outputDir, baseName+".cue")
	files.LogPath = filepath.Join(outputDir, "ctdbtools_repair.log")

	// CUE sheet - try to transform original if available
	var cueErr error
	if opts.OriginalCuePath != "" {
		// Transform original CUE to preserve metadata
		var newFileRefs []string
		if opts.IsSplitTrack {
			// Use the new audio filenames (just base names for CUE)
			for _, p := range files.AudioPaths {
				newFileRefs = append(newFileRefs, filepath.Base(p))
			}
		} else {
			newFileRefs = []string{baseName + ext}
		}
		cueErr = transformCueSheet(opts.OriginalCuePath, files.CUEPath, newFileRefs, format)
		if cueErr != nil {
			fmt.Printf("Warning: failed to transform CUE sheet: %v, generating new one\n", cueErr)
		}
	}

	// Fall back to generating CUE if no original or transformation failed
	if opts.OriginalCuePath == "" || cueErr != nil {
		if opts.IsSplitTrack {
			// Generate split-track CUE
			var audioNames []string
			for _, p := range files.AudioPaths {
				audioNames = append(audioNames, filepath.Base(p))
			}
			if err := writeCueSheetSplitTrack(files.CUEPath, audioNames, layout, format); err != nil {
				return files, fmt.Errorf("failed to write CUE sheet: %w", err)
			}
		} else {
			if err := writeCueSheet(files.CUEPath, baseName+ext, layout, format); err != nil {
				return files, fmt.Errorf("failed to write CUE sheet: %w", err)
			}
		}
	}

	// Repair log
	if err := writeRepairLog(files.LogPath, result, layout); err != nil {
		return files, fmt.Errorf("failed to write repair log: %w", err)
	}

	return files, nil
}

// deriveOutputFilenameWithExt derives the output filename from original source path with specified extension.
func deriveOutputFilenameWithExt(originalPath string, trackNum int, ext string) string {
	if originalPath == "" {
		return fmt.Sprintf("%02d%s", trackNum, ext)
	}
	base := filepath.Base(originalPath)
	origExt := filepath.Ext(base)
	return strings.TrimSuffix(base, origExt) + ext
}

// transformCueSheet reads the original CUE file and transforms FILE references
// to point to the new audio files while preserving all other metadata.
func transformCueSheet(originalPath, outputPath string, newFileRefs []string, format audio.OutputFormat) error {
	inFile, err := os.Open(originalPath)
	if err != nil {
		return fmt.Errorf("failed to open original CUE: %w", err)
	}
	defer inFile.Close()

	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output CUE: %w", err)
	}
	defer outFile.Close()

	// Regex to match FILE directive: FILE "filename" WAVE (or BINARY, etc.)
	// Captures: prefix, filename (with quotes), type
	fileRegex := regexp.MustCompile(`^(\s*FILE\s+)"([^"]+)"(\s+\w+.*)$`)

	// Determine file type for CUE (WAVE or FLAC)
	fileType := cueFileType(format)

	scanner := bufio.NewScanner(inFile)
	writer := bufio.NewWriter(outFile)
	defer writer.Flush()

	fileIdx := 0
	for scanner.Scan() {
		line := scanner.Text()

		if matches := fileRegex.FindStringSubmatch(line); matches != nil {
			// This is a FILE directive - replace the filename and type
			if fileIdx < len(newFileRefs) {
				// Reconstruct with new filename and correct type
				newLine := fmt.Sprintf("%s\"%s\" %s", matches[1], newFileRefs[fileIdx], fileType)
				writer.WriteString(newLine + "\n")
				fileIdx++
			} else {
				// More FILE directives than new refs - keep original
				writer.WriteString(line + "\n")
			}
		} else {
			// Not a FILE directive - preserve as-is
			writer.WriteString(line + "\n")
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading CUE file: %w", err)
	}

	return nil
}

// writeCueSheetSplitTrack generates a CUE sheet for split-track output.
// Each track gets its own FILE directive.
func writeCueSheetSplitTrack(path string, audioFiles []string, layout toc.Layout, format audio.OutputFormat) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Determine file type for CUE
	fileType := cueFileType(format)
	ext := format.Extension()

	// Write CUE header
	fmt.Fprintf(f, "REM Generated by ctdbtools %s\n", version.Version)
	fmt.Fprintf(f, "REM Repaired audio files (split-track)\n")

	// Write track entries - each with its own FILE directive
	trackNum := 0
	for i, track := range layout.Tracks {
		if !track.IsAudio {
			continue
		}
		trackNum++

		if trackNum-1 < len(audioFiles) {
			fmt.Fprintf(f, "FILE \"%s\" %s\n", audioFiles[trackNum-1], fileType)
		} else {
			fmt.Fprintf(f, "FILE \"%02d%s\" %s\n", trackNum, ext, fileType)
		}

		fmt.Fprintf(f, "  TRACK %02d AUDIO\n", i+1)

		// For split-track, each file starts at 00:00:00
		// Pregap handling: if track has pregap, it's typically embedded at end of previous file
		if track.Pregap > 0 && trackNum > 1 {
			// Pregap embedded in previous track file - INDEX 00 not needed here
			fmt.Fprintf(f, "    INDEX 01 00:00:00\n")
		} else if track.Pregap > 0 && trackNum == 1 {
			// First track with pregap
			fmt.Fprintf(f, "    INDEX 00 00:00:00\n")
			fmt.Fprintf(f, "    INDEX 01 %s\n", framesToMSF(track.Pregap))
		} else {
			fmt.Fprintf(f, "    INDEX 01 00:00:00\n")
		}
	}

	return nil
}

// writeCueSheet generates a CUE sheet for the repaired audio.
func writeCueSheet(path, audioFileName string, layout toc.Layout, format audio.OutputFormat) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Determine file type for CUE
	fileType := cueFileType(format)

	// Write CUE header
	fmt.Fprintf(f, "REM Generated by ctdbtools %s\n", version.Version)
	fmt.Fprintf(f, "REM Repaired audio file\n")
	fmt.Fprintf(f, "FILE \"%s\" %s\n", audioFileName, fileType)

	// Write track entries
	for i, track := range layout.Tracks {
		if !track.IsAudio {
			continue
		}

		trackNum := i + 1
		fmt.Fprintf(f, "  TRACK %02d AUDIO\n", trackNum)

		// Write pregap if present (for track 1 or explicit pregaps)
		if track.Pregap > 0 && trackNum == 1 {
			fmt.Fprintf(f, "    INDEX 00 %s\n", framesToMSF(0))
			fmt.Fprintf(f, "    INDEX 01 %s\n", framesToMSF(track.Pregap))
		} else if track.Pregap > 0 {
			// Pregap embedded in previous track
			fmt.Fprintf(f, "    INDEX 00 %s\n", framesToMSF(track.Start-track.Pregap))
			fmt.Fprintf(f, "    INDEX 01 %s\n", framesToMSF(track.Start))
		} else {
			fmt.Fprintf(f, "    INDEX 01 %s\n", framesToMSF(track.Start))
		}
	}

	return nil
}

// cueFileType returns the CUE sheet file type keyword for the given format.
func cueFileType(format audio.OutputFormat) string {
	switch format {
	case audio.FormatFLAC:
		return "FLAC"
	default:
		return "WAVE"
	}
}

// writeRepairLog generates a detailed repair log.
func writeRepairLog(path string, result *RepairResult, layout toc.Layout) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Write header
	fmt.Fprintf(f, "ctdbtools Repair Log\n")
	fmt.Fprintf(f, "====================\n\n")
	fmt.Fprintf(f, "Date: %s\n", time.Now().Format("2006/01/02 15:04:05"))
	fmt.Fprintf(f, "Version: %s\n\n", version.Version)

	// CTDB Entry details
	if result.Entry != nil {
		fmt.Fprintf(f, "CTDB Entry\n")
		fmt.Fprintf(f, "----------\n")
		fmt.Fprintf(f, "Confidence: %d\n", result.Entry.Confidence)
		fmt.Fprintf(f, "CRC32:      %08X\n", result.Entry.CRC32)
		fmt.Fprintf(f, "Stride:     %d\n", result.Entry.Stride)
		fmt.Fprintf(f, "Npar:       %d\n", result.Entry.Npar)
		fmt.Fprintf(f, "\n")
	}

	// Detected offset
	fmt.Fprintf(f, "Detected Offset: %d samples\n\n", result.Offset)

	// Repair summary
	fmt.Fprintf(f, "Repair Summary\n")
	fmt.Fprintf(f, "--------------\n")
	fmt.Fprintf(f, "Total errors: %d\n", result.TotalErrors)
	fmt.Fprintf(f, "Can repair:   %v\n", result.CanRepair)
	fmt.Fprintf(f, "Success:      %v\n\n", result.Success)

	// Per-track results
	fmt.Fprintf(f, "Track Results\n")
	fmt.Fprintf(f, "-------------\n")
	fmt.Fprintf(f, "Track | Errors | Status     | Positions\n")
	fmt.Fprintf(f, "----- | ------ | ---------- | ---------\n")

	for _, tr := range result.TrackResults {
		status := "OK"
		if tr.ErrorCount > 0 {
			if tr.Repaired {
				status = "Repaired"
			} else {
				status = "Error"
			}
		}

		positions := "(none)"
		if tr.Positions != "" {
			positions = "@" + tr.Positions
		}

		fmt.Fprintf(f, " %2d   | %6d | %-10s | %s\n",
			tr.Track, tr.ErrorCount, status, positions)
	}

	fmt.Fprintf(f, "\n")

	// Error message if failed
	if result.ErrorMessage != "" {
		fmt.Fprintf(f, "\nError: %s\n", result.ErrorMessage)
	}

	return nil
}

// framesToMSF converts a frame number to MM:SS:FF format.
func framesToMSF(frames int) string {
	ff := frames % 75
	seconds := frames / 75
	ss := seconds % 60
	mm := seconds / 60
	return fmt.Sprintf("%02d:%02d:%02d", mm, ss, ff)
}
