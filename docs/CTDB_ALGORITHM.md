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

## References

- [CUETools Database Wiki](http://cue.tools/wiki/CUETools_Database)
- [CUETools Source Code](https://github.com/gchudov/cuetools.net)
  - `CUETools.AccurateRip/AccurateRip.cs` - CRC calculation
  - `CUETools.AccurateRip/CDRepair.cs` - Offset finding algorithm
