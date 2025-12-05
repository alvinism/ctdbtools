# CTDB (CUETools Database) Verification Algorithm

This document explains how CTDB verification works and how this tool implements it.

## Overview

CTDB is a database that stores checksums and error correction data for CD rips. Unlike AccurateRip, CTDB is **offset-independent** - it can verify rips regardless of the CD drive's read offset.

## Key Concepts

### CD Drive Offset

Different CD drives read audio data at slightly different positions. This "read offset" is measured in samples (1 sample = 4 bytes for stereo 16-bit audio). Common offsets range from -600 to +700 samples.

When a CD is ripped:
- A drive with offset +667 reads samples shifted 667 positions forward
- A drive with offset 0 reads at the "correct" position
- A drive with offset -100 reads samples shifted 100 positions backward

### CTDB's Offset Independence

CTDB solves the offset problem by:
1. Storing CRCs computed at a reference offset (usually 0)
2. Allowing verification at any offset by searching for a matching offset
3. Using a "stride" parameter that defines the search range

### Stride and LastStride

- **Stride**: The search range for offset matching. Default is `588 * 10 * 2 = 11760` samples (10 CD frames × 2 channels)
- **LastStride**: Computed dynamically based on disc length to ensure the total verified data is a multiple of stride

The formula for laststride (from CueTools CDRepair.cs):
```
laststride = stride + ((finalSampleCount - pregap) * 2) % stride
```

### Prefix and Suffix Samples

CTDB CRC excludes samples at the beginning and end of the disc:
- **Prefix**: First `stride/2` samples (5880 samples ≈ 133ms)
- **Suffix**: Last `laststride/2` samples (varies based on disc length)

This exclusion accounts for:
1. Lead-in/lead-out areas that may contain garbage data
2. Offset differences between drives

## CRC Calculation

### Rolling CRC Tables

The tool maintains rolling CRC tables that cache CRC values at different positions:

```
CRC32[track][offset] = CRC of samples from track start to position (trackStart + offset)
```

For each track, we store:
- **Head region** (indices 0 to maxOffset): CRCs accumulated from track start
- **Tail region** (indices maxOffset to 2*maxOffset): CRCs accumulated up to track end

### CTDB Track CRC Formula

For a track, the CTDB CRC is computed as:

```
CTDBCRC = Combine(CRC_at_posA, CRC_at_posB, chunk_length)
```

Where:
- `posA` = start position (with prefix adjustment for first track)
- `posB` = end position (with suffix adjustment for last track)
- `Combine()` uses CRC32 mathematical properties to compute the CRC of the middle chunk

### Offset Adjustment

When computing CRC at offset `oi`:
```
prefixSamples += oi
suffixSamples -= oi
```

This shifts the window of samples included in the CRC calculation.

## Verification Process

### 1. Parse CUE Sheet and Audio

```
CUE Sheet → Track Layout → Audio Decoding → Sample Stream
```

### 2. Compute Rolling CRCs

As samples are processed:
- Accumulate CRC32 for each track
- Store CRCs at head and tail positions for offset lookup

### 3. Query CTDB

```
TOC String → CTDB Server → List of Entries with Track CRCs
```

### 4. Find Matching Offset

For each CTDB entry:
1. Try offset 0 first (most common case)
2. If no match, search from `1 - stride/2` to `stride/2 - 1`
3. Compare disc CRC at each offset with entry's expected CRC

```go
func findCTDBOffsetByCRC(proc, expectedCRC, stride, laststride) int {
    // Try offset 0
    if proc.DiscCTDBCRC(0, stride, laststride) == expectedCRC {
        return 0
    }

    // Search for matching offset
    for offset := 1 - stride/2; offset < stride/2; offset++ {
        if proc.DiscCTDBCRC(offset, stride, laststride) == expectedCRC {
            return offset
        }
    }
    return 0
}
```

### 5. Compare Track CRCs

Once the offset is found, compare each track's CRC:
```go
localCRC := proc.TrackCTDBCRC(track, offset, stride, laststride)
if localCRC == entry.TrackCRCs[track-1] {
    // Track matches!
}
```

## Data Flow Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                        Audio File                                │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Sample Stream (uint32)                        │
│                 [L0,R0], [L1,R1], [L2,R2], ...                   │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Rolling CRC Tables                           │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │ Track 1: CRC32[0..2*maxOffset]                              ││
│  │ Track 2: CRC32[0..2*maxOffset]                              ││
│  │ ...                                                          ││
│  │ Track N: CRC32[0..2*maxOffset]                              ││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                      CTDB Query                                  │
│  TOC: "0:24037:44250:68250:88162"                               │
│  Response: [{CRC32, TrackCRCs, Stride, Confidence}, ...]        │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Offset Search                                 │
│  For offset in [-stride/2+1, stride/2):                         │
│      if DiscCTDBCRC(offset) == entry.CRC32:                     │
│          found_offset = offset                                   │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────┐
│                   Track Verification                             │
│  For each track:                                                 │
│      localCRC = TrackCTDBCRC(track, found_offset)               │
│      if localCRC == entry.TrackCRCs[track]:                     │
│          track is verified                                       │
└─────────────────────────────────────────────────────────────────┘
```

## CRC32 Combine Function

The `Combine(crcA, crcB, len)` function computes the CRC of concatenated data:

```
CRC(A || B) = Combine(CRC(A), CRC(B), len(B))
```

This uses the mathematical property that CRC32 is a linear function over GF(2).
The implementation uses matrix exponentiation in GF(2) to efficiently combine CRCs.

## Parity-Based Error Detection

CTDB entries can include Reed-Solomon parity data that enables detection and localization of errors. This is what produces the "differs in X samples @MM:SS:FF" messages in CueTools.

### Overview

When you see output like:
```
Track 17 | (56/67) Differs in 381 samples @02:00:29-02:00:30,02:00:64-02:00:65,...
```

This means:
- 56 out of 67 CTDB entries match this track exactly
- 11 entries have differences, with the most common being 381 sample errors
- The errors are located at specific time positions (MM:SS:FF format)

### How Parity Works

#### 1. LFSR-Style Syndrome Accumulation

CueTools uses a Linear Feedback Shift Register (LFSR) style Reed-Solomon encoding during ripping. This is implemented in `AccurateRip.cs:SyndromeCalc8/16`:

```
For each audio sample:
  1. XOR the sample with the first parity word
  2. Look up encode table entries for both bytes of the XORed value
  3. Shift the parity buffer left by 1 position
  4. XOR in the encode table values at each position
```

This builds up a syndrome that represents the entire audio data in a compressed form. The key insight is that this is **systematic RS encoding** - each sample contributes to the syndrome through polynomial division in GF(2^16).

Our implementation in `internal/accuraterip/parity.go:syndromeCalc()`:
```go
func (ps *ParityState) syndromeCalc(part int, sample uint16) {
    // Get first parity word and XOR with input sample
    wr0 := uint16(ps.ParityBuf[base]) | uint16(ps.ParityBuf[base+1])<<8
    wrlo := wr0 ^ sample

    // Lookup encode table entries for both bytes
    loIdx := int(wrlo & 0xff)
    hiIdx := int(wrlo >> 8)

    // Shift left by 1 and XOR in encode table values
    for j := 0; j < ps.MaxNpar-1; j++ {
        next := ... // get next parity word
        result := next ^ ps.EncodeTab[loIdx][0][j] ^ ps.EncodeTab[hiIdx][1][j]
        // store result
    }
    // Last position gets just the table XOR (shifted in zero)
}
```

#### 2. Stride-Based Interleaving

The parity buffer is organized by "stride" - typically 11760 (10 CD frames × 588 samples × 2 channels). Each stride position maintains its own parity row, creating a 2D syndrome matrix:

```
Syndrome Matrix: [stride rows] × [npar columns]

Row 0:    [syn0,0  syn0,1  syn0,2  ... syn0,7]
Row 1:    [syn1,0  syn1,1  syn1,2  ... syn1,7]
...
Row 11759:[syn11759,0 ... syn11759,7]
```

The stridecount determines how many "data strides" are in the audio:
```
stridecount = ((finalSampleCount - pregap) * 2) / stride - 2
```

The `-2` excludes the first and last strides (lead-in/lead-out regions).

#### 3. Offset-Independent Syndrome Comparison

Different CD drives have different read offsets. To compare syndromes across offsets:

1. **Lead-in/Lead-out Buffers**: Store samples at the boundaries
2. **Syndrome Adjustment**: Use Galois field arithmetic to adjust for offset:
   ```go
   // For positions affected by offset, adjust syndrome:
   synI = g.MulExp(synI, i)
   synI ^= leadOut[...] ^ g.MulExp(leadIn[...], (i*strideCount)%g.MaxVal())
   ```

### Error Detection Algorithm

#### Step 1: Syndrome XOR

XOR the local syndrome with the CTDB syndrome:
```go
xorSyn := XORSyndromes(localSyn, ctdbSyn)
if IsZeroSyndrome(xorSyn) {
    // Perfect match - no errors
}
```

#### Step 2: Berlekamp-Massey Algorithm

Find the error locator polynomial σ(x) using the modified Berlekamp-Massey algorithm (`CalcSigmaMBM`):

```go
// Initialize
sg0[1] = 1  // B(x) = x
sg1[0] = 1  // σ(x) = 1
jisu0, jisu1 = 1, 0
m = -1

for n := 0; n < npar; n++ {
    // Calculate discrepancy
    d := syndrome[n]
    for i := 1; i <= jisu1; i++ {
        d ^= galois.mul(sg1[i], syndrome[n-i])
    }

    if d != 0 {
        // Update polynomials using CueTools method
        // Key: wk[i] = sg1[i] ^ mulExp(sg0[i], logd)
    }

    // Shift sg0 AFTER the update (key difference from standard BM)
}
```

The result `jisu1` is the number of errors detected.

#### Step 3: Chien Search

Find error positions using Chien search. For GF(2^16), CueTools has an optimized fast path:

```go
// Convert sigma to log domain
for j := 1; j <= numErrors; j++ {
    sg[j] = log[sigma[j]] - ((j * n) % 0xffff) + 0xffff
}

// Fast search using batched iterations
for i > 0 {
    // Evaluate σ(α^(-i))
    wk := 1
    for j := 1; j <= numErrors; j++ {
        wk ^= exp[sg[j]]
    }
    if wk == 0 {
        // Found a root - error at position i
        positions[posIdx] = exp[i]
    }
}
```

#### Step 4: Position Mapping

Convert GF elements to sample positions (CueTools CDRepair.cs:212-213):

```go
// pos is a GF element, convert using toPos: length - 1 - log(pos)
gfPos := galois.toPos(stridecount, pos)
samplePos := gfPos * stride + part2

// Apply offset and pregap adjustments
erroffi := stride + samplePos + pregap - actualOffset*2
```

### Offset Search for Non-Matching Entries

When an entry's CRC doesn't match at any obvious offset, but we want to try parity-based detection:

```go
// Try priority offsets first (detected offset, best offset, 0)
for _, tryOff := range offsetsToTry {
    success, errCount, errPositions := tryOffset(tryOff)
    if success {
        break
    }
}

// If still not found, search the full ±arOffsetRange
if !foundMatch {
    for searchOff := -arOffsetRange; searchOff <= arOffsetRange; searchOff++ {
        success, errCount, errPositions := tryOffset(searchOff)
        if success {
            break
        }
    }
}
```

This is necessary because CTDB entries may have been submitted by users with different drive offsets.

### Key Parameters

| Parameter | Typical Value | Description |
|-----------|---------------|-------------|
| npar | 8 | Number of parity symbols (max correctable errors = npar/2 = 4 per stride row) |
| stride | 11760 | Interleaving factor (10 CD frames × 588 × 2) |
| stridecount | varies | `((finalSampleCount - pregap) * 2) / stride - 2` |
| arOffsetRange | 2939 | Maximum offset search range (5×588 - 1 samples) |

### Output Format

Errors are formatted as CD time positions (MM:SS:FF where FF is frames, 75 per second):

```go
// Convert 16-bit sample position to frames
// 1 frame = 588 stereo samples = 1176 16-bit samples
frame := samplePos / 1176
mm := frame / 75 / 60
ss := (frame / 75) % 60
ff := frame % 75
```

Nearby errors (within 5 frames = 5880 16-bit samples) are coalesced into ranges:
```
@02:00:29-02:00:30,02:00:64-02:00:65,...
```

### Implementation Files

| File | Purpose |
|------|---------|
| `internal/accuraterip/parity.go` | LFSR-style syndrome accumulation, lead-in/out buffers |
| `internal/parity/galois.go` | GF(2^16) arithmetic operations |
| `internal/parity/rsdecode.go` | Berlekamp-Massey, Chien search, position formatting |
| `internal/parity/paritytosyndrome.go` | Syndrome ↔ parity byte conversions |
| `internal/cli/commands.go` | Orchestrates verification with offset search |

## References

- [CUETools Database Wiki](http://cue.tools/wiki/CUETools_Database)
- [CUETools Source Code](https://github.com/gchudov/cuetools.net)
  - `CUETools.AccurateRip/AccurateRip.cs` - CRC calculation
  - `CUETools.AccurateRip/CDRepair.cs` - Offset finding algorithm
  - `CUETools.Parity/RsDecode.cs` - Reed-Solomon decoder
