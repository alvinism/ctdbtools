# Progress Log

## 2024-05-27
- Initialized Go module scaffolding and directory layout for CLI/library split.
- Added planning document with phased roadmap.
- Began source reading: located TOCID computation in `CUETools.CDImage/CDImage.cs` (SHA1 over track offsets) and identified AccurateRip/CTDB verification code paths in `CUETools.AccurateRip/AccurateRip.cs` and `CUETools.CTDB/CUEToolsDB.cs` for future porting.
- Implemented TOC model and ported TOCID/CDDB/AccurateRip ID calculators into `internal/toc` (matching CUETools logic).
- Added CRC utilities (`internal/hashes`) including zlib-style `Combine`, AR CRC helpers, and a zero-offset AccurateRip track calculator scaffold with unit test; offset-aware/CTDB parity work still pending.
- Added neighbor-aware offset CRC calculators to mirror CUETools offset handling (TrackWindow with prefix/suffix) and tests for offset shifts.
- Implemented CTDB CRC computation over track windows with offset/prefix/suffix trimming plus tests; still computing directly rather than via precomputed tables but matches expected window behavior.
- Introduced parity scaffolding (`internal/parity`): GF(2^16) arithmetic (poly 0x1100b), encode table builder, and basic syndrome generator to mirror CUETools parity path; tests added.
- Ported CUETools parity Galois helpers and ParityToSyndrome conversion (syndrome <-> parity) into `internal/parity`, replacing earlier stubs.
- Added rolling CRC offset calculators and tests, plus parity aggregation scaffolding (stride-based parity state and syndromes) in `internal/accuraterip`; full lead-in/out parity handling still pending.
