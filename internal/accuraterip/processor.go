package accuraterip

import "ctdbtool/internal/toc"

// Processor coordinates rolling CRCs and parity aggregation while streaming PCM samples.
// This is a simplified ingestion scaffold; it assumes samples are fed per track in order.
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
func NewProcessor(layout toc.Layout, stride, laststride, npar int, calcParity bool) *Processor {
	return &Processor{
		layout:  layout,
		rolling: NewRollingTables(layout, stride, laststride, calcParity),
		parity:  NewParityAggregator(stride, laststride, npar),
	}
}

// StartTrack initializes counters for a new track; leadIn/leadOut are sample counts to skip for parity.
func (p *Processor) StartTrack(trackIndex int, leadIn, leadOut int) {
	p.currentTrack = trackIndex
	p.trackSamples = 0
	p.leadIn = leadIn
	p.leadOut = leadOut
	p.totalSamples = tocTrackLengthFrames(&p.layout, trackIndex) * 588
}

// Feed consumes PCM stereo samples for the current track.
func (p *Processor) Feed(samples []uint32) {
	p.rolling.FeedSamples(p.currentTrack, p.trackSamples, samples)
	p.parity.FeedSamples(p.trackSamples, samples, p.leadIn, p.leadOut, p.totalSamples)
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
	return p.parity.Syndrome()
}
