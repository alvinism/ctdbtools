package accuraterip

import "ctdbtool/internal/toc"

// Processor coordinates rolling CRCs and parity aggregation while streaming PCM samples.
// This mirrors CueTools AccurateRipVerify - samples are fed in order across all tracks.
type Processor struct {
	layout       toc.Layout
	rolling      *RollingTables
	parity       *ParityAggregator
	currentTrack int
	trackSamples int
	leadIn       int
	leadOut      int
	totalSamples int
}

// NewProcessor builds a processor with the given stride/laststride and npar settings.
// The parity aggregator receives pregap (in frames) and finalSampleCount (AudioLength * 588)
// to match CueTools parity window calculation.
func NewProcessor(layout toc.Layout, stride, laststride, npar int, calcParity bool) *Processor {
	// CueTools: pregap = TOC.Pregap (pregap of first audio track in frames)
	// CueTools: finalSampleCount = TOC.AudioLength * 588
	pregap := 0
	if layout.AudioTracks > 0 && len(layout.Tracks) > 0 {
		pregap = layout.Tracks[0].Pregap
	}
	finalSampleCount := layout.AudioLengthFrames() * 588

	var parityAgg *ParityAggregator
	if calcParity {
		parityAgg = NewParityAggregator(stride, laststride, npar, pregap, finalSampleCount)
	}

	return &Processor{
		layout:  layout,
		rolling: NewRollingTables(layout, stride, laststride, calcParity),
		parity:  parityAgg,
	}
}

// StartTrack initializes counters for a new track; leadIn/leadOut are sample counts to skip for parity.
// Pass leadIn/leadOut < 0 to auto-derive (leadIn from pregap, leadOut defaults to lastStride).
// Note: With the updated parity implementation, leadIn/leadOut are no longer used for parity window
// control - that's now handled internally by ParityState based on CueTools' currentStride logic.
func (p *Processor) StartTrack(trackIndex int, leadIn, leadOut int) {
	p.currentTrack = trackIndex
	p.trackSamples = 0
	if leadIn < 0 {
		leadIn = tocLeadInSamples(&p.layout, trackIndex)
	}
	if leadOut < 0 {
		leadOut = tocLeadOutSamples(&p.layout, trackIndex, p.parityLastStride())
	}
	p.leadIn = leadIn
	p.leadOut = leadOut
	p.totalSamples = tocTrackLengthFrames(&p.layout, trackIndex) * 588
}

func (p *Processor) parityLastStride() int {
	if p.parity == nil {
		return 0
	}
	return p.parity.LastStride()
}

// Feed consumes PCM stereo samples for the current track.
func (p *Processor) Feed(samples []uint32) {
	p.rolling.FeedSamples(p.currentTrack, p.trackSamples, p.totalSamples, samples)
	if p.parity != nil {
		p.parity.FeedSamples(samples)
	}
	p.trackSamples += len(samples)
}

// CRC returns CRC32 at offset for the current track.
func (p *Processor) CRC(offset int) uint32 {
	return p.rolling.CRCWithOffset(p.currentTrack, offset, &p.layout)
}

// CRCWONULL returns CRC without nulls at offset for the current track.
func (p *Processor) CRCWONULL(offset int) uint32 {
	return p.rolling.CRCWONULLWithOffset(p.currentTrack, offset, &p.layout)
}

// Syndrome returns the current parity syndrome.
func (p *Processor) Syndrome() [][]uint16 {
	if p.parity == nil {
		return nil
	}
	return p.parity.Syndrome()
}

// TailSyndrome returns tail syndrome when lastStride differs.
func (p *Processor) TailSyndrome() [][]uint16 {
	if p.parity == nil {
		return nil
	}
	return p.parity.TailSyndrome()
}

// ParityLastStride exposes the last stride length for lead-out defaults.
func (p *Processor) ParityLastStride() int {
	return p.parityLastStride()
}

// OffsetSyndrome returns parity syndrome adjusted for offset.
func (p *Processor) OffsetSyndrome(offset int, strides int) [][]uint16 {
	if p.parity == nil {
		return nil
	}
	return p.parity.SyndromeWithOffset(offset, strides)
}

// Parity returns the underlying parity aggregator (may be nil if calcParity was false).
func (p *Processor) Parity() *ParityAggregator {
	return p.parity
}

// TrackCRC returns CRC32 at the given offset for a specific track (1-based).
func (p *Processor) TrackCRC(track int, offset int) uint32 {
	return p.rolling.CRCWithOffset(track, offset, &p.layout)
}

// TrackCRCWONULL returns CRC32 without nulls at the given offset for a specific track.
func (p *Processor) TrackCRCWONULL(track int, offset int) uint32 {
	return p.rolling.CRCWONULLWithOffset(track, offset, &p.layout)
}

// TrackCRCAR returns the AccurateRip v1 CRC for a specific track at zero offset.
// track is 1-based audio track number.
func (p *Processor) TrackCRCAR(track int) uint32 {
	// Convert to 0-based index for CRCARWithOffset
	return p.rolling.CRCARWithOffset(track-1, 0, &p.layout)
}

// TrackCRCARWithOffset returns the AccurateRip v1 CRC for a specific track at given offset.
// track is 1-based audio track number, oi is offset in samples.
// This allows searching for matches at different drive offsets (typically ±2939 samples).
func (p *Processor) TrackCRCARWithOffset(track, oi int) uint32 {
	// Convert to 0-based index for CRCARWithOffset
	return p.rolling.CRCARWithOffset(track-1, oi, &p.layout)
}

// TrackCRCV2 returns the AccurateRip v2 CRC for a specific track.
// track is 1-based audio track number.
func (p *Processor) TrackCRCV2(track int) uint32 {
	// Convert to 0-based index for CRCV2WithOffset
	return p.rolling.CRCV2WithOffset(track-1, &p.layout)
}

// Layout returns the TOC layout used by this processor.
func (p *Processor) Layout() toc.Layout {
	return p.layout
}

// Rolling returns the rolling tables for debug inspection.
func (p *Processor) Rolling() *RollingTables {
	return p.rolling
}

// TrackCTDBCRC returns CTDB-style CRC for a track with prefix/suffix skipping.
// track is 1-based audio track number.
// offset is drive offset in samples.
// stride and laststride are parity parameters (samples); the function uses stride/2 and laststride/2.
func (p *Processor) TrackCTDBCRC(track, offset, stride, laststride int) uint32 {
	return p.rolling.CTDBCRCWithOffset(track, offset, stride/2, laststride/2, &p.layout)
}

// DiscCTDBCRC returns CTDB-style CRC for the whole disc.
// offset is drive offset in samples.
// stride and laststride are parity parameters (samples); the function uses stride/2 and laststride/2.
func (p *Processor) DiscCTDBCRC(offset, stride, laststride int) uint32 {
	return p.rolling.CTDBCRCWithOffset(0, offset, stride/2, laststride/2, &p.layout)
}
