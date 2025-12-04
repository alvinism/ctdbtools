package accuraterip

// ParityAggregator wraps ParityState with stride/laststride handling.
// This mirrors CueTools AccurateRipVerify parity accumulation.
//
// The parity window is now controlled inside ParityState via:
//   - currentSample = sampleCount - pregap*588
//   - currentStride = (currentSample * 2) / stride
//   - doParity = currentStride >= 1 && currentStride <= stridecount
type ParityAggregator struct {
	stride     int
	lastStride int
	npar       int
	state      *ParityState
	tailState  *ParityState
}

// NewParityAggregator creates a parity aggregator.
// pregap is in frames (typically TOC.Pregap), finalSampleCount is AudioLength * 588.
func NewParityAggregator(stride, lastStride, npar, pregap, finalSampleCount int) *ParityAggregator {
	if lastStride == 0 {
		lastStride = stride
	}
	var tail *ParityState
	if lastStride != stride {
		tail = NewParityState(lastStride, npar, pregap, finalSampleCount)
		tail.LastStride = lastStride
	}
	state := NewParityState(stride, npar, pregap, finalSampleCount)
	state.LastStride = lastStride
	return &ParityAggregator{
		stride:     stride,
		lastStride: lastStride,
		npar:       npar,
		state:      state,
		tailState:  tail,
	}
}

func (p *ParityAggregator) LastStride() int { return p.lastStride }

// FeedSamples consumes samples and updates parity.
// The parity window and lead-in/out handling is now done inside ParityState.AddSamples().
func (p *ParityAggregator) FeedSamples(samples []uint32) {
	// Feed to main state - it handles the parity window logic internally
	p.state.AddSamples(samples)

	// If we have a tail state for different lastStride, feed there too
	// Note: In CueTools, the tail handling is more complex and involves
	// separate syndrome calculation for the final laststride window.
	// For now, we feed both states; the tail can be used for final stride verification.
	if p.tailState != nil {
		p.tailState.AddSamples(samples)
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

// SyndromeWithOffset returns syndrome adjusted for drive offset.
func (p *ParityAggregator) SyndromeWithOffset(offset int, strides int) [][]uint16 {
	return p.state.SyndromeWithOffset(offset, strides)
}

// State returns the underlying ParityState for advanced access.
func (p *ParityAggregator) State() *ParityState {
	return p.state
}
