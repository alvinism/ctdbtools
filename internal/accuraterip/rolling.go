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
