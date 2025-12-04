package accuraterip

import (
	"ctdbtool/internal/hashes"
	"ctdbtool/internal/toc"
)

// TrackStats holds per-track checksum results mirroring CUETools AccurateRip fields.
type TrackStats struct {
	Frames          int    // number of CD frames (75 Hz) in this track
	Samples         int    // stereo samples processed (Frames*588)
	NonNullSamples  int    // samples that were non-zero (used by CRCWONULL length)
	Peak            int    // peak absolute sample value across both channels
	CRCAR           uint32 // AccurateRip CRC (sum of sample*position low 32 bits)
	CRCV2           uint32 // AccurateRip v2 upper bits
	CRC32           uint32 // CRC32 with nulls
	CRCWONULL       uint32 // CRC32 excluding null samples (EAC-style)
}

// Calculator processes PCM stereo 16-bit samples in track order and computes AR CRCs.
// This focuses on zero-offset CRCs; offset-aware/CTDB parity handling will be layered on later.
type Calculator struct {
	layout toc.Layout
}

// NewCalculator constructs a calculator for a disc layout.
func NewCalculator(layout toc.Layout) *Calculator {
	return &Calculator{layout: layout}
}

// ProcessTrack consumes an entire track worth of stereo samples (16-bit per channel packed into uint32 L|R).
// The caller is responsible for ensuring samples align to the track length (frames*588).
func (c *Calculator) ProcessTrack(samples []uint32, frameCount int) TrackStats {
	var stats TrackStats
	stats.Frames = frameCount
	stats.Samples = len(samples)

	crc32 := uint32(0xffffffff)
	crcwn := uint32(0xffffffff)

	for i, s := range samples {
		pos := i + 1 // AccurateRip positions are 1-based
		crcA, crcV2 := hashes.AccurateRipCRC(stats.CRCAR, s, pos)
		stats.CRCAR = crcA
		stats.CRCV2 += crcV2

		lo := uint16(s & 0xffff)
		hi := uint16(s >> 16)

		crc32 = hashes.Update16(crc32, lo)
		crc32 = hashes.Update16(crc32, hi)

		if lo != 0 {
			crcwn = hashes.Update16(crcwn, lo)
			stats.NonNullSamples++
		}
		if hi != 0 {
			crcwn = hashes.Update16(crcwn, hi)
			stats.NonNullSamples++
		}

		if p := abs16(int16(lo)); p > stats.Peak {
			stats.Peak = p
		}
		if p := abs16(int16(hi)); p > stats.Peak {
			stats.Peak = p
		}
	}

	// finalize CRC32/CRCWONULL to match standard initial xor/final xor
	stats.CRC32 = crc32 ^ 0xffffffff
	stats.CRCWONULL = crcwn ^ 0xffffffff
	return stats
}

func abs16(v int16) int {
	if v < 0 {
		return -int(v)
	}
	return int(v)
}
