package parity

import "fmt"

// RsDecode implements Reed-Solomon error detection and localization.
// Ported from CUETools.Parity.RsDecode for use with CTDB syndrome data.
type RsDecode struct {
	galois *Galois
	npar   int
}

// NewRsDecode creates a new Reed-Solomon decoder for the given number of parity symbols.
func NewRsDecode(npar int) *RsDecode {
	return &RsDecode{
		galois: Galois16,
		npar:   npar,
	}
}

// CalcSigmaMBM calculates the error locator polynomial using the modified Berlekamp-Massey algorithm.
// syndrome is the XOR of local and CTDB syndromes (error syndrome).
// Returns the number of errors detected, or -1 if too many errors.
// sigma will contain the error locator polynomial coefficients.
func (r *RsDecode) CalcSigmaMBM(syndrome []int, sigma []int) int {
	// Initialize sigma to [1, 0, 0, ...]
	for i := range sigma {
		sigma[i] = 0
	}
	sigma[0] = 1

	// B(x) = 1, L = 0
	B := make([]int, r.npar+1)
	B[0] = 1
	L := 0

	for n := 0; n < r.npar; n++ {
		// Calculate discrepancy delta
		delta := syndrome[n]
		for i := 1; i <= L; i++ {
			if sigma[i] != 0 && syndrome[n-i] != 0 {
				delta ^= r.galois.mul(sigma[i], syndrome[n-i])
			}
		}

		// Shift B(x)
		for i := r.npar; i > 0; i-- {
			B[i] = B[i-1]
		}
		B[0] = 0

		if delta != 0 {
			if 2*L <= n {
				// Update L and swap
				temp := make([]int, r.npar+1)
				copy(temp, sigma)
				invDelta := r.galois.div(1, delta)
				for i := 0; i <= r.npar; i++ {
					sigma[i] ^= r.galois.mul(delta, B[i])
				}
				for i := 0; i <= r.npar; i++ {
					B[i] = r.galois.mul(invDelta, temp[i])
				}
				L = n + 1 - L
			} else {
				// Just update sigma
				for i := 0; i <= r.npar; i++ {
					sigma[i] ^= r.galois.mul(delta, B[i])
				}
			}
		}
	}

	// Check if error count is within correctable range
	if L > r.npar/2 {
		return -1 // Too many errors
	}

	return L
}

// ChienSearch finds error positions using Chien search algorithm.
// sigma is the error locator polynomial from CalcSigmaMBM.
// n is the codeword length (stridecount, NOT including npar).
// numErrors is the expected number of errors from CalcSigmaMBM.
// Returns the error positions (as GF elements), or nil if not all roots were found.
func (r *RsDecode) ChienSearch(sigma []int, n, numErrors int) []int {
	if numErrors == 0 {
		return []int{}
	}

	// For single error, use direct calculation
	if numErrors == 1 && sigma[1] != 0 {
		pos := r.galois.toLog(sigma[1])
		if pos >= n {
			return nil
		}
		return []int{r.galois.toExp(pos)}
	}

	// General Chien search
	// Evaluate sigma at each power of alpha
	sg := make([]int, numErrors+1)
	for i := 0; i <= numErrors; i++ {
		sg[i] = sigma[i]
	}

	positions := make([]int, 0, numErrors)
	for i := 0; i < n; i++ {
		// Evaluate polynomial at alpha^i
		sum := 1
		for j := 1; j <= numErrors; j++ {
			sum ^= sg[j]
		}

		// Update coefficients for next iteration: sg[j] *= alpha^j
		for j := 1; j <= numErrors; j++ {
			sg[j] = r.galois.divExp(sg[j], j)
		}

		// If sum is 0, we found a root (error position)
		if sum == 0 {
			positions = append(positions, r.galois.toExp(i))
			if len(positions) == numErrors {
				return positions
			}
		}
	}

	// Didn't find all roots - shouldn't happen for valid data
	return nil
}

// DetectErrors compares local and CTDB syndromes to detect errors.
// Returns the number of errors and their sample positions, or -1 if uncorrectable.
// localSyn and ctdbSyn are 2D syndrome matrices [stride][npar].
// stridecount is the number of data strides (calculated as (finalSampleCount - pregap) * 2 / stride - 2).
// This is a simplified version that doesn't adjust for pregap/offset.
func (r *RsDecode) DetectErrors(localSyn, ctdbSyn [][]uint16, stride, stridecount int) (errorCount int, errorPositions []int) {
	return r.DetectErrorsWithOffset(localSyn, ctdbSyn, stride, stridecount, 0, 0)
}

// DetectErrorsWithOffset compares local and CTDB syndromes to detect errors with offset adjustment.
// Returns the number of errors and their sample positions (in 16-bit samples), or -1 if uncorrectable.
// localSyn and ctdbSyn are 2D syndrome matrices [stride][npar].
// stridecount is the number of data strides.
// pregap is the pregap in 16-bit samples (pregapFrames * 588 * 2).
// actualOffset is the detected drive offset in samples (stereo), doubled internally for 16-bit calculation.
//
// Position calculation matches CueTools CDRepair.cs:212-213:
//
//	pos = galois.toPos(stridecount, _errpos[i]) * stride + part2
//	erroffi = stride + pos + pregap * 2 - actualOffset * 2
func (r *RsDecode) DetectErrorsWithOffset(localSyn, ctdbSyn [][]uint16, stride, stridecount, pregap, actualOffset int) (errorCount int, errorPositions []int) {
	if len(localSyn) == 0 || len(ctdbSyn) == 0 {
		return 0, nil
	}
	if len(localSyn) != len(ctdbSyn) || len(localSyn) != stride {
		return -1, nil
	}

	sigma := make([]int, r.npar+1)
	allPositions := make([]int, 0)

	// Process each stride row (part2 in CueTools)
	for part2 := 0; part2 < stride; part2++ {
		// XOR syndromes to get error syndrome
		errSyn := make([]int, r.npar)
		hasError := false
		for i := 0; i < r.npar && i < len(localSyn[part2]) && i < len(ctdbSyn[part2]); i++ {
			errSyn[i] = int(localSyn[part2][i] ^ ctdbSyn[part2][i])
			if errSyn[i] != 0 {
				hasError = true
			}
		}

		if !hasError {
			continue // No error in this stride row
		}

		// Find error locator polynomial
		numErr := r.CalcSigmaMBM(errSyn, sigma)
		if numErr < 0 {
			return -1, nil // Too many errors, uncorrectable
		}

		// Find error positions using ChienSearch
		// CueTools uses stridecount (NOT stridecount + npar) as the codeword length
		errPos := r.ChienSearch(sigma, stridecount, numErr)
		if errPos == nil {
			return -1, nil // Couldn't find all roots
		}

		// Convert to sample positions matching CueTools CDRepair.cs:212-213
		for _, pos := range errPos {
			// pos is a GF element, convert using toPos which computes: length - 1 - log(pos)
			// CueTools: int pos = galois.toPos(stridecount, _errpos[i]) * stride + part2;
			gfPos := r.galois.toPos(stridecount, pos)
			samplePos := gfPos*stride + part2

			// CueTools: int erroffi = stride + pos + pregap * 2 - actualOffset * 2;
			// Note: pregap here is already in 16-bit samples if passed correctly,
			// but CueTools uses pregap in stereo samples and multiplies by 2
			erroffi := stride + samplePos + pregap - actualOffset*2

			if erroffi >= 0 {
				allPositions = append(allPositions, erroffi)
			}
		}

		errorCount += numErr
	}

	return errorCount, allPositions
}

// GetAffectedSectorsCount returns the count of error positions within [min, max) sample range.
// min/max are in 16-bit samples (parity domain), matching CueTools CDRepair.GetAffectedSectorsCount.
// Positions in errorPositions are also in 16-bit samples.
func GetAffectedSectorsCount(errorPositions []int, min, max int) int {
	count := 0
	for _, pos := range errorPositions {
		if pos >= min && pos < max {
			count++
		}
	}
	return count
}

// GetAffectedSectorsCountWithBounds returns the count of error positions within bounded track range.
// This matches CueTools CDRepairFix.GetAffectedSectorsCount which applies additional bounds:
//
//	min = Math.Max(2 * min, 2 * pregap + stride - 2 * ActualOffset);
//	max = Math.Min(2 * max, 2 * finalSampleCount - laststride - 2 * ActualOffset);
//
// Parameters:
//   - errorPositions: positions in 16-bit samples
//   - trackMin, trackMax: track boundaries in 16-bit samples (already doubled from stereo samples)
//   - pregap: pregap in 16-bit samples
//   - stride, laststride: parity strides
//   - finalSampleCount: total samples (stereo)
//   - actualOffset: detected offset (stereo samples)
func GetAffectedSectorsCountWithBounds(errorPositions []int, trackMin, trackMax, pregap, stride, laststride, finalSampleCount, actualOffset int) int {
	// Apply CueTools bounds
	min := trackMin
	boundedMin := pregap + stride - actualOffset*2
	if boundedMin > min {
		min = boundedMin
	}

	max := trackMax
	boundedMax := finalSampleCount*2 - laststride - actualOffset*2
	if boundedMax < max {
		max = boundedMax
	}

	return GetAffectedSectorsCount(errorPositions, min, max)
}

// GetAffectedSectors returns error positions within [min, max), adjusted by offset.
// Returns filtered positions relative to offset for track-specific error reporting.
// All values are in 16-bit samples (parity domain).
func GetAffectedSectors(errorPositions []int, min, max, offset int) []int {
	var filtered []int
	for _, pos := range errorPositions {
		if pos >= min && pos < max {
			filtered = append(filtered, pos-offset)
		}
	}
	return filtered
}

// FormatAffectedSectorsFiltered formats error positions with offset adjustment and coalescing.
// min/max filter the positions; offset adjusts them; coalesce groups nearby errors.
// This mirrors CueTools CDRepair.GetAffectedSectors(min, max, offs, coalesce).
func FormatAffectedSectorsFiltered(errorPositions []int, min, max, offset, coalesce int) string {
	// Get filtered positions
	filtered := GetAffectedSectors(errorPositions, min, max, offset)
	if len(filtered) == 0 {
		return ""
	}

	// Sort positions
	sortInts(filtered)

	var ranges []string
	i := 0
	for i < len(filtered) {
		start := filtered[i]
		end := start

		// Group errors within coalesce threshold
		for j := i + 1; j < len(filtered); j++ {
			if filtered[j]-filtered[j-1] <= coalesce {
				end = filtered[j]
				i = j
			} else {
				break
			}
		}

		// Format the range - positions are in 16-bit samples, convert to frames
		// Each frame = 588 stereo samples = 1176 16-bit samples
		startFrame := start / 1176
		endFrame := end / 1176
		startTime := frameToTimeString(startFrame)
		if startFrame == endFrame {
			ranges = append(ranges, startTime)
		} else {
			endTime := frameToTimeString(endFrame)
			ranges = append(ranges, startTime+"-"+endTime)
		}
		i++
	}

	return joinStrings(ranges, ",")
}

// frameToTimeString converts a CD frame number to MM:SS:FF format.
// 75 frames per second.
func frameToTimeString(frame int) string {
	ff := frame % 75
	seconds := frame / 75
	ss := seconds % 60
	mm := seconds / 60
	return fmt.Sprintf("%02d:%02d:%02d", mm, ss, ff)
}

// XORSyndromes computes the XOR of two syndrome matrices.
func XORSyndromes(a, b [][]uint16) [][]uint16 {
	if len(a) != len(b) {
		return nil
	}
	result := make([][]uint16, len(a))
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return nil
		}
		result[i] = make([]uint16, len(a[i]))
		for j := range a[i] {
			result[i][j] = a[i][j] ^ b[i][j]
		}
	}
	return result
}

// IsZeroSyndrome checks if a syndrome matrix is all zeros (no errors).
func IsZeroSyndrome(syn [][]uint16) bool {
	for _, row := range syn {
		for _, v := range row {
			if v != 0 {
				return false
			}
		}
	}
	return true
}

// SampleToTimeString converts a sample offset to CD time format (MM:SS:FF).
// Input is in 16-bit samples (parity domain). Each CD frame = 1176 16-bit samples.
// 75 frames per second.
func SampleToTimeString(samples int) string {
	// Convert 16-bit samples to frames: 1 frame = 588 stereo samples = 1176 16-bit samples
	frames := samples / 1176
	ff := frames % 75
	seconds := frames / 75
	ss := seconds % 60
	mm := seconds / 60
	return fmt.Sprintf("%02d:%02d:%02d", mm, ss, ff)
}

// FormatAffectedSectors groups error positions into ranges and formats as MM:SS:FF.
// Input positions are in 16-bit samples (parity domain).
// Errors within 5 frames (5 * 1176 = 5880 16-bit samples) are grouped together.
// This is the default coalesce value from CueTools (2 * 588 * 5 = 5880).
func FormatAffectedSectors(positions []int) string {
	if len(positions) == 0 {
		return ""
	}

	// Sort positions
	sorted := make([]int, len(positions))
	copy(sorted, positions)
	sortInts(sorted)

	var ranges []string
	i := 0
	for i < len(sorted) {
		start := sorted[i]
		end := start

		// Group errors within 5 frames (CueTools default coalesce = 2 * 588 * 5)
		const groupThreshold = 5 * 588 * 2 // 5880 16-bit samples
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j]-sorted[j-1] <= groupThreshold {
				end = sorted[j]
				i = j
			} else {
				break
			}
		}

		// Format the range - positions already in 16-bit samples
		startTime := SampleToTimeString(start)
		if start == end {
			ranges = append(ranges, startTime)
		} else {
			endTime := SampleToTimeString(end)
			ranges = append(ranges, startTime+"-"+endTime)
		}
		i++
	}

	return joinStrings(ranges, ",")
}

// sortInts sorts a slice of integers in ascending order.
func sortInts(a []int) {
	for i := 0; i < len(a)-1; i++ {
		for j := i + 1; j < len(a); j++ {
			if a[j] < a[i] {
				a[i], a[j] = a[j], a[i]
			}
		}
	}
}

// joinStrings joins strings with a separator.
func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += sep + parts[i]
	}
	return result
}
