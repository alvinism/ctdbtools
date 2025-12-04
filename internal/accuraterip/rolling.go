package accuraterip

import (
	"fmt"

	"ctdbtool/internal/hashes"
	"ctdbtool/internal/toc"
)

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
// Unlike CueTools, we always size for stride+laststride to support CTDB verification even when
// parity calculation is disabled. CueTools uses smaller tables when calcParity=false, but then
// CTDB verification wouldn't work without parity mode enabled.
func NewRollingTables(layout toc.Layout, stride, laststride int, calcParity bool) *RollingTables {
	// Always use stride + laststride for CTDB support, with minimum of 4096*2
	maxOffset := stride + laststride
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

// debugFeed controls whether to print debug info during FeedSamples
// Disabled by default to avoid performance impact
var debugFeed = false

// SetDebugFeed enables/disables FeedSamples debug output
func SetDebugFeed(enabled bool) {
	debugFeed = enabled
}

// FeedSamples updates rolling CRC tables with a chunk of stereo samples for the current track.
// samplesPerTrackPosition is zero-based sample index within the current track before this chunk.
// trackIndex is 1-based audio track number; 0 is disc.
// trackTotalSamples is the total number of samples in this track (needed for tail caching).
// This mirrors the inner loop of CUETools AccurateRipVerify.CalculateCRCs.
func (rt *RollingTables) FeedSamples(trackIndex int, samplesPerTrackPosition int, trackTotalSamples int, samples []uint32) {
	// Initialize rolling CRC state for this track at current offset slot.
	// CueTools uses crcTrack = currentTrack + (samplesDoneTrack == 0 && currentTrack > 0 ? -1 : 0)
	// This means CRC32/CRCWN/CRCNL carry over from previous track at the start of each track.
	crcar := rt.CRCAR[trackIndex][0]
	crcsm := rt.CRCSM[trackIndex][0]
	crcv2 := rt.CRCV2[trackIndex][0]

	// For CRC32/CRCWN/CRCNL, use previous track's accumulated state at track start
	crcTrack := trackIndex
	if samplesPerTrackPosition == 0 && trackIndex > 0 {
		crcTrack = trackIndex - 1
	}
	crc32 := rt.CRC32[crcTrack][2*rt.MaxOffset]
	crcwn := rt.CRCWN[crcTrack][2*rt.MaxOffset]
	crcnl := rt.CRCNL[crcTrack][2*rt.MaxOffset]

	if debugFeed && trackIndex == 1 && samplesPerTrackPosition < 10000 {
		fmt.Printf("DEBUG: FeedSamples track=%d pos=%d len=%d crcTrack=%d maxOffset=%d crc32_init=%08X readFrom=[%d][%d]\n",
			trackIndex, samplesPerTrackPosition, len(samples), crcTrack, rt.MaxOffset, crc32, crcTrack, 2*rt.MaxOffset)
	}

	for i, sample := range samples {
		posInTrack := samplesPerTrackPosition + i
		samplesRemaining := trackTotalSamples - posInTrack

		if debugFeed && trackIndex == 1 && samplesPerTrackPosition == 0 && i < 5 {
			fmt.Printf("DEBUG: Sample[%d] = %08X, crc32 before update = %08X\n", i, sample, crc32)
		}

		// Determine cache offset following CueTools logic:
		// - Head: samplesDoneTrack < maxOffset -> offset = samplesDoneTrack
		// - Tail: samplesRemTrack <= maxOffset -> offset = 2*maxOffset - samplesRemTrack
		// - Otherwise: -1 (don't cache)
		offset := -1
		if posInTrack < rt.MaxOffset {
			offset = posInTrack
		} else if samplesRemaining <= rt.MaxOffset {
			offset = 2*rt.MaxOffset - samplesRemaining
		}

		// Cache values at computed offset
		if offset >= 0 && offset < 3*rt.MaxOffset {
			rt.CRCAR[trackIndex][offset] = crcar
			rt.CRCSM[trackIndex][offset] = crcsm
			rt.CRC32[trackIndex][offset] = crc32
			rt.CRCWN[trackIndex][offset] = crcwn
			rt.CRCNL[trackIndex][offset] = crcnl
			rt.CRCV2[trackIndex][offset] = crcv2
			if debugFeed && offset == 5880 && trackIndex == 1 {
				fmt.Printf("DEBUG: Caching at CRC32[1][5880] = %08X (posInTrack=%d, samplesPerTrackPosition=%d, i=%d)\n",
					crc32, posInTrack, samplesPerTrackPosition, i)
			}
		}

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

	// Store final accumulated states
	rt.CRCAR[trackIndex][0] = crcar
	rt.CRCSM[trackIndex][0] = crcsm
	rt.CRC32[trackIndex][2*rt.MaxOffset] = crc32
	rt.CRCWN[trackIndex][2*rt.MaxOffset] = crcwn
	rt.CRCNL[trackIndex][2*rt.MaxOffset] = crcnl
	rt.CRCV2[trackIndex][0] = crcv2

	if debugFeed && trackIndex == 1 && samplesPerTrackPosition < 10000 {
		fmt.Printf("DEBUG: FeedSamples END track=%d stored crc32=%08X at [%d][%d]\n",
			trackIndex, crc32, trackIndex, 2*rt.MaxOffset)
	}
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
		dlen := toc.AudioLengthFrames() * 588
		if oi > 0 {
			crc = rt.CRC32[toc.AudioTracks][2*rt.MaxOffset]
			crc = hashes.Combine(rt.CRC32[0][oi], crc, (dlen-oi)*4)
			crc = hashes.Combine(crc, 0, oi*4)
		} else {
			crc = rt.CRC32[toc.AudioTracks][2*rt.MaxOffset+oi]
		}
		// Use CRCMASK for initial state: 0xffffffff ^ Combine(0xffffffff, 0, len*4)
		// This matches CueTools _CRCMASK[0]
		crcMask := uint32(0xffffffff) ^ hashes.Combine(0xffffffff, 0, dlen*4)
		crc ^= crcMask
	} else {
		trackLengthSamples := tocTrackLengthFrames(toc, track) * 588
		trackLengthBytes := trackLengthSamples * 4
		if oi > 0 {
			if track < toc.AudioTracks {
				crc = rt.CRC32[track+1][oi]
			} else {
				crc = hashes.Combine(rt.CRC32[track][2*rt.MaxOffset], 0, oi*4)
			}
			crc = hashes.Combine(rt.CRC32[track][oi], crc, trackLengthBytes)
		} else {
			crc = hashes.Combine(rt.CRC32[track-1][2*rt.MaxOffset+oi], rt.CRC32[track][2*rt.MaxOffset+oi], trackLengthBytes)
		}
		// Use CRCMASK for initial state: 0xffffffff ^ Combine(0xffffffff, 0, len*4)
		// This matches CueTools _CRCMASK[iTrack]
		crcMask := uint32(0xffffffff) ^ hashes.Combine(0xffffffff, 0, trackLengthBytes)
		crc ^= crcMask
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
	// clamp to track length in samples
	frames := t.TrackLengthFrames(track)
	samples := frames * 588
	if defaultTail > samples {
		return samples
	}
	return defaultTail
}

// CRCARWithOffset calculates AccurateRip v1 CRC for a track with offset, matching CueTools CRC(iTrack, oi).
// This properly handles the first track (skips first 5*588-1 samples) and last track (skips last 5*588 samples).
// iTrack is 0-based (0 = first audio track), oi is the offset.
func (rt *RollingTables) CRCARWithOffset(iTrack int, oi int, toc *toc.Layout) uint32 {
	// CueTools uses 0-based track index in CRC function
	// offs0 determines where to start (skipping first N samples for track 0)
	// offs1 determines where to end (relative to maxOffset for last track)
	var offs0, offs1 int
	if iTrack == 0 {
		offs0 = 5*588 + oi - 1
	} else {
		offs0 = oi
	}

	if iTrack == toc.AudioTracks-1 {
		offs1 = 2*rt.MaxOffset - 5*588 + oi
	} else if oi >= 0 {
		offs1 = 0
	} else {
		offs1 = 2*rt.MaxOffset + oi
	}

	// CRCAR and CRCSM use 1-based track index in our arrays
	track := iTrack + 1

	var crcA, sumA uint32
	if offs1 >= 0 && offs1 < len(rt.CRCAR[track]) {
		crcA = rt.CRCAR[track][offs1]
	}
	if offs0 > 0 && offs0 < len(rt.CRCAR[track]) {
		crcA -= rt.CRCAR[track][offs0]
	}

	if offs1 >= 0 && offs1 < len(rt.CRCSM[track]) {
		sumA = rt.CRCSM[track][offs1]
	}
	if offs0 > 0 && offs0 < len(rt.CRCSM[track]) {
		sumA -= rt.CRCSM[track][offs0]
	}

	crc := crcA - sumA*uint32(oi)

	// Handle negative offset borrowing from previous track
	if oi < 0 && iTrack > 0 {
		prevTrack := iTrack // 1-based prev = iTrack (since iTrack is 0-based current)
		crcB := rt.CRCAR[prevTrack][0] - rt.CRCAR[prevTrack][2*rt.MaxOffset+oi]
		sumB := rt.CRCSM[prevTrack][0] - rt.CRCSM[prevTrack][2*rt.MaxOffset+oi]
		posB := uint32(toc.TrackLengthFrames(iTrack)*588 + oi) // track iTrack in 0-based = track iTrack+1 in layout
		crc += crcB - sumB*posB
	}

	// Handle positive offset borrowing from next track
	if oi > 0 && iTrack < toc.AudioTracks-1 {
		nextTrack := iTrack + 2 // 1-based next = iTrack + 2 (since iTrack is 0-based current)
		if oi < len(rt.CRCAR[nextTrack]) {
			crcB := rt.CRCAR[nextTrack][oi]
			sumB := rt.CRCSM[nextTrack][oi]
			posB := uint32(toc.TrackLengthFrames(iTrack+2)*588 + -oi)
			crc += crcB + sumB*posB
		}
	}

	return crc
}

// CRCV2WithOffset calculates AccurateRip v2 CRC for a track, matching CueTools CRCV2(iTrack).
// iTrack is 0-based.
func (rt *RollingTables) CRCV2WithOffset(iTrack int, toc *toc.Layout) uint32 {
	offs0 := 0
	if iTrack == 0 {
		offs0 = 5*588 - 1
	}
	offs1 := 0
	if iTrack == toc.AudioTracks-1 {
		offs1 = 2*rt.MaxOffset - 5*588
	}

	track := iTrack + 1
	var crcA1, crcA2 uint32
	if offs1 >= 0 && offs1 < len(rt.CRCAR[track]) {
		crcA1 = rt.CRCAR[track][offs1]
	}
	if offs0 > 0 && offs0 < len(rt.CRCAR[track]) {
		crcA1 -= rt.CRCAR[track][offs0]
	}

	if offs1 >= 0 && offs1 < len(rt.CRCV2[track]) {
		crcA2 = rt.CRCV2[track][offs1]
	}
	if offs0 > 0 && offs0 < len(rt.CRCV2[track]) {
		crcA2 -= rt.CRCV2[track][offs0]
	}

	return crcA1 + crcA2
}

// CTDBCRCWithOffset computes CTDB-style CRC for a track with prefix/suffix skipping.
// This mirrors CueTools AccurateRipVerify.CTDBCRC(iTrack, oi, prefixSamples, suffixSamples).
// track = 0 for disc CRC, 1+ for audio track (1-based).
// oi = drive offset in samples.
// prefixSamples = samples to skip at disc start (typically stride/2).
// suffixSamples = samples to skip at disc end (typically laststride/2).
func (rt *RollingTables) CTDBCRCWithOffset(track, oi, prefixSamples, suffixSamples int, toc *toc.Layout) uint32 {
	// CueTools adjusts prefix/suffix by offset
	prefixSamples += oi
	suffixSamples -= oi

	// Validate bounds - need prefixSamples < maxOffset and suffixSamples <= maxOffset
	// Note: suffixSamples can be == maxOffset (edge case)
	if prefixSamples < 0 || prefixSamples >= rt.MaxOffset || suffixSamples < 0 || suffixSamples > rt.MaxOffset {
		return 0 // out of range
	}

	if track == 0 {
		// Disc CRC: combines from track 1 head to last track tail with prefix/suffix trimmed
		discLen := toc.AudioLengthFrames() * 588
		if len(toc.Tracks) > 0 {
			discLen -= toc.Tracks[0].Pregap * 588 // subtract pregap like CueTools
		}
		chunkLen := discLen - prefixSamples - suffixSamples

		// _CRC32[1, prefixSamples] = head of track 1 at prefixSamples
		// _CRC32[AudioTracks, 2*maxOffset - suffixSamples] = tail of last track
		crcHead := rt.CRC32[1][prefixSamples]
		crcTail := rt.CRC32[toc.AudioTracks][2*rt.MaxOffset-suffixSamples]

		// 0xffffffff ^ Combine(0xffffffff ^ crcHead, crcTail, chunkLen * 4)
		return 0xffffffff ^ hashes.Combine(0xffffffff^crcHead, crcTail, chunkLen*4)
	}

	// Track CRC with prefix/suffix handling for first/last tracks
	// posA = start position, posB = end position
	var posA, posB int
	if track > 1 {
		posA = toc.TrackStartFrame(track)*588 + oi
	} else {
		posA = toc.TrackStartFrame(track)*588 + prefixSamples
	}

	if track < toc.AudioTracks {
		posB = toc.TrackStartFrame(track+1)*588 + oi
	} else {
		posB = toc.Leadout*588 - suffixSamples
	}

	var crcA, crcB uint32
	if oi > 0 {
		// Positive offset: borrow from next track
		if track > 1 {
			crcA = rt.CRC32[track][oi]
		} else {
			crcA = rt.CRC32[track][prefixSamples]
		}
		if track < toc.AudioTracks {
			crcB = rt.CRC32[track+1][oi]
		} else {
			crcB = rt.CRC32[track][2*rt.MaxOffset-suffixSamples]
		}
	} else {
		// Zero or negative offset: borrow from previous track
		if track > 1 {
			crcA = rt.CRC32[track-1][2*rt.MaxOffset+oi]
		} else {
			crcA = rt.CRC32[track][prefixSamples]
		}
		if track < toc.AudioTracks {
			crcB = rt.CRC32[track][2*rt.MaxOffset+oi]
		} else {
			// Last track: get CRC at position excluding suffix samples
			crcB = rt.CRC32[track][2*rt.MaxOffset-suffixSamples]
		}
	}


	// 0xffffffff ^ Combine(0xffffffff ^ crcA, crcB, (posB - posA) * 4)
	return 0xffffffff ^ hashes.Combine(0xffffffff^crcA, crcB, (posB-posA)*4)
}
