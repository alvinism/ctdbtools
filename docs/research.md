# Research Notes

## CUETools source references
- `CUETools.CDImage/CDImage.cs`: TOCID property builds hex offsets for each audio track offset from first audio start, pads to 100 tracks, SHA1 over ASCII hex string, then Base64-url encodes ('.' for '+', '_' for '/', '-' for '='). Also includes MusicBrainz TOC/ID builders for cross-check.
- `CUETools.AccurateRip/AccurateRip.cs`: core CRC accumulation for AccurateRip/CTDB. Key fields:
  - `_CRCAR`, `_CRCSM`, `_CRC32`, `_CRCWN`, `_CRCNL`, `_CRCV2` arrays store rolling CRCs/sums per track and offset window (`maxOffset` derived from stride settings).
  - `CRCWONULL`/`CRC32` adjust offsets using `Crc32.Combine` to splice segments; initial state uses `0xffffffff` xor when combining.
  - `CTDBCRC` builds CRC32 for disc/track segments allowing prefix/suffix trimming and drive offset adjustments.
  - `CalculateCRCs` iterates PCM samples (uint per stereo sample), updating AccurateRip CRC (sum of sample * position), CRC32, CRC32 without nulls, null counts, V2 CRC, peak; also emits parity/RS syndrome via `ParityToSyndrome` tables.
- `CUETools.CTDB/CUEToolsDB.cs`: CTDB verify/submit flow; uses `verify.AR.CTDBCRC(offset)` and `verify.TrackCRC` with offsets, parity upload via `ParityToSyndrome` helpers, offset search using syndromes.

## Pending deep-dives
- Map stride/maxOffset initialization in `AccurateRipVerify.Init` to mirror parity/offset handling.
- Extract AccurateRip ID/CDDB ID calculators and offset-safe CRC specifics.
- Understand `ParityToSyndrome`/`Galois16` helpers for repair flow.
