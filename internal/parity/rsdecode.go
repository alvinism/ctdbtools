// Package parity implements Reed-Solomon error detection for CTDB verification.
//
// This package provides the core algorithms for detecting and locating errors
// in CD rips by comparing local syndrome data with CTDB (CUETools Database)
// syndrome data. The key algorithms are:
//
//   - Berlekamp-Massey: Finds the error locator polynomial from syndrome data
//   - Chien Search: Finds the roots of the error locator polynomial (error positions)
//
// Together, these enable the "differs in X samples @MM:SS:FF" feature that
// reports exactly where errors are located in a CD rip.
//
// See docs/CTDB_ALGORITHM.md for detailed algorithm explanation.
package parity

import "fmt"

// RsDecode implements Reed-Solomon error detection and localization.
// Ported from CUETools.Parity.RsDecode for use with CTDB syndrome data.
//
// The decoder works in GF(2^16) (Galois Field with 65536 elements) and can
// detect up to npar/2 errors per stride row. With typical npar=8, this means
// up to 4 errors can be detected and located per stride row.
type RsDecode struct {
	galois *Galois
	npar   int
}

// NewRsDecode creates a new Reed-Solomon decoder for the given number of parity symbols.
// npar is typically 8 for CTDB, allowing detection of up to 4 errors per stride row.
func NewRsDecode(npar int) *RsDecode {
	return &RsDecode{
		galois: Galois16,
		npar:   npar,
	}
}

// CalcSigmaMBM calculates the error locator polynomial using modified Berlekamp-Massey.
// Returns number of errors (degree of σ), or -1 if uncorrectable.
// Matches CueTools RsDecode.cs calcSigmaMBM exactly.
func (r *RsDecode) CalcSigmaMBM(syndrome []int, sigma []int) int {
	// CueTools uses sg0, sg1, wk arrays and jisu0, jisu1, m counters
	sg0 := make([]int, r.npar+1)
	sg1 := make([]int, r.npar+1)
	wk := make([]int, r.npar+1)

	// CueTools initialization: sg0[1] = 1, sg1[0] = 1
	sg0[1] = 1
	sg1[0] = 1
	jisu0 := 1
	jisu1 := 0
	m := -1

	for n := 0; n < r.npar; n++ {
		// Calculate discrepancy d
		d := syndrome[n]
		for i := 1; i <= jisu1; i++ {
			d ^= r.galois.mul(sg1[i], syndrome[n-i])
		}

		if d != 0 {
			logd := r.galois.toLog(d)
			// wk[i] = sg1[i] ^ mulExp(sg0[i], logd)
			for i := 0; i <= n; i++ {
				wk[i] = sg1[i] ^ r.galois.mulExp(sg0[i], logd)
			}
			js := n - m
			if js > jisu1 {
				// sg0[i] = divExp(sg1[i], logd)
				for i := 0; i <= jisu0; i++ {
					sg0[i] = r.galois.divExp(sg1[i], logd)
				}
				m = n - jisu1
				jisu1 = js
				jisu0 = js
			}
			// sg1[i] = wk[i]
			for i := 0; i < r.npar; i++ {
				sg1[i] = wk[i]
			}
		}

		// Shift sg0 - this happens AFTER the update, which is the key difference from standard BM
		for i := jisu0; i > 0; i-- {
			sg0[i] = sg0[i-1]
		}
		sg0[0] = 0
		jisu0++
	}

	// CueTools: if sg1[jisu1] == 0, return -1
	if sg1[jisu1] == 0 {
		return -1
	}

	// Copy result to sigma
	maxCopy := r.npar/2 + 2
	if maxCopy > r.npar {
		maxCopy = r.npar
	}
	for i := 0; i < maxCopy; i++ {
		sigma[i] = sg1[i]
	}

	return jisu1
}

// ChienSearch finds error positions using Chien search algorithm.
//
// The Chien search evaluates σ(x) at all possible positions α^(-i) for i = 0 to n-1.
// When σ(α^(-i)) = 0, position i contains an error.
//
// Input:
//   - sigma: error locator polynomial from CalcSigmaMBM
//   - n: codeword length (stridecount, NOT including npar)
//   - numErrors: expected number of errors (from CalcSigmaMBM)
//
// Output:
//   - Error positions as GF elements, or nil if not all roots were found
//
// The search fails (returns nil) if:
//   - We can't find numErrors roots within the valid range [0, n)
//   - This usually means the offset is wrong, causing syndrome mismatch
//
// This implementation matches CueTools RsDecode.cs chienSearch, including the fast path
// for GF(2^16) when no sigma coefficients are zero. The fast path uses log-domain
// arithmetic and batches iterations for performance.
func (r *RsDecode) ChienSearch(sigma []int, n, numErrors int) []int {
	if numErrors == 0 {
		return []int{}
	}

	// CueTools optimization: For single error (jisu == 1), sigma[1] is the error position
	// sigma(z) = 1 + sigma[1] * z, zero at z = 1/sigma[1] = alpha^(-log(sigma[1]))
	// which means error at position log(sigma[1])
	last := sigma[1]
	if numErrors == 1 {
		if r.galois.toLog(last) >= n {
			return nil // Error position out of range
		}
		return []int{last}
	}

	// Check if any sigma coefficient is zero
	haveZeroes := false
	for j := 1; j <= numErrors; j++ {
		if sigma[j] == 0 {
			haveZeroes = true
			break
		}
	}

	positions := make([]int, numErrors)
	posIdx := numErrors - 1 // Fill positions from end (CueTools style)

	// Fast path for GF(2^16) when no zeros - uses log domain iteration
	// This matches CueTools RsDecode.cs lines 164-198
	if !haveZeroes && r.galois.max == 0xffff {
		const himax = 0x11000
		sg := make([]int, numErrors+1)
		exp := r.galois.expTbl
		log := r.galois.logTbl

		// Convert to log domain, adjusted for position n
		// sg[j] = log[sg[j]] - ((j * n) % 0xffff) + 0xffff
		for j := 1; j <= numErrors; j++ {
			sg[j] = int(log[sigma[j]]) - ((j * n) % 0xffff) + 0xffff
			sg[j] = (sg[j] & 0xffff) + (sg[j] >> 16)
		}

		i := n
		for i > 0 {
			// Calculate how many iterations we can batch
			cnt := i
			for j := 1; j <= numErrors; j++ {
				sg[j] = (sg[j] & 0xffff) + (sg[j] >> 16)
				maxIter := (himax - sg[j]) / j
				if maxIter < cnt {
					cnt = maxIter
				}
			}

			// Fast inner loop - increment sg[j] by j for cnt iterations
			i -= r.chienFast(sg, cnt, numErrors)

			// Evaluate at current position
			wk := 1
			for j := 1; j <= numErrors; j++ {
				wk ^= int(exp[sg[j]])
			}

			if wk == 0 {
				last ^= int(exp[i])
				positions[posIdx] = int(exp[i])
				posIdx--
				if posIdx == 0 {
					positions[0] = last
					if int(log[last]) >= n {
						return nil
					}
					return positions
				}
			}
		}
		return nil
	}

	// Slow path - standard Chien search (for completeness)
	sg := make([]int, numErrors+1)
	for j := 1; j <= numErrors; j++ {
		sg[j] = sigma[j]
	}

	for i := 0; i < n; i++ {
		wk := 1
		for j := 1; j <= numErrors; j++ {
			wk ^= sg[j]
		}

		for j := 1; j <= numErrors; j++ {
			sg[j] = r.galois.divExp(sg[j], j)
		}

		if wk == 0 {
			pv := r.galois.toExp(i)
			last ^= pv
			positions[posIdx] = pv
			posIdx--
			if posIdx == 0 {
				if r.galois.toLog(last) >= n {
					return nil
				}
				positions[0] = last
				return positions
			}
		}
	}

	return nil
}

// chienFast performs fast inner loop for Chien search in log domain.
// Returns the number of positions skipped (cnt - remaining).
// This matches CueTools RsDecode.chienFast.
func (r *RsDecode) chienFast(sg []int, cnt, jisu int) int {
	start := cnt
	exp := r.galois.expTbl

	switch jisu {
	case 2:
		sg1, sg2 := sg[1], sg[2]
		for cnt > 0 {
			sg1++
			sg2 += 2
			cnt--
			if (int(exp[sg1]) ^ int(exp[sg2])) == 1 {
				break
			}
		}
		sg[1], sg[2] = sg1, sg2

	case 3:
		sg1, sg2, sg3 := sg[1], sg[2], sg[3]
		for cnt > 0 {
			sg1++
			sg2 += 2
			sg3 += 3
			cnt--
			if (int(exp[sg1]) ^ int(exp[sg2]) ^ int(exp[sg3])) == 1 {
				break
			}
		}
		sg[1], sg[2], sg[3] = sg1, sg2, sg3

	case 4:
		sg1, sg2, sg3, sg4 := sg[1], sg[2], sg[3], sg[4]
		for cnt > 0 {
			sg1++
			sg2 += 2
			sg3 += 3
			sg4 += 4
			cnt--
			if (int(exp[sg1]) ^ int(exp[sg2]) ^ int(exp[sg3]) ^ int(exp[sg4])) == 1 {
				break
			}
		}
		sg[1], sg[2], sg[3], sg[4] = sg1, sg2, sg3, sg4

	default:
		// General case for jisu >= 5
		sg1, sg2, sg3, sg4, sg5 := sg[1], sg[2], sg[3], sg[4], sg[5]
		for cnt > 0 {
			sg1++
			sg2 += 2
			sg3 += 3
			sg4 += 4
			sg5 += 5
			wkhi := int(exp[sg1]) ^ int(exp[sg2]) ^ int(exp[sg3]) ^ int(exp[sg4]) ^ int(exp[sg5])
			for j := 6; j <= jisu; j++ {
				sg[j] += j
				wkhi ^= int(exp[sg[j]])
			}
			cnt--
			if wkhi == 1 {
				break
			}
		}
		sg[1], sg[2], sg[3], sg[4], sg[5] = sg1, sg2, sg3, sg4, sg5
	}

	return start - cnt
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
	// Allow syndromes to be different size - use minimum
	synLen := len(localSyn)
	if len(ctdbSyn) < synLen {
		synLen = len(ctdbSyn)
	}
	if synLen > stride {
		synLen = stride
	}

	sigma := make([]int, r.npar+1)
	errSyn := make([]int, r.npar) // Hoisted outside loop to avoid ~80,000 allocations
	allPositions := make([]int, 0)

	// Process each stride row (part2 in CueTools)
	for part2 := 0; part2 < synLen; part2++ {
		// Clear and reuse errSyn buffer
		for i := range errSyn {
			errSyn[i] = 0
		}

		// XOR syndromes to get error syndrome
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
// Handles mismatched lengths by using the minimum of the two lengths.
func XORSyndromes(a, b [][]uint16) [][]uint16 {
	if a == nil || b == nil {
		return nil
	}
	// Use minimum length of the two matrices
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	if minLen == 0 {
		return nil
	}
	result := make([][]uint16, minLen)
	for i := 0; i < minLen; i++ {
		// Use minimum row length
		rowLen := len(a[i])
		if len(b[i]) < rowLen {
			rowLen = len(b[i])
		}
		result[i] = make([]uint16, rowLen)
		for j := 0; j < rowLen; j++ {
			result[i][j] = a[i][j] ^ b[i][j]
		}
	}
	return result
}

// IsZeroSyndrome checks if a syndrome matrix is all zeros (no errors).
// Returns true for nil or empty syndromes (conservative: treat as no errors detectable).
func IsZeroSyndrome(syn [][]uint16) bool {
	if syn == nil || len(syn) == 0 {
		return false // Can't determine if zero, so return false (not zero)
	}
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

// CalculateErrorMagnitudes computes error values using Forney's algorithm.
// This is the key function that enables repair (vs just detection).
//
// Formula: e_j = Omega(X_j^-1) / Sigma'(X_j^-1)
//
// Where:
//   - X_j = α^(error_position) is the error locator
//   - Omega is the error evaluator polynomial: Ω(x) = S(x) * σ(x) mod x^npar
//   - Sigma' is the formal derivative of the error locator polynomial
//
// Parameters:
//   - syndrome: the XOR of local and CTDB syndromes (error syndrome)
//   - sigma: error locator polynomial from CalcSigmaMBM
//   - positions: error positions as GF elements from ChienSearch
//   - numErrors: number of errors (degree of sigma)
//
// Returns: error magnitudes as uint16 values to XOR with samples at positions
//
// This implementation matches CUETools.Parity/RsDecode.cs:239-263.
func (r *RsDecode) CalculateErrorMagnitudes(syndrome, sigma []int, positions []int, numErrors int) []uint16 {
	if numErrors == 0 || len(positions) == 0 {
		return nil
	}

	// Compute error evaluator polynomial Omega
	omega := r.computeOmega(syndrome, sigma, numErrors)

	// Compute formal derivative of sigma
	sigmaPrime := r.formalDerivative(sigma, numErrors)

	magnitudes := make([]uint16, numErrors)

	for i := 0; i < numErrors && i < len(positions); i++ {
		pos := positions[i]
		if pos == 0 {
			// Position 0 means error at α^0 = 1, handle specially
			// X^(-1) = 1, so just evaluate directly
			omegaVal := r.evaluatePolynomial(omega, 0, numErrors)
			sigmaDerivVal := r.evaluatePolynomial(sigmaPrime, 0, numErrors-1)
			if sigmaDerivVal != 0 {
				// Forney formula: E^i = pos * Ω(z) / σ'(z)
				// For pos=0 (which is α^0=1), multiply by 1 (no change needed in value,
				// but pos=0 in GF is actually the zero element, so this case shouldn't happen
				// for valid error positions)
				magnitudes[i] = uint16(r.galois.div(omegaVal, sigmaDerivVal))
			}
			continue
		}

		// X_j^(-1) = α^(-log(X_j)) = α^(max - log(X_j))
		// In log domain: xInvLog = max - log(pos)
		logPos := r.galois.toLog(pos)
		xInvLog := r.galois.max - logPos

		// Evaluate Omega and Sigma' at X_j^(-1)
		omegaVal := r.evaluatePolynomialAtLogX(omega, xInvLog, numErrors)
		sigmaDerivVal := r.evaluatePolynomialAtLogX(sigmaPrime, xInvLog, numErrors-1)

		if sigmaDerivVal != 0 {
			// Forney formula: E^i = pos * Ω(z) / σ'(z)
			// This matches CUETools.Parity/RsDecode.cs:262:
			// return galois.mul(ps, galois.div(ov, dv));
			magnitudes[i] = uint16(r.galois.mul(pos, r.galois.div(omegaVal, sigmaDerivVal)))
		}
	}

	return magnitudes
}

// computeOmega calculates the error evaluator polynomial: Omega(x) = S(x) * Sigma(x) mod x^npar
//
// The error evaluator polynomial is used in Forney's algorithm to compute
// error magnitudes. It's computed by multiplying the syndrome polynomial
// by the error locator polynomial, keeping only terms below x^npar.
//
// Omega[i] = S[i] + sigma[1]*S[i-1] + sigma[2]*S[i-2] + ... + sigma[i]*S[0]
func (r *RsDecode) computeOmega(syndrome, sigma []int, numErrors int) []int {
	// Omega has at most numErrors terms (indices 0 to numErrors-1)
	omega := make([]int, numErrors)

	for i := 0; i < numErrors; i++ {
		// Start with S[i]
		omega[i] = syndrome[i]

		// Add sigma[j] * S[i-j] for j = 1 to min(i, numErrors)
		for j := 1; j <= i && j <= numErrors; j++ {
			if j < len(sigma) && (i-j) < len(syndrome) {
				omega[i] ^= r.galois.mul(sigma[j], syndrome[i-j])
			}
		}
	}

	return omega
}

// formalDerivative computes Sigma'(x) - the formal derivative of the error locator.
//
// In GF(2^n), the formal derivative has a special form:
// For f(x) = a_0 + a_1*x + a_2*x^2 + a_3*x^3 + ...
// f'(x) = a_1 + 0*x + a_3*x^2 + 0*x^3 + a_5*x^4 + ...
//
// Only odd-indexed coefficients survive, shifted down by one position.
// This is because in GF(2), 2=0, so even-indexed terms vanish.
//
// For sigma = [1, s1, s2, s3, s4, ...]
// sigmaPrime = [s1, 0, s3, 0, s5, ...] = [s1, s3, s5, ...] (packed)
func (r *RsDecode) formalDerivative(sigma []int, numErrors int) []int {
	// The derivative has numErrors terms (degree numErrors-1)
	derivative := make([]int, numErrors)

	for i := 0; i < numErrors; i++ {
		// Coefficient at position i in derivative comes from
		// coefficient at position i+1 in sigma, but only if i+1 is odd
		if (i+1)%2 == 1 && (i+1) < len(sigma) {
			// i+1 is odd, so this term survives
			derivative[i] = sigma[i+1]
		} else {
			derivative[i] = 0
		}
	}

	return derivative
}

// evaluatePolynomial evaluates a polynomial at point x in GF(2^16).
// Uses standard evaluation (not log domain for the point).
func (r *RsDecode) evaluatePolynomial(poly []int, x, degree int) int {
	if degree < 0 || len(poly) == 0 {
		return 0
	}

	result := 0
	xPow := 1 // x^0 = 1

	for i := 0; i <= degree && i < len(poly); i++ {
		if poly[i] != 0 {
			result ^= r.galois.mul(poly[i], xPow)
		}
		if i < degree {
			xPow = r.galois.mul(xPow, x)
		}
	}

	return result
}

// evaluatePolynomialAtLogX evaluates a polynomial at x where logX = log_alpha(x).
// This is more efficient when x is given in log form.
//
// poly[i] * x^i = poly[i] * α^(logX * i)
// In log domain: exp[log[poly[i]] + logX * i]
func (r *RsDecode) evaluatePolynomialAtLogX(poly []int, logX, degree int) int {
	if degree < 0 || len(poly) == 0 {
		return 0
	}

	result := 0

	for i := 0; i <= degree && i < len(poly); i++ {
		if poly[i] != 0 {
			// poly[i] * x^i where x = α^logX
			// = α^(log(poly[i]) + logX * i)
			logCoef := r.galois.toLog(poly[i])
			exp := (logCoef + logX*i) % r.galois.max

			result ^= r.galois.toExp(exp)
		}
	}

	return result
}

// CalculateCorrections performs full error correction: detection + magnitude calculation.
// This is the main entry point for repair functionality.
//
// Parameters:
//   - localSyn: local syndrome matrix [stride][npar]
//   - ctdbSyn: CTDB syndrome matrix [stride][npar]
//   - stride: parity stride
//   - stridecount: number of data strides
//   - pregap: pregap in 16-bit samples
//   - actualOffset: detected drive offset (stereo samples)
//
// Returns:
//   - corrections: slice of (position, magnitude) pairs
//   - errorCount: total number of errors found
//   - error: non-nil if too many errors to correct
func (r *RsDecode) CalculateCorrections(localSyn, ctdbSyn [][]uint16, stride, stridecount, pregap, actualOffset int) (corrections []ErrorCorrection, errorCount int, err error) {
	if len(localSyn) == 0 || len(ctdbSyn) == 0 {
		return nil, 0, nil
	}

	// Use minimum length of syndromes
	synLen := len(localSyn)
	if len(ctdbSyn) < synLen {
		synLen = len(ctdbSyn)
	}
	if synLen > stride {
		synLen = stride
	}

	sigma := make([]int, r.npar+1)
	errSyn := make([]int, r.npar) // Hoisted outside loop to avoid ~80,000 allocations
	corrections = make([]ErrorCorrection, 0)

	// Process each stride row (part2 in CueTools)
	for part2 := 0; part2 < synLen; part2++ {
		// Clear and reuse errSyn buffer
		for i := range errSyn {
			errSyn[i] = 0
		}

		// XOR syndromes to get error syndrome
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
			return nil, -1, fmt.Errorf("uncorrectable errors in stride row %d: too many errors", part2)
		}

		// Find error positions using ChienSearch
		errPos := r.ChienSearch(sigma, stridecount, numErr)
		if errPos == nil {
			return nil, -1, fmt.Errorf("uncorrectable errors in stride row %d: could not locate all errors", part2)
		}

		// Calculate error magnitudes using Forney
		magnitudes := r.CalculateErrorMagnitudes(errSyn, sigma, errPos, numErr)
		if magnitudes == nil {
			return nil, -1, fmt.Errorf("failed to calculate error magnitudes in stride row %d", part2)
		}

		// Convert to sample positions and store corrections
		for i, pos := range errPos {
			// Convert GF element to position: length - 1 - log(pos)
			gfPos := r.galois.toPos(stridecount, pos)
			samplePos := gfPos*stride + part2

			// Apply offset and pregap adjustment (CueTools CDRepair.cs:212-213)
			// erroffi = stride + pos + pregap * 2 - actualOffset * 2
			erroffi := stride + samplePos + pregap - actualOffset*2

			if erroffi >= 0 && magnitudes[i] != 0 {
				corrections = append(corrections, ErrorCorrection{
					Position:  erroffi,
					Magnitude: magnitudes[i],
				})
			}
		}

		errorCount += numErr
	}

	return corrections, errorCount, nil
}

// ErrorCorrection represents a single error correction: position and XOR magnitude.
type ErrorCorrection struct {
	Position  int    // Position in 16-bit samples
	Magnitude uint16 // XOR value to apply
}
