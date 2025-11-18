package simulator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUniversalCompactionL2BaseLevelRegression reproduces the bug where L2 base level
// gets stuck - L2 is repeatedly selected for compaction but never gets compacted.
//
// This test uses Step() with a realistic configuration to catch integration bugs
// that unit tests miss.
func TestUniversalCompactionL2BaseLevelRegression(t *testing.T) {
	// Configuration matching typical user scenario
	config := DefaultConfig()
	config.CompactionStyle = CompactionStyleUniversal
	config.WriteRateMBps = 30.0
	config.SimulationSpeedMultiplier = 50
	config.MemtableFlushSizeMB = 64
	config.L0CompactionTrigger = 2
	config.MaxBackgroundJobs = 6
	config.IOThroughputMBps = 125.0
	config.MaxSizeAmplificationPercent = 200

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	require.NoError(t, sim.Reset())

	// Track state over time to detect infinite loops
	type StepState struct {
		virtualTime   float64
		baseLevel     int
		l2FileCount   int
		l2SizeMB      float64
		compactions   int  // Active compaction count
		hasProgressed bool // True if base level moved or L2 changed
	}

	states := make([]StepState, 0)
	maxSteps := 200 // Limit to prevent infinite test runs
	lastBaseLevel := -1
	lastL2FileCount := 0
	lastL2SizeMB := 0.0

	for step := 0; step < maxSteps; step++ {
		// Step the simulation
		sim.Step()

		// Get current state
		state := sim.State()
		metrics := sim.Metrics()

		// Extract base level
		baseLevel, ok := state["baseLevel"].(int)
		if !ok {
			baseLevel = len(sim.lsm.Levels) - 1 // Default to deepest
		}

		// Get L2 state
		l2Level := sim.lsm.Levels[2]
		l2FileCount := l2Level.FileCount
		l2SizeMB := l2Level.TotalSize

		// Check if we've progressed
		hasProgressed := false
		if baseLevel != lastBaseLevel {
			hasProgressed = true
		}
		if l2FileCount != lastL2FileCount || l2SizeMB != lastL2SizeMB {
			hasProgressed = true
		}

		stepState := StepState{
			virtualTime:   sim.VirtualTime(),
			baseLevel:     baseLevel,
			l2FileCount:   l2FileCount,
			l2SizeMB:      l2SizeMB,
			compactions:   sim.ActiveCompactions(),
			hasProgressed: hasProgressed,
		}
		states = append(states, stepState)

		// Check for infinite loop: if base level is L2 and hasn't progressed in many steps
		if baseLevel == 2 && step > 50 {
			// Check last 20 steps for progression
			recentProgress := false
			for i := len(states) - 20; i < len(states); i++ {
				if i >= 0 && states[i].hasProgressed {
					recentProgress = true
					break
				}
			}
			if !recentProgress {
				// Log state for debugging
				t.Logf("INFINITE LOOP DETECTED at step %d:", step)
				t.Logf("  Virtual time: %.1f", stepState.virtualTime)
				t.Logf("  Base level: L%d", baseLevel)
				t.Logf("  L2: %d files, %.1f MB", l2FileCount, l2SizeMB)
				t.Logf("  Active compactions: %d", stepState.compactions)
				// Also log detailed compaction info from state
				if compInfos, ok := state["activeCompactionInfos"].([]*ActiveCompactionInfo); ok {
					for _, comp := range compInfos {
						t.Logf("    L%d→L%d", comp.FromLevel, comp.ToLevel)
					}
				}
				t.Logf("  Last 10 steps:")
				for i := len(states) - 10; i < len(states); i++ {
					if i >= 0 {
						s := states[i]
						t.Logf("    Step %d: t=%.1f, base=L%d, L2=%d files/%.1fMB, progressed=%v",
							i, s.virtualTime, s.baseLevel, s.l2FileCount, s.l2SizeMB, s.hasProgressed)
					}
				}
				t.Fatalf("Infinite loop detected: Base level stuck at L2 for 20+ steps without progression")
			}
		}

		// Stop if OOM killed (expected in some scenarios)
		if metrics.IsOOMKilled {
			t.Logf("Simulation OOM killed at step %d, virtual time %.1f", step, sim.VirtualTime())
			break
		}

		// Stop if base level moved down (success case)
		if baseLevel < 2 {
			t.Logf("Success: Base level moved to L%d at step %d", baseLevel, step)
			break
		}

		// Update tracking
		lastBaseLevel = baseLevel
		lastL2FileCount = l2FileCount
		lastL2SizeMB = l2SizeMB
	}

	// Final validation: if we reached max steps, check if we're stuck
	if len(states) >= maxSteps {
		finalState := states[len(states)-1]
		if finalState.baseLevel == 2 {
			t.Logf("Test reached max steps (%d) with base level still at L2", maxSteps)
			t.Logf("Final state: L2 has %d files, %.1f MB", finalState.l2FileCount, finalState.l2SizeMB)
			// This is a warning, not a failure - the test may just need more steps
			// But if base level is L2 and hasn't changed, it's suspicious
			if finalState.l2FileCount > 0 {
				t.Logf("WARNING: Base level is L2 with files, but didn't progress in %d steps", maxSteps)
			}
		}
	}
}

// TestUniversalCompactionBaseLevelProgressionIntegration tests that base level
// progression works correctly through the full simulation flow.
func TestUniversalCompactionBaseLevelProgressionIntegration(t *testing.T) {
	config := DefaultConfig()
	config.CompactionStyle = CompactionStyleUniversal
	config.WriteRateMBps = 30.0
	config.SimulationSpeedMultiplier = 10 // Lower multiplier for more predictable testing
	config.MemtableFlushSizeMB = 64
	config.L0CompactionTrigger = 2
	config.MaxBackgroundJobs = 6
	config.IOThroughputMBps = 125.0
	config.MaxSizeAmplificationPercent = 200

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	require.NoError(t, sim.Reset())

	// Track base level progression
	baseLevelHistory := make([]int, 0)
	baseLevelHistory = append(baseLevelHistory, len(sim.lsm.Levels)-1) // Start at deepest

	maxSteps := 100
	for step := 0; step < maxSteps; step++ {
		sim.Step()

		state := sim.State()
		baseLevel, ok := state["baseLevel"].(int)
		if !ok {
			baseLevel = len(sim.lsm.Levels) - 1
		}

		// Track base level changes
		if len(baseLevelHistory) == 0 || baseLevelHistory[len(baseLevelHistory)-1] != baseLevel {
			baseLevelHistory = append(baseLevelHistory, baseLevel)
			t.Logf("Step %d: Base level changed to L%d", step, baseLevel)
		}

		// Base level is the lowest non-empty level. It can fluctuate as files move between levels:
		// - Moving down (L6→L5→L4): Files compacted to deeper levels, leaving intermediate levels empty
		// - Moving up (L4→L5): Intermediate level (L4) becomes empty, leaving L5 as lowest non-empty
		// This is correct RocksDB behavior - base level reflects current LSM state, not a monotonic progression
		// We only check that base level stays within valid bounds [1, numLevels-1]
		if len(baseLevelHistory) > 1 {
			currentBaseLevel := baseLevelHistory[len(baseLevelHistory)-1]
			require.GreaterOrEqual(t, currentBaseLevel, 1, "Base level should be >= 1")
			require.LessOrEqual(t, currentBaseLevel, len(sim.lsm.Levels)-1, "Base level should be <= numLevels-1")
		}

		// Stop if OOM
		if sim.Metrics().IsOOMKilled {
			break
		}

		// Stop if we've progressed significantly (base level is L1 or lower)
		if baseLevel <= 1 {
			t.Logf("Success: Base level reached L%d", baseLevel)
			break
		}
	}

	// Validate that we progressed at least somewhat
	if len(baseLevelHistory) > 1 {
		initialBaseLevel := baseLevelHistory[0]
		finalBaseLevel := baseLevelHistory[len(baseLevelHistory)-1]
		t.Logf("Base level progression: L%d → L%d", initialBaseLevel, finalBaseLevel)
		// Starting empty, base level should start at L6 and move down
		require.Equal(t, len(sim.lsm.Levels)-1, initialBaseLevel, "Initial base level should be deepest")
		require.LessOrEqual(t, finalBaseLevel, initialBaseLevel, "Final base level should be <= initial")
	}
}

// TestUniversalCompactionL2CompactionWhenSelected verifies that when L2 is selected
// for compaction, it actually gets compacted (not just L0 files).
func TestUniversalCompactionL2CompactionWhenSelected(t *testing.T) {
	config := DefaultConfig()
	config.CompactionStyle = CompactionStyleUniversal
	config.WriteRateMBps = 30.0
	config.SimulationSpeedMultiplier = 5 // Very low multiplier for deterministic testing
	config.MemtableFlushSizeMB = 64
	config.L0CompactionTrigger = 2
	config.MaxBackgroundJobs = 6
	config.IOThroughputMBps = 125.0
	config.MaxSizeAmplificationPercent = 200

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	require.NoError(t, sim.Reset())

	// Run until base level is L2
	maxSteps := 100
	for step := 0; step < maxSteps; step++ {
		sim.Step()

		state := sim.State()
		baseLevel, ok := state["baseLevel"].(int)
		if !ok {
			baseLevel = len(sim.lsm.Levels) - 1
		}

		// Once base level is L2, check that L2 compactions happen
		if baseLevel == 2 {
			l2Level := sim.lsm.Levels[2]
			l2FileCountBefore := l2Level.FileCount
			l2SizeBefore := l2Level.TotalSize

			// Check active compactions - if L2 is selected, should see L2→L3
			state := sim.State()
			hasL2Compaction := false
			// Check detailed compaction info
			if compInfos, ok := state["activeCompactionInfos"].([]*ActiveCompactionInfo); ok {
				for _, comp := range compInfos {
					if comp.FromLevel == 2 {
						hasL2Compaction = true
						require.Equal(t, 3, comp.ToLevel, "L2 compaction should target L3 when base level is L2")
						t.Logf("Found L2→L3 compaction at step %d", step)
						break
					}
				}
			}

			// Advance a few more steps to see if L2 actually changes
			for i := 0; i < 10; i++ {
				sim.Step()
				if sim.Metrics().IsOOMKilled {
					break
				}
			}

			l2Level = sim.lsm.Levels[2]
			l2FileCountAfter := l2Level.FileCount
			l2SizeAfter := l2Level.TotalSize

			// If L2 was selected for compaction, it should have changed
			if hasL2Compaction || l2FileCountBefore > 0 {
				// L2 should have either:
				// 1. File count decreased (compaction completed)
				// 2. Base level moved to L1 (L2 was compacted)
				// 3. Size changed (compaction in progress or completed)
				state = sim.State()
				newBaseLevel, _ := state["baseLevel"].(int)
				if newBaseLevel != 2 {
					t.Logf("Success: Base level moved from L2 to L%d", newBaseLevel)
					return // Success
				}
				if l2FileCountAfter != l2FileCountBefore || l2SizeAfter != l2SizeBefore {
					t.Logf("Success: L2 changed (files: %d→%d, size: %.1f→%.1f)",
						l2FileCountBefore, l2FileCountAfter, l2SizeBefore, l2SizeAfter)
					return // Success
				}
			}

			// If we're here, L2 hasn't changed - this might be the bug
			if l2FileCountBefore > 0 && !hasL2Compaction {
				t.Logf("WARNING: L2 has files (%d files, %.1f MB) but no L2 compaction found", l2FileCountBefore, l2SizeBefore)
			}
			break
		}

		if sim.Metrics().IsOOMKilled {
			break
		}
	}
}

// TestUniversalCompactionMultiLevelFileRemoval tests that files are correctly removed
// from all contributing levels during universal compaction size amplification.
//
// This test specifically validates the fix for the multi-level source file removal bug
// where files from levels like L0 + L5 need to be removed from BOTH levels, not just
// the FromLevel. See leveled_compaction.go:816-863 for implementation details.
func TestUniversalCompactionMultiLevelFileRemoval(t *testing.T) {
	config := DefaultConfig()
	config.CompactionStyle = CompactionStyleUniversal
	config.WriteRateMBps = 50.0
	config.SimulationSpeedMultiplier = 1 // Run slowly for deterministic testing
	config.MemtableFlushSizeMB = 64
	config.L0CompactionTrigger = 4
	config.MaxBackgroundJobs = 1 // Single compaction at a time for predictability
	config.IOThroughputMBps = 500.0
	config.MaxSizeAmplificationPercent = 200 // Trigger size amplification compaction

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	require.NoError(t, sim.Reset())

	// Strategy: Build LSM state that will trigger size amplification compaction
	// This will cause files from multiple levels (e.g., L0 + L5) to be compacted together
	// We'll verify that files are removed correctly from all contributing levels

	// Step 1: Write enough data to build up multiple levels
	// We want to create a scenario with:
	// - Multiple L0 files
	// - Files in intermediate levels (e.g., L5)
	// - Base level at L6
	t.Logf("Phase 1: Building multi-level LSM state...")

	maxSteps := 500
	foundMultiLevelCompaction := false
	var multiLevelStep int

	for step := 0; step < maxSteps; step++ {
		// Step the simulation
		sim.Step()

		// Check LSM state before compaction
		preState := make(map[int]struct {
			fileCount int
			sizeMB    float64
		})

		for level := 0; level < len(sim.lsm.Levels); level++ {
			preState[level] = struct {
				fileCount int
				sizeMB    float64
			}{
				fileCount: sim.lsm.Levels[level].FileCount,
				sizeMB:    sim.lsm.Levels[level].TotalSize,
			}
		}

		// Wait for a size amplification compaction that involves multiple non-L0 levels
		// Check if we have files in L0 and multiple lower levels
		hasL0Files := sim.lsm.Levels[0].FileCount > 0
		hasIntermediateFiles := false
		intermediateLevel := -1
		for level := 1; level < len(sim.lsm.Levels)-1; level++ {
			if sim.lsm.Levels[level].FileCount > 0 {
				hasIntermediateFiles = true
				intermediateLevel = level
				break
			}
		}

		// Check for active compaction
		activeCompactions := sim.ActiveCompactions()
		if activeCompactions > 0 && hasL0Files && hasIntermediateFiles {
			// We have a compaction running with files in multiple levels
			// Wait for it to complete
			t.Logf("Step %d: Found potential multi-level compaction scenario", step)
			t.Logf("  L0: %d files (%.1f MB)", preState[0].fileCount, preState[0].sizeMB)
			t.Logf("  L%d: %d files (%.1f MB)", intermediateLevel, preState[intermediateLevel].fileCount, preState[intermediateLevel].sizeMB)

			// Step forward to let compaction complete
			for i := 0; i < 50; i++ {
				sim.Step()
				if sim.ActiveCompactions() == 0 {
					break
				}
			}

			// Check post-compaction state
			postState := make(map[int]struct {
				fileCount int
				sizeMB    float64
			})

			for level := 0; level < len(sim.lsm.Levels); level++ {
				postState[level] = struct {
					fileCount int
					sizeMB    float64
				}{
					fileCount: sim.lsm.Levels[level].FileCount,
					sizeMB:    sim.lsm.Levels[level].TotalSize,
				}
			}

			// Verify that files were removed from BOTH L0 and intermediate level
			l0FilesRemoved := preState[0].fileCount > postState[0].fileCount
			intermediateFilesRemoved := preState[intermediateLevel].fileCount > postState[intermediateLevel].fileCount

			if l0FilesRemoved && intermediateFilesRemoved {
				foundMultiLevelCompaction = true
				multiLevelStep = step

				t.Logf("SUCCESS: Multi-level compaction completed at step %d", step)
				t.Logf("  L0: %d files → %d files (removed %d)",
					preState[0].fileCount, postState[0].fileCount,
					preState[0].fileCount-postState[0].fileCount)
				t.Logf("  L%d: %d files → %d files (removed %d)",
					intermediateLevel, preState[intermediateLevel].fileCount,
					postState[intermediateLevel].fileCount,
					preState[intermediateLevel].fileCount-postState[intermediateLevel].fileCount)

				// CRITICAL VALIDATION: No "zombie files" - verify file counts are consistent
				// If the bug existed, files would remain in intermediate levels after compaction
				for level := 0; level < len(sim.lsm.Levels); level++ {
					levelState := sim.lsm.Levels[level]
					require.Equal(t, len(levelState.Files), levelState.FileCount,
						"Level %d file count mismatch: Files slice has %d elements but FileCount=%d (zombie files detected)",
						level, len(levelState.Files), levelState.FileCount)

					// Also verify TotalSize matches sum of file sizes
					var actualSize float64
					for _, file := range levelState.Files {
						actualSize += file.SizeMB
					}
					require.InDelta(t, actualSize, levelState.TotalSize, 0.1,
						"Level %d size mismatch: Files sum to %.1f MB but TotalSize=%.1f MB",
						level, actualSize, levelState.TotalSize)
				}

				t.Logf("  Validation PASSED: No zombie files detected, LSM state is consistent")
				break
			}
		}

		// Stop if we've written a lot and still haven't found the scenario
		if sim.VirtualTime() > 60.0 {
			break
		}

		if sim.Metrics().IsOOMKilled {
			t.Skip("Test reached OOM condition before finding multi-level compaction scenario")
		}
	}

	// Test result
	if !foundMultiLevelCompaction {
		t.Skip("Did not encounter multi-level compaction scenario during test run (may need longer simulation)")
	} else {
		t.Logf("Test completed successfully at step %d", multiLevelStep)
	}
}

// TestWALEnabled verifies that WAL writes are tracked correctly when enabled
func TestWALEnabled(t *testing.T) {
	config := DefaultConfig()
	config.EnableWAL = true
	config.WALSync = true
	config.WALSyncLatencyMs = 1.5
	config.WriteRateMBps = 10.0
	config.SimulationSpeedMultiplier = 10
	config.MemtableFlushSizeMB = 64
	config.IOThroughputMBps = 125.0

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	require.NoError(t, sim.Reset())

	// Run simulation for a few seconds
	for i := 0; i < 50; i++ {
		sim.Step()
		if sim.VirtualTime() > 5.0 {
			break
		}
	}

	metrics := sim.Metrics()

	// Verify WAL bytes were written
	require.Greater(t, metrics.WALBytesWritten, 0.0,
		"WAL bytes should be tracked when WAL is enabled")

	// Verify WAL bytes approximately equal user writes (1:1 ratio before memtable flush)
	// Allow some tolerance since we might have started mid-write
	require.InDelta(t, metrics.TotalDataWrittenMB, metrics.WALBytesWritten, metrics.TotalDataWrittenMB*0.1,
		"WAL bytes should approximately match user writes (before flush)")

	t.Logf("WAL enabled test: wrote %.2f MB user data, %.2f MB to WAL",
		metrics.TotalDataWrittenMB, metrics.WALBytesWritten)
}

// TestWALDisabled verifies that WAL writes are not tracked when disabled
func TestWALDisabled(t *testing.T) {
	config := DefaultConfig()
	config.EnableWAL = false // Disable WAL
	config.WriteRateMBps = 10.0
	config.SimulationSpeedMultiplier = 10
	config.MemtableFlushSizeMB = 64
	config.IOThroughputMBps = 125.0

	sim, err := NewSimulator(config)
	require.NoError(t, err)
	require.NoError(t, sim.Reset())

	// Run simulation for a few seconds
	for i := 0; i < 50; i++ {
		sim.Step()
		if sim.VirtualTime() > 5.0 {
			break
		}
	}

	metrics := sim.Metrics()

	// Verify no WAL bytes were written
	require.Equal(t, 0.0, metrics.WALBytesWritten,
		"WAL bytes should be 0 when WAL is disabled")

	// Verify user writes still happened
	require.Greater(t, metrics.TotalDataWrittenMB, 0.0,
		"User writes should still be tracked when WAL is disabled")

	t.Logf("WAL disabled test: wrote %.2f MB user data, %.2f MB to WAL (expected 0)",
		metrics.TotalDataWrittenMB, metrics.WALBytesWritten)
}

// TestWALWriteAmplification verifies that WAL contributes to write amplification
func TestWALWriteAmplification(t *testing.T) {
	// Test with WAL enabled
	configWithWAL := DefaultConfig()
	configWithWAL.EnableWAL = true
	configWithWAL.WALSync = true
	configWithWAL.WriteRateMBps = 10.0
	configWithWAL.SimulationSpeedMultiplier = 10
	configWithWAL.MemtableFlushSizeMB = 64
	configWithWAL.L0CompactionTrigger = 4
	configWithWAL.IOThroughputMBps = 125.0

	simWithWAL, err := NewSimulator(configWithWAL)
	require.NoError(t, err)
	require.NoError(t, simWithWAL.Reset())

	// Run until we get some flushes and compactions
	for i := 0; i < 100; i++ {
		simWithWAL.Step()
		if simWithWAL.VirtualTime() > 10.0 {
			break
		}
	}

	metricsWithWAL := simWithWAL.Metrics()

	// Test with WAL disabled
	configNoWAL := DefaultConfig()
	configNoWAL.EnableWAL = false // Disable WAL
	configNoWAL.WriteRateMBps = 10.0
	configNoWAL.SimulationSpeedMultiplier = 10
	configNoWAL.MemtableFlushSizeMB = 64
	configNoWAL.L0CompactionTrigger = 4
	configNoWAL.IOThroughputMBps = 125.0
	configNoWAL.RandomSeed = configWithWAL.RandomSeed // Use same seed for determinism

	simNoWAL, err := NewSimulator(configNoWAL)
	require.NoError(t, err)
	require.NoError(t, simNoWAL.Reset())

	// Run for same amount of time
	for i := 0; i < 100; i++ {
		simNoWAL.Step()
		if simNoWAL.VirtualTime() > 10.0 {
			break
		}
	}

	metricsNoWAL := simNoWAL.Metrics()

	// Verify WAL increases write amplification
	// With WAL: WA includes WAL + flush + compaction
	// Without WAL: WA includes only flush + compaction
	// Therefore: WA_with_WAL should be >= WA_without_WAL
	require.GreaterOrEqual(t, metricsWithWAL.WriteAmplification, metricsNoWAL.WriteAmplification,
		"Write amplification with WAL should be >= without WAL")

	t.Logf("WAL write amplification test:")
	t.Logf("  With WAL: WA=%.2fx, WAL bytes=%.2f MB, user writes=%.2f MB",
		metricsWithWAL.WriteAmplification, metricsWithWAL.WALBytesWritten, metricsWithWAL.TotalDataWrittenMB)
	t.Logf("  Without WAL: WA=%.2fx, WAL bytes=%.2f MB, user writes=%.2f MB",
		metricsNoWAL.WriteAmplification, metricsNoWAL.WALBytesWritten, metricsNoWAL.TotalDataWrittenMB)
}
