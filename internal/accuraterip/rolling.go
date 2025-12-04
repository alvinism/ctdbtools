package accuraterip

import "ctdbtool/internal/toc"

// RollingTables mirrors CUETools' rolling CRC arrays for AccurateRip/CTDB offset calculations.
// It holds preallocated buffers sized to 3*maxOffset per track; filling/updating is TODO.
type RollingTables struct {
	MaxOffset     int
	OffsetRangeAR int

	CRCAR [][]uint32
	CRCSM [][]uint32
	CRC32 [][]uint32
	CRCWN [][]uint32
	CRCNL [][]int
	CRCV2 [][]uint32
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
	}
}
