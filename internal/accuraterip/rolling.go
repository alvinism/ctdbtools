package accuraterip

import "ctdbtool/internal/toc"
import "ctdbtool/internal/hashes"

// RollingTables mirrors CUETools' rolling CRC arrays for AccurateRip/CTDB offset calculations.
// It holds preallocated buffers sized to 3*maxOffset per track.
type RollingTables struct {
	MaxOffset     int
	OffsetRangeAR int

	CRCAR      [][]uint32
	CRCSM      [][]uint32
	CRC32      [][]uint32
	CRCWN      [][]uint32
	CRCNL      [][]int
	CRCV2      [][]uint32
	CacheCRC32 [][]uint32
	CacheCRCWN [][]uint32
}

// NewRollingTables preallocates arrays based on layout and stride settings (matching CUETools logic).
func NewRollingTables(layout toc.Layout, stride, laststride int, calcParity bool) *RollingTables {
	maxOffset := stride + laststride
	if !calcParity {
		maxOffset = 0
	}
	if maxOffset < 4096*2 {
		maxOffset = 4096 * 2
	}
	if rem := maxOffset % 588; rem != 0 {
		maxOffset += 588 - rem
	}

	tracks := layout.AudioTracks + 1 // include track 0 (pregap/whole-disc)
	size := 3 * maxOffset

	makeUint := func() [][]uint32 {
		arr := make([][]uint32, tracks)
		for i := range arr {
			arr[i] = make([]uint32, size)
		}
		return arr
	}
	makeInt := func() [][]int {
		arr := make([][]int, tracks)
		for i := range arr {
			arr[i] = make([]int, size)
		}
		return arr
	}

	return &RollingTables{
		MaxOffset:     maxOffset,
		OffsetRangeAR: 5*588 - 1,
		CRCAR:         makeUint(),
		CRCSM:         makeUint(),
		CRC32:         makeUint(),
		CRCWN:         makeUint(),
		CRCNL:         makeInt(),
		CRCV2:         makeUint(),
		CacheCRC32:    makeUint(),
		CacheCRCWN:    makeUint(),
	}
}

// FeedSamples updates rolling CRC tables with a chunk of stereo samples for the current track.
// samplesPerTrackPosition is zero-based sample index within the current track before this chunk.
// trackIndex is 1-based audio track number; 0 is disc.
// This mirrors the inner loop of CUETools AccurateRipVerify.CalculateCRCs.
func (rt *RollingTables) FeedSamples(trackIndex int, samplesPerTrackPosition int, samples []uint32) {
	// Initialize rolling CRC state for this track at current offset slot.
	crcar := rt.CRCAR[trackIndex][0]
	crcsm := rt.CRCSM[trackIndex][0]
	crc32 := rt.CRC32[trackIndex][2*rt.MaxOffset]
	crcwn := rt.CRCWN[trackIndex][2*rt.MaxOffset]
	crcnl := rt.CRCNL[trackIndex][2*rt.MaxOffset]
	crcv2 := rt.CRCV2[trackIndex][0]

	for i, sample := range samples {
		posInTrack := samplesPerTrackPosition + i
		// cache pre-offset values for offsets that rely on leading data
		rt.CRCAR[trackIndex][posInTrack] = crcar
		rt.CRCSM[trackIndex][posInTrack] = crcsm
		rt.CRC32[trackIndex][posInTrack] = crc32
		rt.CRCWN[trackIndex][posInTrack] = crcwn
		rt.CRCNL[trackIndex][posInTrack] = crcnl
		rt.CRCV2[trackIndex][posInTrack] = crcv2

		crcsm += sample
		val := uint64(sample) * uint64(posInTrack+1)
		crcar += uint32(val)
		crcv2 += uint32(val >> 32)

		lo := uint16(sample & 0xffff)
		hi := uint16(sample >> 16)
		crc32 = hashes.Update16(crc32, lo)
		crc32 = hashes.Update16(crc32, hi)

		if lo != 0 {
			crcwn = hashes.Update16(crcwn, lo)
			crcnl++
		}
		if hi != 0 {
			crcwn = hashes.Update16(crcwn, hi)
			crcnl++
		}
	}

	// Store tail states in the maxOffset slot for this track
	rt.CRCAR[trackIndex][0] = crcar
	rt.CRCSM[trackIndex][0] = crcsm
	rt.CRC32[trackIndex][2*rt.MaxOffset] = crc32
	rt.CRCWN[trackIndex][2*rt.MaxOffset] = crcwn
	rt.CRCNL[trackIndex][2*rt.MaxOffset] = crcnl
	rt.CRCV2[trackIndex][0] = crcv2
}

// CRCZeroOffset returns CRC32 for a track at zero offset using cached values.
func (rt *RollingTables) CRCZeroOffset(track int, trackLengthSamples int) uint32 {
	crc := rt.CRC32[track][2*rt.MaxOffset]
	crc ^= 0xffffffff
	return crc
}

// CRCWithOffset replicates CUETools AccurateRipVerify.CRC32(iTrack, oi) using rolling tables.
// track 0 = whole disc.
func (rt *RollingTables) CRCWithOffset(track int, oi int, toc *toc.Layout) uint32 {
	if rt.CacheCRC32[track][rt.OffsetRangeAR+oi] != 0 {
		return rt.CacheCRC32[track][rt.OffsetRangeAR+oi]
	}
	var crc uint32
	if track == 0 {
		dlen := toc.AudioLengthFrames()
		if oi > 0 {
			crc = rt.CRC32[toc.AudioTracks][2*rt.MaxOffset]
			crc = hashes.Combine(rt.CRC32[0][oi], crc, (dlen-oi)*4)
			crc = hashes.Combine(crc, 0, oi*4)
		} else {
			crc = rt.CRC32[toc.AudioTracks][2*rt.MaxOffset+oi]
		}
		crc ^= 0xffffffff // initial xor
	} else {
		trackLength := tocTrackLengthFrames(toc, track) * 588 * 4
		if oi > 0 {
			if track < toc.AudioTracks {
				crc = rt.CRC32[track+1][oi]
			} else {
				crc = hashes.Combine(rt.CRC32[track][2*rt.MaxOffset], 0, oi*4)
			}
			crc = hashes.Combine(rt.CRC32[track][oi], crc, trackLength)
		} else {
			crc = hashes.Combine(rt.CRC32[track-1][2*rt.MaxOffset+oi], rt.CRC32[track][2*rt.MaxOffset+oi], trackLength)
		}
		crc ^= 0xffffffff
	}
	rt.CacheCRC32[track][rt.OffsetRangeAR+oi] = crc
	return crc
}

// CRCWONULLWithOffset mirrors CUETools AccurateRipVerify.CRCWONULL(iTrack, oi) using rolling tables.
func (rt *RollingTables) CRCWONULLWithOffset(track int, oi int, toc *toc.Layout) uint32 {
	if rt.CacheCRCWN[track][rt.OffsetRangeAR+oi] != 0 {
		return rt.CacheCRCWN[track][rt.OffsetRangeAR+oi]
	}
	var crc uint32
	var cnt int
	if track == 0 {
		if oi > 0 {
			cnt = rt.CRCNL[toc.AudioTracks][2*rt.MaxOffset] * 2
			crc = rt.CRCWN[toc.AudioTracks][2*rt.MaxOffset]
			cnt -= rt.CRCNL[0][oi] * 2
			crc = hashes.Combine(rt.CRCWN[0][oi], crc, cnt)
		} else {
			cnt = rt.CRCNL[toc.AudioTracks][2*rt.MaxOffset+oi] * 2
			crc = rt.CRCWN[toc.AudioTracks][2*rt.MaxOffset+oi]
		}
	} else {
		if oi > 0 {
			if track < toc.AudioTracks {
				cnt = rt.CRCNL[track+1][oi] * 2
				crc = rt.CRCWN[track+1][oi]
			} else {
				cnt = rt.CRCNL[track][2*rt.MaxOffset] * 2
				crc = rt.CRCWN[track][2*rt.MaxOffset]
			}
			cnt -= rt.CRCNL[track][oi] * 2
			crc = hashes.Combine(rt.CRCWN[track][oi], crc, cnt)
		} else {
			cnt = rt.CRCNL[track][2*rt.MaxOffset+oi] * 2
			crc = rt.CRCWN[track][2*rt.MaxOffset+oi]
			cnt -= rt.CRCNL[track-1][2*rt.MaxOffset+oi] * 2
			crc = hashes.Combine(rt.CRCWN[track-1][2*rt.MaxOffset+oi], crc, cnt)
		}
	}
	crc = hashes.Combine(0xffffffff, crc, cnt)
	crc ^= 0xffffffff
	rt.CacheCRCWN[track][rt.OffsetRangeAR+oi] = crc
	return crc
}

// helper to get track length in frames
func tocTrackLengthFrames(t *toc.Layout, track int) int {
	if track == 0 {
		return t.AudioLengthFrames()
	}
	return t.TrackLengthFrames(track)
}

func tocLeadInSamples(t *toc.Layout, track int) int {
	if track == 0 {
		return 0
	}
	idx := track + t.FirstAudio - 2
	if idx >= 0 && idx < len(t.Tracks) {
		return t.Tracks[idx].Pregap * 588
	}
	return 0
}

func tocLeadOutSamples(t *toc.Layout, track int, defaultTail int) int {
	if track == 0 {
		return defaultTail
	}
	// Use default tail unless specific leadout handling is needed per track
	return defaultTail
}
