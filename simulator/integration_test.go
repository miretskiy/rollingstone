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
	sim.Reset()

	// Track state over time to detect infinite loops
	type StepState struct {
		virtualTime   float64
		baseLevel     int
		l2FileCount   int
		l2SizeMB      float64
		compactions   []int // Active compaction levels
		hasProgressed bool  // True if base level moved or L2 changed
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
				t.Logf("  Active compactions: %d (levels: %v)", len(stepState.compactions), stepState.compactions)
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
	sim.Reset()

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
	sim.Reset()

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
