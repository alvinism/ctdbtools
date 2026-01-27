package repair

import "ctdbtools/internal/toc"

// GetTrackSampleRange returns the sample range [min, max) for a track in 16-bit samples.
// This matches CueTools convention where positions are relative to first audio track start.
func GetTrackSampleRange(layout toc.Layout, track int) (min, max int) {
	firstTrackStart := layout.TrackStartFrame(1)
	trackStart := layout.TrackStartFrame(track)
	trackEnd := trackStart + layout.TrackLengthFrames(track)

	// Convert frames to 16-bit samples (1 frame = 588 stereo = 1176 16-bit)
	min = (trackStart - firstTrackStart) * 588 * 2
	max = (trackEnd - firstTrackStart) * 588 * 2
	return min, max
}

// FirstRowMatch checks if two syndrome first rows match (XOR is all zeros).
// This is O(npar) instead of O(stride * npar) for full syndrome match.
func FirstRowMatch(local, ctdb []uint16, npar int) bool {
	for j := 0; j < npar && j < len(ctdb) && j < len(local); j++ {
		if local[j]^ctdb[j] != 0 {
			return false
		}
	}
	return true
}
