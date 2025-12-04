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

// ParseCueMinimal parses TRACK/PREGAP/INDEX 01 start times into a toc.Layout.
func ParseCueMinimal(lines []string) (toc.Layout, error) {
	var tracks []toc.Track
	var current toc.Track
	var leadoutFrames int
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "TRACK") {
			if current.Length > 0 || current.Start > 0 {
				tracks = append(tracks, current)
			}
			current = toc.Track{IsAudio: true}
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
	if current.Length > 0 || current.Start > 0 {
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

// ParseCueFileMinimal reads a cuesheet file and parses it using ParseCueMinimal.
func ParseCueFileMinimal(path string) (toc.Layout, error) {
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
	return ParseCueMinimal(lines)
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
