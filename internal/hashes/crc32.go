package hashes

// CRC32 implements the same polynomial/table behavior as CUETools (standard IEEE).
// We expose Combine to stitch CRCs when offsets change, mirroring CUETools Crc32.Combine.

const (
	crc32Poly = 0xedb88320
	gf2Dim    = 32
)

var (
	crc32Table   [256]uint32
	combineTable [gf2Dim][gf2Dim]uint32
)

func init() {
	// Build CRC32 lookup table (standard IEEE polynomial)
	for i := 0; i < 256; i++ {
		crc := uint32(i)
		for j := 0; j < 8; j++ {
			if crc&1 == 1 {
				crc = (crc >> 1) ^ crc32Poly
			} else {
				crc >>= 1
			}
		}
		crc32Table[i] = crc
	}

	// Build combineTable matching CueTools CRC32.cs static constructor (lines 130-151)
	// combineTable[0] is the operator for one zero bit
	combineTable[0][0] = crc32Poly
	for n := 1; n < gf2Dim; n++ {
		combineTable[0][n] = 1 << (n - 1)
	}

	// Square the matrix repeatedly to get operators for 2, 4, 8, ... zero bits
	for i := 1; i < gf2Dim; i++ {
		gf2MatrixSquare(&combineTable[i], &combineTable[i-1])
	}
}

// Update calculates CRC32 for data with an initial state.
// Initial state should already be XORed with 0xffffffff when matching CUETools usage.
func Update(crc uint32, data []byte) uint32 {
	for _, b := range data {
		crc = (crc >> 8) ^ crc32Table[(crc^uint32(b))&0xff]
	}
	return crc
}

// Combine merges crc1 with crc2 (len2 bytes) to produce CRC of concatenated data.
// This matches CueTools CRC32.Combine exactly (CRC32.cs lines 202-229).
//
// Key difference from zlib's crc32_combine: CueTools uses precomputed tables
// and starts at n=3, cycling (n+1) & 31.
func Combine(crc1, crc2 uint32, len2 int) uint32 {
	// degenerate cases (matching CueTools lines 205-210)
	if len2 == 0 {
		return crc1
	}
	if crc1 == 0 {
		return crc2
	}
	if len2 < 0 {
		// CueTools throws ArgumentException, we just return crc1
		return crc1
	}

	// Apply zeros operator for each bit of len2
	// CueTools uses n=3 as starting point and cycles (n+1) & 31
	// This is because combineTable[3] corresponds to the 8-bit (1 byte) operator
	n := 3
	for len2 != 0 {
		if len2&1 != 0 {
			crc1 = gf2MatrixTimes(&combineTable[n], crc1)
		}
		len2 >>= 1
		n = (n + 1) & (gf2Dim - 1)
	}

	// Return combined CRC
	crc1 ^= crc2
	return crc1
}

// gf2MatrixTimes multiplies a GF(2) matrix by a vector.
// This matches CueTools gf2_matrix_times (CRC32.cs lines 156-193).
func gf2MatrixTimes(mat *[gf2Dim]uint32, vec uint32) uint32 {
	sum := uint32(0)
	for i := 0; vec != 0; i++ {
		if vec&1 != 0 {
			sum ^= mat[i]
		}
		vec >>= 1
	}
	return sum
}

// gf2MatrixSquare squares a GF(2) matrix.
// This matches CueTools gf2_matrix_square (CRC32.cs lines 196-200).
func gf2MatrixSquare(square *[gf2Dim]uint32, mat *[gf2Dim]uint32) {
	for n := 0; n < gf2Dim; n++ {
		square[n] = gf2MatrixTimes(mat, mat[n])
	}
}
