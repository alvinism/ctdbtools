package accuraterip

import "ctdbtool/internal/hashes"

// CRC32WithOffset recomputes CRC32 for a stereo sample slice after applying a sample offset.
// Positive offset drops that many samples from the start and pads the end with zeros.
// Negative offset pads the start with zeros and drops that many samples from the end.
// Offset is in stereo samples (uint32 values).
func CRC32WithOffset(samples []uint32, offset int) uint32 {
	crc := uint32(0xffffffff)

	if offset > 0 {
		if offset < len(samples) {
			samples = samples[offset:]
		} else {
			samples = nil
		}
		for _, s := range samples {
			crc = hashes.Update16(crc, uint16(s&0xffff))
			crc = hashes.Update16(crc, uint16(s>>16))
		}
		// pad trailing zeros (implicit no-op on CRC since Update16 skips xor when data is zero)
		for i := 0; i < offset; i++ {
			crc = hashes.Update16(crc, 0)
			crc = hashes.Update16(crc, 0)
		}
	} else if offset < 0 {
		off := -offset
		for i := 0; i < off; i++ {
			crc = hashes.Update16(crc, 0)
			crc = hashes.Update16(crc, 0)
		}
		if off < len(samples) {
			samples = samples[:len(samples)-off]
		} else {
			samples = nil
		}
		for _, s := range samples {
			crc = hashes.Update16(crc, uint16(s&0xffff))
			crc = hashes.Update16(crc, uint16(s>>16))
		}
	} else {
		for _, s := range samples {
			crc = hashes.Update16(crc, uint16(s&0xffff))
			crc = hashes.Update16(crc, uint16(s>>16))
		}
	}
	return crc ^ 0xffffffff
}

// CRCWONULLWithOffset recomputes CRC32 ignoring null samples with the given sample offset.
// The length contribution is only from non-null samples, matching CUETools behavior.
func CRCWONULLWithOffset(samples []uint32, offset int) uint32 {
	crc := uint32(0xffffffff)

	emit := func(s uint16) {
		if s == 0 {
			return
		}
		crc = hashes.Update16(crc, s)
	}

	if offset > 0 {
		if offset < len(samples) {
			samples = samples[offset:]
		} else {
			samples = nil
		}
		for _, s := range samples {
			emit(uint16(s & 0xffff))
			emit(uint16(s >> 16))
		}
		// padding zeros do not affect CRCWONULL
	} else if offset < 0 {
		off := -offset
		// leading zeros ignored
		if off < len(samples) {
			samples = samples[:len(samples)-off]
		} else {
			samples = nil
		}
		for _, s := range samples {
			emit(uint16(s & 0xffff))
			emit(uint16(s >> 16))
		}
	} else {
		for _, s := range samples {
			emit(uint16(s & 0xffff))
			emit(uint16(s >> 16))
		}
	}
	return crc ^ 0xffffffff
}
