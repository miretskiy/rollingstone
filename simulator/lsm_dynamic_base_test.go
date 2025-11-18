package simulator

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDynamicBaseLevel_MovesUpAsDataGrows tests that base level moves UP (toward L1)
// as the max level size grows, matching RocksDB's CalculateBaseBytes() algorithm
func TestDynamicBaseLevel_MovesUpAsDataGrows(t *testing.T) {
	config := DefaultConfig()
	config.LevelCompactionDynamicLevelBytes = true
	config.CompactionStyle = CompactionStyleLeveled
	config.NumLevels = 7
	config.MaxBytesForLevelBaseMB = 256 // 256 MB base
	config.LevelMultiplier = 10         // 10x multiplier

	t.Run("Small L6 - base level stays at L6", func(t *testing.T) {
		lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
		// L6 has 100 MB (small, < baseBytesMax)
		lsm.Levels[6].AddSize(100.0, 0.0)

		baseLevel := lsm.calculateDynamicBaseLevel(config)
		require.Equal(t, 6, baseLevel, "Small L6 should keep base level at L6")
	})

	t.Run("L6 = 2560 MB (10x base) - base level should move to L5", func(t *testing.T) {
		lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
		// L6 has 2560 MB (10x base = 256 MB * 10)
		// Working backwards: L6=2560, L5 target=2560/10=256, L4 target=25.6
		// L5 target (256) = baseBytesMax, so base level should be L5
		lsm.Levels[6].AddSize(2560.0, 0.0)

		baseLevel := lsm.calculateDynamicBaseLevel(config)
		require.Equal(t, 5, baseLevel, "L6 at 10x base should move base level to L5")
	})

	t.Run("L6 = 6533.8 MB (~25.5x base) - base level should move to L4", func(t *testing.T) {
		lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
		// L6 has 6533.8 MB (from server.log)
		// Working backwards:
		//   L6 = 6533.8 MB → base level 6, target = 6533.8 > 256 → move up
		//   L5 = 653.38 MB → base level 5, target = 653.38 > 256 → move up
		//   L4 = 65.338 MB → base level 4, target = 65.338 < 256 → stop
		lsm.Levels[6].AddSize(6533.8, 0.0)

		baseLevel := lsm.calculateDynamicBaseLevel(config)
		require.Equal(t, 4, baseLevel, "L6 at ~25.5x base should move base level to L4")
	})

	t.Run("L6 = 25600 MB (100x base) - base level should move to L4", func(t *testing.T) {
		lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
		// L6 has 25600 MB
		// Working backwards:
		//   L6 = 25600 → base level 6, curLevelSize = 25600 > 256 → move up
		//   L5 = 2560 → base level 5, curLevelSize = 2560 > 256 → move up
		//   L4 = 256 → base level 4, curLevelSize = 256 > 256 → FALSE, stop
		// Result: baseLevel = 4
		lsm.Levels[6].AddSize(25600.0, 0.0)

		baseLevel := lsm.calculateDynamicBaseLevel(config)
		require.Equal(t, 4, baseLevel, "L6 at 100x base should move base level to L4")
	})

	t.Run("L6 = 10000 MB (~39x base) - base level should move to L4", func(t *testing.T) {
		lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
		// L6 has 10000 MB
		// Working backwards:
		//   Start: baseLevel=6, curLevelSize=10000
		//   Iteration 1: 10000 > 256, so baseLevel=5, curLevelSize=1000
		//   Iteration 2: 1000 > 256, so baseLevel=4, curLevelSize=100
		//   Iteration 3: 100 > 256 → FALSE, stop at baseLevel=4
		// Result: baseLevel = 4
		lsm.Levels[6].AddSize(10000.0, 0.0)

		baseLevel := lsm.calculateDynamicBaseLevel(config)
		require.Equal(t, 4, baseLevel, "L6 at ~39x base should move base level to L4")
	})

	t.Run("L6 = 255999 MB (just under 1000x base) - base level should move to L3", func(t *testing.T) {
		lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
		// L6 has 255999 MB
		// Working backwards:
		//   L6 = 255999 → base level 6, curLevelSize = 255999 > 256 → move up
		//   L5 = 25599.9 → base level 5, curLevelSize = 25599.9 > 256 → move up
		//   L4 = 2559.99 → base level 4, curLevelSize = 2559.99 > 256 → move up
		//   L3 = 255.999 → base level 3, curLevelSize = 255.999 > 256 → FALSE, stop
		// Result: baseLevel = 3
		lsm.Levels[6].AddSize(255999.0, 0.0)

		baseLevel := lsm.calculateDynamicBaseLevel(config)
		require.Equal(t, 3, baseLevel, "L6 at ~1000x base should move base level to L3")
	})

	t.Run("L6 = 2559999 MB (just under 10000x base) - base level should move to L2", func(t *testing.T) {
		lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
		// L6 has 2559999 MB
		// Working backwards:
		//   L6 = 2559999 → base level 6, curLevelSize > 256 → move up
		//   L5 = 255999.9 → base level 5, curLevelSize > 256 → move up
		//   L4 = 25599.99 → base level 4, curLevelSize > 256 → move up
		//   L3 = 2559.999 → base level 3, curLevelSize > 256 → move up
		//   L2 = 255.9999 → base level 2, curLevelSize = 255.9999 > 256 → FALSE, stop
		// Result: baseLevel = 2
		lsm.Levels[6].AddSize(2559999.0, 0.0)

		baseLevel := lsm.calculateDynamicBaseLevel(config)
		require.Equal(t, 2, baseLevel, "L6 at ~10000x base should move base level to L2")
	})

	t.Run("L6 = 25600000 MB (100000x base) - base level should move to L1", func(t *testing.T) {
		lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
		// L6 has 25600000 MB (extreme case)
		// Working backwards, all levels down to L1 will be > 256
		lsm.Levels[6].AddSize(25600000.0, 0.0)

		baseLevel := lsm.calculateDynamicBaseLevel(config)
		require.Equal(t, 1, baseLevel, "Extreme L6 size should move base level to L1")
	})
}

// TestDynamicBaseLevel_MatchesCalculateLevelTargets verifies that calculateDynamicBaseLevel
// returns the same base level as calculateLevelTargets (which has the full implementation)
func TestDynamicBaseLevel_MatchesCalculateLevelTargets(t *testing.T) {
	config := DefaultConfig()
	config.LevelCompactionDynamicLevelBytes = true
	config.CompactionStyle = CompactionStyleLeveled
	config.NumLevels = 7
	config.MaxBytesForLevelBaseMB = 256
	config.LevelMultiplier = 10

	testCases := []struct {
		name         string
		l6SizeMB     float64
		expectedBase int
	}{
		{"Small L6", 100.0, 6},
		{"L6 at 10x base", 2560.0, 5},
		{"L6 at ~25.5x base", 6533.8, 4},
		{"L6 at 100x base", 25600.0, 4},
		{"L6 at 1000x base", 256000.0, 3},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
			lsm.Levels[6].AddSize(tc.l6SizeMB, 0.0)

			// Get base level from calculateDynamicBaseLevel
			baseLevel1 := lsm.calculateDynamicBaseLevel(config)

			// Get base level from calculateLevelTargets (infer from targets)
			targets := lsm.calculateLevelTargets(config)
			baseLevel2 := -1
			for i := 1; i < len(targets); i++ {
				if targets[i] > 0 {
					baseLevel2 = i
					break
				}
			}

			require.Equal(t, tc.expectedBase, baseLevel1, "calculateDynamicBaseLevel should return expected base level")
			require.Equal(t, baseLevel1, baseLevel2, "calculateDynamicBaseLevel should match calculateLevelTargets base level")
		})
	}
}
