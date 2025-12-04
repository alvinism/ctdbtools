package accuraterip

// ParityAggregator wraps ParityState with stride/laststride handling.
// This is a simplified parity accumulator; it records parity for the main stride
// and a trailing laststride region to mirror CUETools' leadin/leadout use.
type ParityAggregator struct {
	stride     int
	lastStride int
	state      *ParityState
	tailState  *ParityState
}

func NewParityAggregator(stride, lastStride, npar int) *ParityAggregator {
	if lastStride == 0 {
		lastStride = stride
	}
	var tail *ParityState
	if lastStride != stride {
		tail = NewParityState(lastStride, npar)
	}
	return &ParityAggregator{
		stride:     stride,
		lastStride: lastStride,
		state:      NewParityState(stride, npar),
		tailState:  tail,
	}
}

func (p *ParityAggregator) LastStride() int { return p.lastStride }

// FeedSamples consumes samples at a global sample offset (stereo samples) and updates parity.
// leadInSamples and leadOutSamples can be used to skip parity outside data region.
func (p *ParityAggregator) FeedSamples(globalSampleOffset int, samples []uint32, leadInSamples int, leadOutSamples int, totalSamples int) {
	for i, s := range samples {
		pos := globalSampleOffset + i
		if pos < leadInSamples {
			continue
		}
		if pos >= totalSamples-leadOutSamples {
			continue
		}
		// use tail stride if within last stride window
		if p.tailState != nil && pos >= totalSamples-p.lastStride {
			part := (pos - (totalSamples - p.lastStride)) % p.lastStride
			p.tailState.AddSamples([]uint32{s}, part, totalSamples)
		} else {
			part := pos % p.stride
			p.state.AddSamples([]uint32{s}, part, totalSamples)
		}
	}
}

// Syndrome returns current syndrome matrix.
func (p *ParityAggregator) Syndrome() [][]uint16 {
	return p.state.Syndrome()
}

// TailSyndrome returns the tail syndrome if lastStride differs; nil otherwise.
func (p *ParityAggregator) TailSyndrome() [][]uint16 {
	if p.tailState == nil {
		return nil
	}
	return p.tailState.Syndrome()
}
