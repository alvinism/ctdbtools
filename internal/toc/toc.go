package toc

import "fmt"

// Layout models a CD table of contents.
// Times/positions are measured in frames (1/75 second) to match CUETools math.
type Layout struct {
	FirstAudio  int
	AudioTracks int
	Tracks      []Track
}

// Track represents a single track within a TOC.
type Track struct {
	Start  int // start offset in frames
	End    int // inclusive end offset in frames
	Pregap int // pregap frames preceding this track (audio data)
	Length int // track length in sectors (frames)
}

// TOCID computes the CUETools TOCID string for the layout.
// TODO: port SHA1 logic from CUETools.CDImage/CDImage.cs once layout parsing is in place.
func (l Layout) TOCID() (string, error) {
	return "", ErrNotImplemented
}

// ErrNotImplemented is a sentinel for stubbed functionality.
var ErrNotImplemented = fmt.Errorf("not implemented")
