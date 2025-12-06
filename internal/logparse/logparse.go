package logparse

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// TrackLogData contains CRC and peak data for a single track
type TrackLogData struct {
	CRC32       uint32  // CRC32 hash
	CRCWONULL   uint32  // CRC32 hash (skip zero)
	Peak        float64 // Peak amplitude (0.0 - 1.0)
	HasCRC32    bool
	HasCRCWONULL bool
}

// LogData contains parsed data from a ripping log file
type LogData struct {
	Tracks []TrackLogData // Per-track data (index 0 = disc-wide if available)
	Disc   TrackLogData   // Disc-wide data (All Tracks section)
}

// HasUsableData returns true if the log data contains any CRC information
func (d *LogData) HasUsableData() bool {
	if d == nil {
		return false
	}
	// Check if any track has CRC data
	for _, t := range d.Tracks {
		if t.HasCRC32 || t.HasCRCWONULL {
			return true
		}
	}
	return d.Disc.HasCRC32 || d.Disc.HasCRCWONULL
}

// ParseLogFile attempts to detect and parse a log file.
// Returns nil (not error) for unsupported formats or files without CRC data.
func ParseLogFile(logPath string) (*LogData, error) {
	content, err := os.ReadFile(logPath)
	if err != nil {
		return nil, nil // File read error - just skip log parsing
	}

	text := string(content)

	// Try to detect log type based on content
	// XLD logs are UTF-8
	if strings.Contains(text, "X Lossless Decoder") || strings.Contains(text, "XLD extraction logfile") {
		data, err := ParseXLDLog(text)
		if err != nil || !data.HasUsableData() {
			return nil, nil // No usable data - skip
		}
		return data, nil
	}

	// EAC logs are typically UTF-16LE - check for BOM or wide characters
	// For now, we don't support EAC logs just like CUETools not support XLD logs
	// if isEACLog(content) {
	//     return ParseEACLog(content)
	// }

	// Unknown or unsupported format - just skip (not an error)
	return nil, nil
}

// FindLogFile searches for a .log file in the given directory
func FindLogFile(dirPath string) string {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return ""
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(strings.ToLower(name), ".log") {
			return filepath.Join(dirPath, name)
		}
	}
	return ""
}

// Regex patterns for XLD log parsing
var (
	xldTrackPattern    = regexp.MustCompile(`^Track (\d+)`)
	xldCRC32Pattern    = regexp.MustCompile(`CRC32 hash\s*:\s*([0-9A-Fa-f]{8})`)
	xldCRCSkipPattern  = regexp.MustCompile(`CRC32 hash \(skip zero\)\s*:\s*([0-9A-Fa-f]{8})`)
	xldPeakPattern     = regexp.MustCompile(`Peak\s*:\s*(\d+\.\d+)`)
	xldAllTracksMarker = "All Tracks"
)

// ParseXLDLog parses X Lossless Decoder log content
func ParseXLDLog(content string) (*LogData, error) {
	data := &LogData{}
	scanner := bufio.NewScanner(strings.NewReader(content))

	var currentData *TrackLogData

	for scanner.Scan() {
		line := scanner.Text()

		// Check for All Tracks section (disc-wide)
		if strings.HasPrefix(strings.TrimSpace(line), xldAllTracksMarker) {
			currentData = &data.Disc
			continue
		}

		// Check for Track N section
		if matches := xldTrackPattern.FindStringSubmatch(strings.TrimSpace(line)); len(matches) > 1 {
			trackNum, _ := strconv.Atoi(matches[1])

			// Ensure tracks slice is large enough
			for len(data.Tracks) < trackNum {
				data.Tracks = append(data.Tracks, TrackLogData{})
			}
			currentData = &data.Tracks[trackNum-1]
			continue
		}

		// Parse data if we're in a track section
		if currentData != nil {
			// Parse CRC32 hash
			if matches := xldCRC32Pattern.FindStringSubmatch(line); len(matches) > 1 {
				val, _ := strconv.ParseUint(matches[1], 16, 32)
				currentData.CRC32 = uint32(val)
				currentData.HasCRC32 = true
			}

			// Parse CRC32 hash (skip zero)
			if matches := xldCRCSkipPattern.FindStringSubmatch(line); len(matches) > 1 {
				val, _ := strconv.ParseUint(matches[1], 16, 32)
				currentData.CRCWONULL = uint32(val)
				currentData.HasCRCWONULL = true
			}

			// Parse Peak
			if matches := xldPeakPattern.FindStringSubmatch(line); len(matches) > 1 {
				peak, _ := strconv.ParseFloat(matches[1], 64)
				currentData.Peak = peak
			}
		}
	}

	return data, nil
}
