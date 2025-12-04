package hashes

// CRC32 implements the same polynomial/table behavior as CUETools (standard IEEE).
// We expose Combine to stitch CRCs when offsets change, mirroring CUETools Crc32.Combine.

const (
	crc32Poly = 0xedb88320
)

var crc32Table [256]uint32

func init() {
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
}

// Update calculates CRC32 for data with an initial state.
// Initial state should already be XORed with 0xffffffff when matching CUETools usage.
func Update(crc uint32, data []byte) uint32 {
	for _, b := range data {
		crc = (crc >> 8) ^ crc32Table[(crc^uint32(b))&0xff]
	}
	return crc
}

// Combine merges crc1 (len1 bytes) with crc2 (len2 bytes) to produce CRC of concatenated data.
// Ported from zlib's crc32_combine which CUETools wraps.
func Combine(crc1, crc2 uint32, len2 int) uint32 {
	// degenerate cases
	if len2 == 0 {
		return crc1
	}
	// matrix exponentiation for the polynomial shift
	even := [32]uint32{}
	odd := [32]uint32{}

	// put operator for one zero bit in odd
	odd[0] = crc32Poly
	row := uint32(1)
	for i := 1; i < 32; i++ {
		odd[i] = row
		row <<= 1
	}

	// apply len2 zeros to crc1
	gf2MatrixSquare(&even, &odd)
	gf2MatrixSquare(&odd, &even)

	n := len2
	for n != 0 {
		// apply zeros for this bit of len2
		gf2MatrixSquare(&even, &odd)
		if n&1 == 1 {
			crc1 = gf2MatrixTimes(&even, crc1)
		}
		n >>= 1
		if n == 0 {
			break
		}
		gf2MatrixSquare(&odd, &even)
		if n&1 == 1 {
			crc1 = gf2MatrixTimes(&odd, crc1)
		}
		n >>= 1
	}

	crc1 ^= crc2
	return crc1
}

func gf2MatrixTimes(mat *[32]uint32, vec uint32) uint32 {
	sum := uint32(0)
	i := 0
	for vec != 0 {
		if vec&1 == 1 {
			sum ^= mat[i]
		}
		vec >>= 1
		i++
	}
	return sum
}

func gf2MatrixSquare(square *[32]uint32, mat *[32]uint32) {
	for n := 0; n < 32; n++ {
		square[n] = gf2MatrixTimes(mat, mat[n])
	}
}
