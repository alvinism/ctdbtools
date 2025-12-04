package accuraterip

// ParityAggregator wraps ParityState with stride/laststride handling.
// This is a simplified parity accumulator; it records parity for the main stride
// and a trailing laststride region to mirror CUETools' leadin/leadout use.
type ParityAggregator struct {
	stride     int
	lastStride int
	state      *ParityState
}

func NewParityAggregator(stride, lastStride, npar int) *ParityAggregator {
	if lastStride == 0 {
		lastStride = stride
	}
	return &ParityAggregator{
		stride:     stride,
		lastStride: lastStride,
		state:      NewParityState(stride, npar),
	}
}

// FeedSamples consumes samples at a global sample offset (stereo samples) and updates parity.
func (p *ParityAggregator) FeedSamples(globalSampleOffset int, samples []uint32) {
	for i, s := range samples {
		part := (globalSampleOffset + i) % p.stride
		p.state.AddSamples([]uint32{s}, part)
	}
}

// Syndrome returns current syndrome matrix.
func (p *ParityAggregator) Syndrome() [][]uint16 {
	return p.state.Syndrome()
}
