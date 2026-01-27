package hashes

// Update16 updates CRC32 with a single 16-bit sample (little endian order) given a running CRC state.
// The crc parameter should already be xor'd with 0xffffffff if you want standard CRC32 finalization
// via crc^0xffffffff after the stream ends.
func Update16(crc uint32, sample uint16) uint32 {
	b0 := byte(sample)
	b1 := byte(sample >> 8)
	crc = (crc >> 8) ^ crc32Table[(crc^uint32(b0))&0xff]
	crc = (crc >> 8) ^ crc32Table[(crc^uint32(b1))&0xff]
	return crc
}

// AccurateRip CRC helpers (DiscIds are in toc package).
// This file will grow to include per-track CRC accumulation that mirrors CUETools.AccurateRip.CalculateCRCs.

// AccurateRipCRC accumulates the classic AR v1 CRC over PCM 16-bit stereo samples.
// sampleIndex is 1-based position within a track.
func AccurateRipCRC(crc uint32, sample uint32, sampleIndex int) (uint32, uint32) {
	// CRCAR uses sum(sample * position) modulo 2^32; CRCV2 tracks high bits.
	val := uint64(sample) * uint64(sampleIndex)
	crc += uint32(val)
	crcV2 := uint32(val >> 32)
	return crc, crcV2
}

