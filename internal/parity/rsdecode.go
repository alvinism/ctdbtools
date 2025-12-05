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
// n is the codeword length (stridecount + npar).
// numErrors is the expected number of errors from CalcSigmaMBM.
// Returns the error positions, or nil if not all roots were found.
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
// stridecount is the number of data strides (total strides - parity strides).
func (r *RsDecode) DetectErrors(localSyn, ctdbSyn [][]uint16, stride, stridecount int) (errorCount int, errorPositions []int) {
	if len(localSyn) == 0 || len(ctdbSyn) == 0 {
		return 0, nil
	}
	if len(localSyn) != len(ctdbSyn) || len(localSyn) != stride {
		return -1, nil
	}

	sigma := make([]int, r.npar+1)
	allPositions := make([]int, 0)

	// Process each stride row
	for part := 0; part < stride; part++ {
		// XOR syndromes to get error syndrome
		errSyn := make([]int, r.npar)
		hasError := false
		for i := 0; i < r.npar && i < len(localSyn[part]) && i < len(ctdbSyn[part]); i++ {
			errSyn[i] = int(localSyn[part][i] ^ ctdbSyn[part][i])
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

		// Find error positions
		errPos := r.ChienSearch(sigma, stridecount+r.npar, numErr)
		if errPos == nil {
			return -1, nil // Couldn't find all roots
		}

		// Convert to sample positions
		for _, pos := range errPos {
			// pos is in GF domain, convert to sample offset
			// Sample position = (stridecount + npar - 1 - log(pos)) * stride + part
			logPos := r.galois.toLog(pos)
			samplePos := (stridecount + r.npar - 1 - logPos) * stride + part
			if samplePos >= 0 {
				allPositions = append(allPositions, samplePos)
			}
		}

		errorCount += numErr
	}

	return errorCount, allPositions
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
// There are 588 samples per CD frame, 75 frames per second.
func SampleToTimeString(samples int) string {
	// samples is stereo (32-bit per sample), divide by 2 for 16-bit mono position
	frames := samples / 588
	seconds := frames / 75
	minutes := seconds / 60
	return fmt.Sprintf("%02d:%02d:%02d", minutes, seconds%60, frames%75)
}

// FormatAffectedSectors groups error positions into ranges and formats as MM:SS:FF.
// Errors within 5 sectors (5 * 588 * 2 samples) are grouped together.
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

		// Group errors within 5 sectors
		const groupThreshold = 5 * 588 * 2 // 5 sectors in stereo samples
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j]-sorted[j-1] <= groupThreshold {
				end = sorted[j]
				i = j
			} else {
				break
			}
		}

		// Format the range
		startTime := SampleToTimeString(start / 2) // Convert to mono samples for time
		if start == end {
			ranges = append(ranges, startTime)
		} else {
			endTime := SampleToTimeString(end / 2)
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
