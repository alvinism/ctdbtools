package toc

import (
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// Layout models a CD table of contents.
// Frames are CD sectors at 75 Hz.
type Layout struct {
	FirstAudio  int     // 1-based index of first audio track
	AudioTracks int     // number of audio tracks
	Leadout     int     // frame of start of leadout (disc length)
	Tracks      []Track // ordered track list (1-based when referenced externally)
}

// Track represents a single track within a TOC.
type Track struct {
	Start   int  // start offset in frames
	Length  int  // length in frames
	Pregap  int  // pregap frames preceding this track
	IsAudio bool // true if this track is audio
}

// End returns inclusive end frame.
func (t Track) End() int {
	if t.Length == 0 {
		return t.Start
	}
	return t.Start + t.Length - 1
}

// AudioLengthFrames returns total audio length in frames across all audio tracks.
func (l Layout) AudioLengthFrames() int {
	if l.AudioTracks == 0 || l.FirstAudio < 1 || l.FirstAudio+l.AudioTracks-1 > len(l.Tracks) {
		return 0
	}
	first := l.Tracks[l.FirstAudio-1].Start
	last := l.Tracks[l.FirstAudio+l.AudioTracks-2].End()
	return last + 1 - first
}

// TrackLengthFrames returns length in frames for a 1-based audio track number.
func (l Layout) TrackLengthFrames(track int) int {
	idx := track + l.FirstAudio - 2
	if idx < 0 || idx >= len(l.Tracks) {
		return 0
	}
	return l.Tracks[idx].Length
}

// TrackCount returns the number of tracks in the layout.
func (l Layout) TrackCount() int {
	return len(l.Tracks)
}

// TrackStartFrame returns the start frame for a 1-based audio track number.
func (l Layout) TrackStartFrame(track int) int {
	idx := track + l.FirstAudio - 2
	if idx < 0 || idx >= len(l.Tracks) {
		return 0
	}
	return l.Tracks[idx].Start
}

// TOCID computes the CUETools TOCID string for the layout.
// Mirrors CUETools.CDImage/CDImage.cs property TOCID.
func (l Layout) TOCID() (string, error) {
	if l.FirstAudio < 1 || l.FirstAudio > len(l.Tracks) {
		return "", fmt.Errorf("invalid FirstAudio index")
	}
	if l.AudioTracks <= 0 || l.FirstAudio+l.AudioTracks-1 > len(l.Tracks) {
		return "", fmt.Errorf("invalid AudioTracks count")
	}

	start0 := l.Tracks[l.FirstAudio-1].Start
	var b strings.Builder
	for i := 1; i < l.AudioTracks; i++ {
		b.WriteString(fmt.Sprintf("%08X", l.Tracks[l.FirstAudio-1+i].Start-start0))
	}
	last := l.Tracks[l.FirstAudio-1+l.AudioTracks-1]
	b.WriteString(fmt.Sprintf("%08X", last.End()+1-start0))

	if l.AudioTracks < 100 {
		b.WriteString(strings.Repeat("0", (100-l.AudioTracks)*8))
	}

	sum := sha1.Sum([]byte(b.String()))
	enc := base64.StdEncoding.EncodeToString(sum[:])
	enc = strings.ReplaceAll(enc, "+", ".")
	enc = strings.ReplaceAll(enc, "/", "_")
	enc = strings.ReplaceAll(enc, "=", "-")
	return enc, nil
}

// CDDBID replicates AccurateRip CDDB disc id calculation from CUETools.AccurateRip.AccurateRip.cs.
func (l Layout) CDDBID() (string, error) {
	if len(l.Tracks) == 0 {
		return "", fmt.Errorf("no tracks")
	}
	if l.Leadout <= 0 {
		return "", fmt.Errorf("invalid leadout")
	}
	var cddb uint32
	for i := 0; i < len(l.Tracks); i++ {
		cddb += sumDigits(uint32(l.Tracks[i].Start/75 + 2))
	}
	id := (((cddb % 255) << 24) + (uint32(l.Leadout/75-l.Tracks[0].Start/75) << 8) + uint32(len(l.Tracks))) & 0xffffffff
	return fmt.Sprintf("%08X", id), nil
}

// TOCString returns the TOC string for CTDB queries.
// Format: "{-}{start}:{-}{start}:...:{leadout}" where "-" prefix indicates non-audio.
// Mirrors CUETools.CDImage/CDImage.cs ToString().
func (l Layout) TOCString() string {
	var b strings.Builder
	for _, tr := range l.Tracks {
		if !tr.IsAudio {
			b.WriteString("-")
		}
		b.WriteString(fmt.Sprintf("%d:", tr.Start))
	}
	b.WriteString(fmt.Sprintf("%d", l.Leadout))
	return b.String()
}

// AccurateRipID builds the AccurateRip disc id triplet.
// Format: discId1-discId2-cddbId (lowercase) matching CUETools.
func (l Layout) AccurateRipID() (string, error) {
	cddb, err := l.CDDBID()
	if err != nil {
		return "", err
	}
	var disc1, disc2, num uint32
	for _, tr := range l.Tracks {
		if !tr.IsAudio {
			continue
		}
		num++
		disc1 += uint32(tr.Start)
		disc2 += uint32(maxInt(tr.Start, 1)) * num
	}
	num++
	disc1 += uint32(l.Leadout)
	disc2 += uint32(maxInt(l.Leadout, 1)) * num
	disc1 &= 0xffffffff
	disc2 &= 0xffffffff
	return fmt.Sprintf("%08x-%08x-%s", disc1, disc2, strings.ToLower(cddb)), nil
}

func sumDigits(n uint32) uint32 {
	var r uint32
	for n > 0 {
		r += n % 10
		n /= 10
	}
	return r
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ParseTOCString parses a CTDB TOC string into track start frames.
// TOC format: "{-}{start}:{-}{start}:...:{leadout}" where "-" prefix indicates non-audio.
func ParseTOCString(tocStr string) ([]int, error) {
	if tocStr == "" {
		return nil, fmt.Errorf("empty TOC string")
	}
	parts := strings.Split(tocStr, ":")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid TOC string: too few parts")
	}
	frames := make([]int, len(parts))
	for i, p := range parts {
		// Handle "-" prefix for non-audio tracks
		p = strings.TrimPrefix(p, "-")
		val, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid frame value at position %d: %w", i, err)
		}
		frames[i] = val
	}
	return frames, nil
}

// DetectPregapFromTOC compares two TOC strings and returns the pregap difference.
// Returns the difference in first track position (ctdb[0] - local[0]).
// A positive result means CTDB has a pregap that our local TOC is missing.
func DetectPregapFromTOC(localTOC, ctdbTOC string) int {
	local, err := ParseTOCString(localTOC)
	if err != nil || len(local) == 0 {
		return 0
	}
	ctdb, err := ParseTOCString(ctdbTOC)
	if err != nil || len(ctdb) == 0 {
		return 0
	}
	// Pregap = difference in first track position
	return ctdb[0] - local[0]
}

// ApplyPregap applies a pregap correction to a layout.
// This adjusts all track start positions and the leadout by the pregap amount.
// The pregap is recorded in the first track's Pregap field.
func (l *Layout) ApplyPregap(pregap int) {
	if pregap <= 0 || len(l.Tracks) == 0 {
		return
	}
	for i := range l.Tracks {
		l.Tracks[i].Start += pregap
	}
	l.Tracks[0].Pregap = pregap
	l.Leadout += pregap
}

// AdjustTOCByPregap creates a new TOC string with the given pregap applied.
// This is useful for trying different pregap values against the CTDB database.
func AdjustTOCByPregap(tocStr string, pregap int) string {
	frames, err := ParseTOCString(tocStr)
	if err != nil || len(frames) == 0 {
		return tocStr
	}

	var b strings.Builder
	for i, f := range frames {
		if i > 0 {
			b.WriteString(":")
		}
		b.WriteString(strconv.Itoa(f + pregap))
	}
	return b.String()
}
