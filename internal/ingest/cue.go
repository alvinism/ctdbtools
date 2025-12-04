package ingest

import (
	"fmt"
	"io/ioutil"
	"strings"

	"ctdbtool/internal/toc"
)

// ParseCueMinimal parses a minimal cuesheet-like track layout (supports TRACK/PREGAP/INDEX 01 start times).
// This is a simplified placeholder; for full coverage we should replace with a robust parser.
func ParseCueMinimal(lines []string) (toc.Layout, error) {
	var tracks []toc.Track
	var current toc.Track
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "TRACK") {
			if current.Length > 0 || current.Start > 0 {
				tracks = append(tracks, current)
			}
			current = toc.Track{IsAudio: true}
		} else if strings.HasPrefix(ln, "PREGAP") {
			var mm, ss, ff int
			fmt.Sscanf(ln, "PREGAP %02d:%02d:%02d", &mm, &ss, &ff)
			current.Pregap = mm*60*75 + ss*75 + ff
		} else if strings.HasPrefix(ln, "INDEX 01") {
			var mm, ss, ff int
			fmt.Sscanf(ln, "INDEX 01 %02d:%02d:%02d", &mm, &ss, &ff)
			current.Start = mm*60*75 + ss*75 + ff
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
		var end int
		if i+1 < len(tracks) {
			end = tracks[i+1].Start
		} else {
			end = tracks[i].Start + 75*60 // placeholder 60s
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
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return toc.Layout{}, err
	}
	lines := strings.Split(string(data), "\n")
	return ParseCueMinimal(lines)
}
