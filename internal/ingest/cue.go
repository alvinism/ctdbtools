package ingest

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"ctdbtool/internal/toc"
)

var timeRe = regexp.MustCompile(`(\d+):(\d+):(\d+)`)

// ParseCue parses TRACK/PREGAP/INDEX 01 start times into a toc.Layout.
func ParseCue(lines []string) (toc.Layout, error) {
	var tracks []toc.Track
	var current toc.Track
	var hasCurrentTrack bool
	var leadoutFrames int
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "TRACK") {
			if hasCurrentTrack {
				tracks = append(tracks, current)
			}
			current = toc.Track{IsAudio: true}
			hasCurrentTrack = true
		} else if strings.HasPrefix(ln, "REM LEAD-OUT") {
			if f, ok := parseFrames(ln[len("REM LEAD-OUT"):]); ok {
				leadoutFrames = f
			}
		} else if strings.HasPrefix(ln, "PREGAP") {
			if f, ok := parseFrames(strings.TrimPrefix(ln, "PREGAP")); ok {
				current.Pregap = f
			}
		} else if strings.HasPrefix(ln, "INDEX 01") {
			if f, ok := parseFrames(strings.TrimPrefix(ln, "INDEX 01")); ok {
				current.Start = f
			}
		}
	}
	if hasCurrentTrack {
		tracks = append(tracks, current)
	}
	if len(tracks) == 0 {
		return toc.Layout{}, fmt.Errorf("no tracks parsed")
	}
	// Derive lengths
	for i := 0; i < len(tracks); i++ {
		end := leadoutFrames
		if i+1 < len(tracks) {
			end = tracks[i+1].Start
		} else if end == 0 {
			end = tracks[i].Start + 75*60 // fallback to 60s if no leadout found
		}
		if end < tracks[i].Start {
			end = tracks[i].Start
		}
		tracks[i].Length = end - tracks[i].Start
	}
	return toc.Layout{
		FirstAudio:  1,
		AudioTracks: len(tracks),
		Leadout:     tracks[len(tracks)-1].End(),
		Tracks:      tracks,
	}, nil
}

// ParseCueFile reads a cuesheet file and parses it using ParseCue.
func ParseCueFile(path string) (toc.Layout, error) {
	return ParseCueFileWithLeadout(path, 0)
}

// ParseCueFileWithLeadout reads a cuesheet file with an explicit leadout frame count.
// If leadout is 0, it falls back to 60 seconds after the last track start.
func ParseCueFileWithLeadout(path string, leadout int) (toc.Layout, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return toc.Layout{}, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		return toc.Layout{}, err
	}
	return ParseCueWithLeadout(lines, leadout)
}

// ParseCueWithLeadout parses CUE with an explicit leadout frame count.
func ParseCueWithLeadout(lines []string, overrideLeadout int) (toc.Layout, error) {
	layout, err := ParseCue(lines)
	if err != nil {
		return layout, err
	}
	if overrideLeadout > 0 && len(layout.Tracks) > 0 {
		// Recalculate last track length with actual leadout
		lastIdx := len(layout.Tracks) - 1
		layout.Tracks[lastIdx].Length = overrideLeadout - layout.Tracks[lastIdx].Start
		layout.Leadout = overrideLeadout
	}
	return layout, nil
}

func parseFrames(s string) (int, bool) {
	m := timeRe.FindStringSubmatch(s)
	if len(m) != 4 {
		return 0, false
	}
	mm, _ := strconv.Atoi(m[1])
	ss, _ := strconv.Atoi(m[2])
	ff, _ := strconv.Atoi(m[3])
	return mm*60*75 + ss*75 + ff, true
}

// fileRe matches FILE "filename" TYPE
var fileRe = regexp.MustCompile(`^FILE\s+"([^"]+)"\s+(\w+)`)

// trackRe matches TRACK nn AUDIO/DATA
var trackRe = regexp.MustCompile(`^TRACK\s+(\d+)\s+(\w+)`)

// indexRe matches INDEX nn mm:ss:ff
var indexRe = regexp.MustCompile(`^INDEX\s+(\d+)\s+(\d+:\d+:\d+)`)

// trackFileInfo holds parsing state for a track
type trackFileInfo struct {
	track          toc.Track
	filePath       string // audio file for this track
	fileRelStart   int    // INDEX 01 position relative to file start (frames)
	fileRelIndex00 int    // INDEX 00 position relative to file (marks track end in split mode)
}

// ParseCueSheet parses a CUE sheet with FILE directive support for split tracks.
// Returns a CueSheet with layout and source segment mapping.
// cueDir is the directory containing the CUE file (for resolving relative paths).
func ParseCueSheet(lines []string, cueDir string) (CueSheet, error) {
	var trackInfos []trackFileInfo
	var currentInfo *trackFileInfo
	var currentFile string
	var leadoutFrames int

	for _, ln := range lines {
		ln = strings.TrimSpace(ln)

		// Handle FILE directive
		if m := fileRe.FindStringSubmatch(ln); m != nil {
			currentFile = m[1]
			continue
		}

		// Handle TRACK directive
		if m := trackRe.FindStringSubmatch(ln); m != nil {
			trackType := m[2]

			// Save previous track info if any
			if currentInfo != nil {
				trackInfos = append(trackInfos, *currentInfo)
			}

			currentInfo = &trackFileInfo{
				track: toc.Track{
					IsAudio: strings.EqualFold(trackType, "AUDIO"),
				},
				filePath: currentFile,
			}
			continue
		}

		// Handle INDEX directive
		if m := indexRe.FindStringSubmatch(ln); m != nil {
			indexNum, _ := strconv.Atoi(m[1])
			frames, ok := parseFrames(m[2])
			if !ok {
				continue
			}

			if currentInfo == nil {
				continue
			}

			// Update file path in case FILE appeared between TRACK and INDEX
			if currentFile != "" && currentInfo.filePath != currentFile {
				currentInfo.filePath = currentFile
			}

			if indexNum == 0 {
				// INDEX 00 marks pregap start (relative to current file)
				currentInfo.fileRelIndex00 = frames
			} else if indexNum == 1 {
				// INDEX 01 marks track audio start (relative to current file)
				currentInfo.fileRelStart = frames
			}
			continue
		}

		// Handle PREGAP directive (virtual pregap, not in file)
		if strings.HasPrefix(ln, "PREGAP") {
			if f, ok := parseFrames(strings.TrimPrefix(ln, "PREGAP")); ok {
				if currentInfo != nil {
					currentInfo.track.Pregap = f
				}
			}
			continue
		}

		// Handle REM LEAD-OUT
		if strings.HasPrefix(ln, "REM LEAD-OUT") {
			if f, ok := parseFrames(ln[len("REM LEAD-OUT"):]); ok {
				leadoutFrames = f
			}
			continue
		}
	}

	// Save last track
	if currentInfo != nil {
		trackInfos = append(trackInfos, *currentInfo)
	}

	if len(trackInfos) == 0 {
		return CueSheet{}, fmt.Errorf("no tracks parsed")
	}

	// Determine if this is a split-track CUE (multiple files)
	isSplit := false
	if len(trackInfos) > 1 {
		firstFile := trackInfos[0].filePath
		for _, ti := range trackInfos[1:] {
			if ti.filePath != firstFile {
				isSplit = true
				break
			}
		}
	}

	// Build sources and calculate absolute track positions
	var sources []SourceSegment
	var tracks []toc.Track

	if isSplit {
		// Split track mode: each track has its own file
		// INDEX 00 of next track (in previous file) marks end of previous track
		absoluteFrame := 0

		for i, ti := range trackInfos {
			// For split tracks, INDEX 01 is typically 00:00:00 (start of file)
			// The track's absolute start is the cumulative position
			track := ti.track
			track.Start = absoluteFrame

			// Pregap handling: INDEX 00 of THIS track was in the PREVIOUS file
			// For the first track, there's no pregap from previous file
			// The pregap value represents frames before INDEX 01 in the file
			// which don't contribute to this track's CRC

			// Determine track length:
			// For split tracks, look at the NEXT track's INDEX 00 value
			// which tells us where this track ends in this file
			var trackLengthFrames int
			if i+1 < len(trackInfos) {
				nextInfo := trackInfos[i+1]
				if nextInfo.fileRelIndex00 > 0 {
					// Next track's INDEX 00 is in THIS track's file
					// Track length = INDEX 00 position (from file start)
					trackLengthFrames = nextInfo.fileRelIndex00
				} else {
					// No INDEX 00 - need to probe file duration
					trackLengthFrames = 75 * 60 // placeholder, will be overridden
				}
			} else {
				// Last track - use leadout if available, else placeholder
				if leadoutFrames > 0 && leadoutFrames > absoluteFrame {
					trackLengthFrames = leadoutFrames - absoluteFrame
				} else {
					trackLengthFrames = 75 * 60 // placeholder
				}
			}
			track.Length = trackLengthFrames

			// Create source segment for this track
			src := SourceSegment{
				FilePath: ti.filePath,
				Offset:   int64(ti.fileRelStart) * 588, // usually 0
				Length:   int64(trackLengthFrames) * 588,
			}
			sources = append(sources, src)
			tracks = append(tracks, track)

			absoluteFrame += trackLengthFrames
		}
	} else {
		// Single file mode: all tracks in one file with absolute positions
		// Parse as before, using INDEX 01 times as absolute positions
		for _, ti := range trackInfos {
			track := ti.track
			track.Start = ti.fileRelStart
			tracks = append(tracks, track)
		}

		// Derive lengths from track starts
		for i := 0; i < len(tracks); i++ {
			var end int
			if i+1 < len(tracks) {
				end = tracks[i+1].Start
			} else if leadoutFrames > 0 {
				end = leadoutFrames
			} else {
				end = tracks[i].Start + 75*60 // fallback
			}
			if end < tracks[i].Start {
				end = tracks[i].Start
			}
			tracks[i].Length = end - tracks[i].Start
		}

		// Single source covering all tracks
		sources = append(sources, SourceSegment{
			FilePath: trackInfos[0].filePath,
			Offset:   0,
			Length:   0, // 0 means use whole file
		})
	}

	// Calculate leadout
	var finalLeadout int
	if len(tracks) > 0 {
		lastTrack := tracks[len(tracks)-1]
		finalLeadout = lastTrack.Start + lastTrack.Length
	}

	layout := toc.Layout{
		FirstAudio:  1,
		AudioTracks: len(tracks),
		Leadout:     finalLeadout,
		Tracks:      tracks,
	}

	return CueSheet{
		Layout:  layout,
		CueDir:  cueDir,
		Sources: sources,
	}, nil
}

// ParseCueSheetFile reads a CUE file and parses it with split track support.
func ParseCueSheetFile(path string) (CueSheet, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return CueSheet{}, err
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		return CueSheet{}, err
	}

	cueDir := filepath.Dir(path)
	return ParseCueSheet(lines, cueDir)
}
