package accuraterip

import "ctdbtool/internal/hashes"

// TrackWindow represents a track with optional leading/trailing neighbor samples.
// Prefix/Suffix should contain up to maxOffset samples from adjacent tracks to allow offset windows.
type TrackWindow struct {
	Prefix []uint32
	Track  []uint32
	Suffix []uint32
}

// sampleAt returns the sample visible at position idx within the window after applying offset.
// idx is relative to the current track (0-based).
func (tw TrackWindow) sampleAt(idx, offset int) uint32 {
	src := idx + offset
	if src < 0 {
		pos := len(tw.Prefix) + src
		if pos >= 0 && pos < len(tw.Prefix) {
			return tw.Prefix[pos]
		}
		return 0
	}
	if src < len(tw.Track) {
		return tw.Track[src]
	}
	tail := src - len(tw.Track)
	if tail >= 0 && tail < len(tw.Suffix) {
		return tw.Suffix[tail]
	}
	return 0
}

// ComputeCRCs returns AccurateRip CRC (v1+v2), CRC32, CRCWONULL, non-null sample count, and peak for a window at the given offset.
func ComputeCRCs(tw TrackWindow, offset int) (ar, arv2, crc32, crcwn uint32, nonNull int, peak int) {
	crc32 = 0xffffffff
	crcwn = 0xffffffff
	for i := 0; i < len(tw.Track); i++ {
		sample := tw.sampleAt(i, offset)
		pos := i + 1
		ar, arv2 = hashes.AccurateRipCRC(ar, sample, pos)

		lo := uint16(sample & 0xffff)
		hi := uint16(sample >> 16)
		crc32 = hashes.Update16(crc32, lo)
		crc32 = hashes.Update16(crc32, hi)

		if lo != 0 {
			crcwn = hashes.Update16(crcwn, lo)
			nonNull++
		}
		if hi != 0 {
			crcwn = hashes.Update16(crcwn, hi)
			nonNull++
		}

		if p := abs16(int16(lo)); p > peak {
			peak = p
		}
		if p := abs16(int16(hi)); p > peak {
			peak = p
		}
	}
	return ar, arv2, crc32 ^ 0xffffffff, crcwn ^ 0xffffffff, nonNull, peak
}

// CRC32WithOffset recomputes CRC32 for a slice by treating it as a track without neighbors.
func CRC32WithOffset(samples []uint32, offset int) uint32 {
	tw := TrackWindow{Track: samples}
	_, _, crc, _, _, _ := ComputeCRCs(tw, offset)
	return crc
}

// CRCWONULLWithOffset recomputes CRCWONULL for a slice by treating it as a track without neighbors.
func CRCWONULLWithOffset(samples []uint32, offset int) uint32 {
	tw := TrackWindow{Track: samples}
	_, _, _, crcwn, _, _ := ComputeCRCs(tw, offset)
	return crcwn
}
