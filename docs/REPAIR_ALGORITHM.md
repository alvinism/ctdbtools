# CD Audio Repair Algorithm

This document explains the error correction algorithm used in ctdbtools repair functionality, ported from CUETools.

## Overview: Repair vs Verify

### Verification (Detection Only)
The verify command detects and locates errors but does not correct them:
1. **Syndrome XOR**: XOR local and CTDB syndromes to get error syndrome
2. **Berlekamp-Massey**: Find error locator polynomial σ(x)
3. **Chien Search**: Find error positions (roots of σ(x))
4. **Output**: "differs in X samples @MM:SS:FF"

### Repair (Detection + Correction)
The repair command additionally computes error magnitudes and applies corrections:
1. **Detection**: Same as verify (steps 1-3)
2. **Forney Algorithm**: Compute error magnitudes using Omega polynomial
3. **Correction**: XOR computed magnitudes at error positions
4. **Output**: Corrected WAV file

## Mathematical Foundation

### Galois Field GF(2^16)

All arithmetic is performed in GF(2^16) with primitive polynomial `0x1100B`:
- **Addition**: XOR (no carry)
- **Multiplication**: Using log/exp tables for efficiency
- **Division**: `a/b = exp[log[a] - log[b] + max]`

The field has 65535 non-zero elements, represented as powers of α (primitive element).

### Reed-Solomon Code Structure

CTDB uses a systematic RS code with:
- **n**: Codeword length (stridecount)
- **npar**: Parity symbols (typically 8)
- **t = npar/2**: Maximum correctable errors per stride row (typically 4)

The generator polynomial:
```
g(x) = (x - α^0)(x - α^1)...(x - α^(npar-1))
```

### Syndrome Calculation

Syndromes are computed during audio processing using LFSR-style accumulation:
```
S_i = Σ(r_j * α^(i*j)) for j = 0 to n-1
```

Where r_j is the received codeword (XOR of local and CTDB data).

## Forney Algorithm

The Forney algorithm computes error magnitudes once positions are known.

### Error Evaluator Polynomial (Omega)

First, compute Ω(x) from syndrome and error locator:
```
Ω(x) = S(x) * σ(x) mod x^npar
```

Where:
- S(x) = S_0 + S_1*x + S_2*x^2 + ... (syndrome polynomial)
- σ(x) = 1 + σ_1*x + σ_2*x^2 + ... (error locator polynomial)

In Go:
```go
func computeOmega(syndrome, sigma []int, numErrors int) []int {
    omega := make([]int, numErrors)
    for i := 0; i < numErrors; i++ {
        omega[i] = syndrome[i]
        for j := 1; j <= i && j <= numErrors; j++ {
            omega[i] ^= galois.mul(sigma[j], syndrome[i-j])
        }
    }
    return omega
}
```

### Formal Derivative of Sigma

In GF(2^n), the derivative of a polynomial has a special form:
- Only odd-indexed coefficients survive
- Coefficient at position i becomes coefficient at position i-1

For σ(x) = 1 + σ_1*x + σ_2*x^2 + σ_3*x^3 + σ_4*x^4:
```
σ'(x) = σ_1 + 0 + σ_3*x^2 + 0 + ... = σ_1 + σ_3*x^2 + σ_5*x^4 + ...
```

In Go:
```go
func formalDerivative(sigma []int, numErrors int) []int {
    derivative := make([]int, numErrors)
    for i := 0; i < numErrors; i++ {
        if (i+1)%2 == 1 {  // odd index in original
            derivative[i] = sigma[i+1]
        } else {
            derivative[i] = 0
        }
    }
    return derivative
}
```

### Error Magnitude Calculation

For each error position X_j = α^(error_position), compute:
```
e_j = X_j × Ω(X_j^(-1)) / σ'(X_j^(-1))
```

Where:
- X_j is the error locator (GF element representing the position)
- X_j^(-1) = α^(-error_position) is the inverse of the error locator
- Ω and σ' are evaluated at X_j^(-1)

**Important**: The multiplication by X_j is essential! This matches CUETools RsDecode.cs:262:
```csharp
return galois.mul(ps, galois.div(ov, dv));
```

In Go:
```go
func CalculateErrorMagnitudes(syndrome, sigma, positions []int, numErrors int) []uint16 {
    omega := computeOmega(syndrome, sigma, numErrors)
    sigmaPrime := formalDerivative(sigma, numErrors)

    magnitudes := make([]uint16, numErrors)
    for i, pos := range positions {
        // X_j^(-1) in log domain
        xInv := galois.max - galois.toLog(pos)

        omegaVal := evaluatePolynomial(omega, xInv, numErrors)
        sigmaDerivVal := evaluatePolynomial(sigmaPrime, xInv, numErrors-1)

        // Forney formula: e_j = X_j × Ω(X_j^(-1)) / σ'(X_j^(-1))
        magnitudes[i] = uint16(galois.mul(pos, galois.div(omegaVal, sigmaDerivVal)))
    }
    return magnitudes
}
```

### Polynomial Evaluation in GF(2^16)

Evaluate polynomial at point x using Horner's method in log domain:
```go
func evaluatePolynomial(poly []int, logX int, degree int) int {
    result := 0
    for i := 0; i <= degree && i < len(poly); i++ {
        if poly[i] != 0 {
            // poly[i] * x^i in log domain
            exp := galois.toLog(poly[i]) + logX*i
            result ^= galois.toExp(exp % galois.max)
        }
    }
    return result
}
```

## Error Correction Process

### Step-by-Step

1. **Get Syndromes**: Fetch local syndrome and CTDB syndrome
2. **XOR Syndromes**: `errorSyn = localSyn ^ ctdbSyn`
3. **Berlekamp-Massey**: Find σ(x) and count errors
4. **Chien Search**: Find error positions (α^pos values)
5. **Forney**: Compute error magnitudes at each position
6. **Apply Corrections**: XOR magnitudes at positions in audio stream

### Per-Stride Row Processing

The parity data is organized in a 2D matrix [stride][npar]. Each row is processed independently:

```go
for part2 := 0; part2 < stride; part2++ {
    // Get error syndrome for this row
    errSyn := xorRow(localSyn[part2], ctdbSyn[part2])

    // Find errors in this row
    sigma := berlekampMassey(errSyn)
    positions := chienSearch(sigma, stridecount)
    magnitudes := forney(errSyn, sigma, positions)

    // Convert to sample positions and store
    for i, pos := range positions {
        samplePos := galoisToPos(pos) * stride + part2
        corrections = append(corrections, {samplePos, magnitudes[i]})
    }
}
```

### Applying Corrections

Corrections are sorted by position and applied while streaming audio:

```go
sort.Slice(corrections, func(i, j int) bool {
    return corrections[i].Position < corrections[j].Position
})

corrIdx := 0
for sampleIdx := 0; reader.hasMore(); sampleIdx++ {
    sample := reader.nextSample()  // 16-bit value

    // Check if this position needs correction
    if corrIdx < len(corrections) && corrections[corrIdx].Position == sampleIdx {
        sample ^= corrections[corrIdx].Magnitude
        corrIdx++
    }

    writer.writeSample(sample)
}
```

## Limitations

### Maximum Errors Per Stride Row

Reed-Solomon can correct up to `t = npar/2` errors per codeword. With typical npar=8:
- **Maximum correctable**: 4 errors per stride row
- **If more errors**: Berlekamp-Massey returns -1 (uncorrectable)

Since stride rows are independent, total correctable errors = 4 × stride positions.

### Position Coverage

Errors must be within the valid stridecount range:
- First stride (lead-in) excluded
- Last stride (lead-out) excluded
- Positions outside `[0, stridecount)` cannot be located

### Offset Dependency

The correct offset must be known for repair to work:
1. Find offset using CRC matching (same as verify)
2. Adjust syndrome comparison for offset
3. Convert GF positions to sample positions with offset adjustment

### Error Types

The algorithm corrects:
- **Burst errors**: Common from physical disc damage
- **Random errors**: Occasional bit flips
- **Interpolation artifacts**: From bad original rips

It cannot handle:
- **Large gaps**: Missing data beyond npar/2 per stride
- **Wrong offset**: Will produce incorrect corrections
- **Non-audio corruption**: Header damage, wrong format

## Code References

### CUETools Source Files

| File | Lines | Purpose |
|------|-------|---------|
| `CUETools.Parity/RsDecode.cs` | 239-263 | Forney algorithm implementation |
| `CUETools.Parity/RsDecode.cs` | 128-136 | CalcSigmaMBM (Berlekamp-Massey) |
| `CUETools.Parity/RsDecode.cs` | 157-200 | ChienSearch |
| `CUETools.CDRepair/CDRepair.cs` | 662-689 | Error magnitude application |
| `CUETools.Parity/Galois.cs` | * | GF(2^16) operations |

### ctdbtools Implementation

| File | Purpose |
|------|---------|
| `internal/parity/rsdecode.go` | RS decoder with Forney |
| `internal/parity/galois.go` | GF(2^16) arithmetic |
| `internal/repair/repair.go` | Repair orchestration |
| `internal/repair/apply.go` | Correction application |

## Example: Single Error Correction

Given:
- Error at sample position 12345 with unknown magnitude e
- Error syndrome S_0 = e (for single error, S_0 equals the magnitude)
- Berlekamp-Massey gives σ(x) = 1 + σ_1*x where σ_1 = X = α^12345 (the error locator)

Forney calculation:
1. Ω(x) = S_0 (just first syndrome for single error)
2. σ'(x) = σ_1 = X (derivative is just the coefficient)
3. X^(-1) = α^(-12345) = α^(65535-12345) = α^53190
4. Ω(X^(-1)) = S_0 (constant polynomial)
5. σ'(X^(-1)) = X = α^12345
6. e = X × Ω(X^(-1)) / σ'(X^(-1)) = X × S_0 / X = S_0 = magnitude

Note: For a single error, the X terms cancel out, but for multiple errors the
multiplication by X_j is essential and does not simplify.

Apply: `corrected_sample = original_sample ^ e`

## Verification After Repair

After repair, the corrected audio should:
1. Match the CTDB disc CRC exactly
2. Have zero syndrome XOR with CTDB
3. Pass AccurateRip verification (if AR data exists)

Run `ctdbtools verify` on the repaired output to confirm success.
