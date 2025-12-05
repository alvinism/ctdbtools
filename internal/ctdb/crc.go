package ctdb

import (
	"errors"

	"ctdbtools/internal/accuraterip"
	"ctdbtools/internal/hashes"
)

// CRCComputer computes CTDB CRCs over track windows with offset/prefix/suffix trimming.
// This is a direct computation over samples (simpler than CUETools' precomputed rolling tables),
// but produces the same result when provided identical windows and trims.
type CRCComputer struct {
	MaxOffset int // maximum allowed trim/offset in samples
}

// TrackCRC computes CTDB CRC for a track window given offset and leadin/leadout trims (in samples).
// offset is drive offset in samples; positive drops from start and pads zeros at end.
// prefix/suffix are additional trims (as used in CUETools with stride/laststride).
func (c CRCComputer) TrackCRC(tw accuraterip.TrackWindow, offset int, prefixSamples int, suffixSamples int) (uint32, error) {
	if prefixSamples < 0 || prefixSamples > c.MaxOffset || suffixSamples < 0 || suffixSamples > c.MaxOffset {
		return 0, ErrOutOfRange
	}
	if prefixSamples+suffixSamples > len(tw.Track) {
		return 0, ErrOutOfRange
	}

	crc := uint32(0xffffffff)
	for i := prefixSamples; i < len(tw.Track)-suffixSamples; i++ {
		sample := accuraterip.SampleAt(tw, i, offset)
		crc = hashes.Update16(crc, uint16(sample&0xffff))
		crc = hashes.Update16(crc, uint16(sample>>16))
	}
	return crc ^ 0xffffffff, nil
}

var ErrOutOfRange = errors.New("ctdb crc: offset/prefix/suffix out of range")
