# Progress Log

## 2024-05-27
- Initialized Go module scaffolding and directory layout for CLI/library split.
- Added planning document with phased roadmap.
- Began source reading: located TOCID computation in `CUETools.CDImage/CDImage.cs` (SHA1 over track offsets) and identified AccurateRip/CTDB verification code paths in `CUETools.AccurateRip/AccurateRip.cs` and `CUETools.CTDB/CUEToolsDB.cs` for future porting.
- Implemented TOC model and ported TOCID/CDDB/AccurateRip ID calculators into `internal/toc` (matching CUETools logic).
- Added CRC utilities (`internal/hashes`) including zlib-style `Combine`, AR CRC helpers, and a zero-offset AccurateRip track calculator scaffold with unit test; offset-aware/CTDB parity work still pending.
