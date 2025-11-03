<!-- edbfe324-2370-4af4-9f80-943474be5c95 90a1f1cb-a737-44ae-bd52-3dd7fd2b69ea -->
# Refactor Compaction API to Match RocksDB

## Problem

Current design has unclear responsibilities and interface leakage:

- `FindLevelToCompact` and `PickCompaction` both "find compactions" with unclear boundaries
- Interface returns implementation-specific magic values (`-2`, `-1`)
- `PickCompaction` takes a level parameter that RocksDB doesn't use
- `activeCompactions` map is managed by simulator instead of compactor
- Two separate calls needed when one should suffice

## Solution

Refactor to match RocksDB's simpler design:

- Eliminate `FindLevelToCompact` method
- `PickCompaction` takes no level parameter (matches RocksDB)
- `PickCompaction` does fast checks first, returns `nil` if no compaction needed
- Compactor manages `activeCompactions` map internally
- `ExecuteCompaction` clears internal tracking when done

## Changes

### 1. Update Compactor Interface (`simulator/compactor.go`)

- Remove `FindLevelToCompact` method
- Change `PickCompaction` signature: remove `level` parameter, add `activeCompactions` tracking internally
- Update signature: `PickCompaction(lsm *LSMTree, config SimConfig) *CompactionJob`
- Add internal `activeCompactions` map to both `LeveledCompactor` and `UniversalCompactor`
- `ExecuteCompaction` clears the compaction from internal tracking when called

### 2. Update LeveledCompactor (`simulator/compactor.go`)

- Remove `FindLevelToCompact` implementation
- Add `activeCompactions map[int]bool` field
- Move fast checks (L0 score, target level contention, thresholds) to start of `PickCompaction`
- Remove level parameter, pick best level internally (highest score, eligible)
- Update `ExecuteCompaction` to call internal clear method

### 3. Update UniversalCompactor (`simulator/compactor.go`)

- Remove `FindLevelToCompact` implementation  
- Add `activeCompactions map[int]bool` field
- Move fast checks (L0 already compacting, L0 score, sorted runs count) to start of `PickCompaction`
- Remove level parameter (already ignored)
- Update `ExecuteCompaction` to call internal clear method

### 4. Update Simulator (`simulator/simulator.go`)

- Remove `s.activeCompactions` map (moved to compactor)
- Simplify `tryScheduleCompaction()`: single call to `PickCompaction()`
- Remove `CompactionScheduleResult` struct and constants
- Update `processCompaction()`: remove `delete(s.activeCompactions, ...)` call
- Update all places that reference `s.activeCompactions`

### 5. Update Tests (`simulator/compactor_test.go`, `simulator/simulator_test.go`)

- Review all `FindLevelToCompact` tests - they test important logic (L0 already compacting checks, thresholds, sorted runs count, etc.)
- Migrate `FindLevelToCompact` tests to test `PickCompaction` instead (same logic, different method)
- Update `PickCompaction` tests to not pass level parameter
- Ensure all fast-path checks (early nil returns) are still covered
- Update tests that check `activeCompactions` to use compactor's internal state (or test via observable behavior)

### 6. Remove Unused Code

- Remove `CompactionScheduleResult` struct
- Remove `LevelToCompactNoCompaction` and `LevelToCompactUniversalChoice` constants

### To-dos

- [ ] Phase 1: Setup Next.js + TypeScript project with Tailwind
- [ ] Phase 1: Build core simulation engine (event loop, memtable, flush logic)
- [ ] Phase 1: Create UI with playback controls and L0 visualization
- [ ] Phase 2: Implement L0→L1 compaction with reduction factor
- [ ] Phase 2: Add 2-level visualization and write amplification metrics
- [ ] Phase 3: Implement full multi-level LSM tree and compaction
- [ ] Phase 4: Add read simulation and read amplification tracking
- [ ] Phase 5: Implement IO profiles and performance modeling
- [ ] Phase 6: Add advanced configurations and UI polish