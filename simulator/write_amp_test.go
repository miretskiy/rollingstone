package simulator

import (
	"fmt"
	"testing"
)

// TestWriteAmplificationTracking tests that write amplification is correctly calculated
// across flushes and compactions
//
// RocksDB calculates WA as: (bytes written to disk) / (bytes written by user)
// This should match our simulation's calculation in metrics.go
func TestWriteAmplificationTracking(t *testing.T) {
	config := SimConfig{
		WriteRateMBps:                    10.0,
		MemtableFlushSizeMB:              64,
		MaxWriteBufferNumber:             2,
		L0CompactionTrigger:              4,
		MaxBytesForLevelBaseMB:           256,
		LevelMultiplier:                  10,
		TargetFileSizeMB:                 64,
		TargetFileSizeMultiplier:         2,
		CompactionReductionFactor:        0.9,
		MaxBackgroundJobs:                2,
		MaxSubcompactions:                1,
		IOLatencyMs:                      5.0,
		IOThroughputMBps:                 500.0,
		NumLevels:                        3,
		LevelCompactionDynamicLevelBytes: false,
	}

	sim, err := NewSimulator(config)
	if err != nil {
		t.Fatalf("Failed to create simulator: %v", err)
	}

	// Record some user writes
	userWrittenMB := 200.0
	sim.metrics.RecordUserWrite(userWrittenMB)

	t.Run("WA after flush only", func(t *testing.T) {
		// Simulate a flush: 200MB user data → 200MB disk write
		sim.metrics.RecordFlush(200.0, 0.0, 1.0)

		// WA should be 1.0 (no amplification yet, just flush)
		if sim.metrics.WriteAmplification != 1.0 {
			t.Errorf("After flush: expected WA=1.0, got %.2f", sim.metrics.WriteAmplification)
		}
	})

	t.Run("WA after compaction", func(t *testing.T) {
		// Simulate L0→L1 compaction with reduction factor
		// Input: 200MB, Output: 180MB (10% reduction)
		sim.metrics.RecordCompaction(200.0, 180.0, 1.0, 2.0, 0, 10, 8)

		// Total disk writes = 200 (flush) + 180 (compaction) = 380MB
		// WA = 380 / 200 = 1.9
		expectedWA := 380.0 / 200.0
		if sim.metrics.WriteAmplification != expectedWA {
			t.Errorf("After compaction: expected WA=%.2f, got %.2f", expectedWA, sim.metrics.WriteAmplification)
		}
	})
}

// TestMultiRoundCompactionWA tests write amplification across multiple compaction rounds
func TestMultiRoundCompactionWA(t *testing.T) {
	config := DefaultConfig()
	config.NumLevels = 3

	sim, err := NewSimulator(config)
	if err != nil {
		t.Fatalf("Failed to create simulator: %v", err)
	}

	// User writes 100MB
	sim.metrics.RecordUserWrite(100.0)

	// Flush to L0: 100MB
	sim.metrics.RecordFlush(100.0, 0.0, 1.0)

	// Current WA = 100 / 100 = 1.0
	if sim.metrics.WriteAmplification != 1.0 {
		t.Errorf("After flush: expected WA=1.0, got %.2f", sim.metrics.WriteAmplification)
	}

	// L0→L1 compaction: 100MB → 90MB (10% reduction)
	sim.metrics.RecordCompaction(100.0, 90.0, 1.0, 2.0, 0, 5, 4)

	// Total disk writes = 100 (flush) + 90 (L0→L1) = 190MB
	// WA = 190 / 100 = 1.9
	expectedWA1 := 190.0 / 100.0
	if sim.metrics.WriteAmplification != expectedWA1 {
		t.Errorf("After L0→L1: expected WA=%.2f, got %.2f", expectedWA1, sim.metrics.WriteAmplification)
	}

	// L1→L2 compaction: 90MB → 89.1MB (1% reduction for deeper levels)
	sim.metrics.RecordCompaction(90.0, 89.1, 2.0, 3.0, 1, 4, 3)

	// Total disk writes = 100 + 90 + 89.1 = 279.1MB
	// WA = 279.1 / 100 = 2.791
	expectedWA2 := 279.1 / 100.0
	tolerance := 0.01
	if sim.metrics.WriteAmplification < expectedWA2-tolerance || sim.metrics.WriteAmplification > expectedWA2+tolerance {
		t.Errorf("After L1→L2: expected WA≈%.2f, got %.2f", expectedWA2, sim.metrics.WriteAmplification)
	}
}

// TestReductionFactorApplication tests that reduction factor is correctly applied
func TestReductionFactorApplication(t *testing.T) {
	tests := []struct {
		name            string
		fromLevel       int
		toLevel         int
		inputMB         float64
		outputMB        float64
		reductionFactor float64
	}{
		{
			name:            "L0→L1 with 10% reduction",
			fromLevel:       0,
			toLevel:         1,
			inputMB:         100.0,
			outputMB:        90.0, // 10% reduction
			reductionFactor: 0.9,
		},
		{
			name:            "L1→L2 with 1% reduction",
			fromLevel:       1,
			toLevel:         2,
			inputMB:         90.0,
			outputMB:        89.1, // 1% reduction
			reductionFactor: 0.99,
		},
		{
			name:            "L2→L3 with 1% reduction",
			fromLevel:       2,
			toLevel:         3,
			inputMB:         89.1,
			outputMB:        88.2, // 1% reduction
			reductionFactor: 0.99,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultConfig()
			config.CompactionReductionFactor = tt.reductionFactor

			lsm := NewLSMTree(config.NumLevels, float64(config.MemtableFlushSizeMB))
			compactor := NewLeveledCompactor(0)

			// Create source files
			for i := 0; i < 2; i++ {
				lsm.Levels[tt.fromLevel].AddSize(tt.inputMB/2, 1.0)
			}

			// Add a small target file to ensure we do a real compaction (not trivial move)
			targetFile := &SSTFile{ID: fmt.Sprintf("target-L%d", tt.toLevel), SizeMB: 5.0, CreatedAt: 0.5}
			lsm.Levels[tt.toLevel].Files = append(lsm.Levels[tt.toLevel].Files, targetFile)

			job := &CompactionJob{
				FromLevel:   tt.fromLevel,
				ToLevel:     tt.toLevel,
				SourceFiles: lsm.Levels[tt.fromLevel].Files,
				TargetFiles: []*SSTFile{targetFile},
			}

		inputSize, outputSize, _ := compactor.ExecuteCompaction(job, lsm, config, 10.0)

		// Verify input size (source files + target file)
			expectedInput := tt.inputMB + 5.0
			if inputSize != expectedInput {
				t.Errorf("Expected input size %.1f MB, got %.1f MB", expectedInput, inputSize)
			}

			// Verify output size matches expected reduction
			expectedOutput := expectedInput * tt.reductionFactor
			if outputSize != expectedOutput {
				t.Errorf("Expected output size %.1f MB (%.0f%% of input), got %.1f MB",
					expectedOutput, tt.reductionFactor*100, outputSize)
			}
		})
	}
}

// TestWriteAmplificationBounds tests that WA stays within reasonable bounds
func TestWriteAmplificationBounds(t *testing.T) {
	config := DefaultConfig()
	config.NumLevels = 7

	sim, err := NewSimulator(config)
	if err != nil {
		t.Fatalf("Failed to create simulator: %v", err)
	}

	// Simulate a realistic workload: 1GB of user writes
	userWrittenMB := 1000.0
	sim.metrics.RecordUserWrite(userWrittenMB)

	// Flush: 1000MB
	sim.metrics.RecordFlush(userWrittenMB, 0.0, 1.0)

	// L0→L1: 1000MB → 900MB
	sim.metrics.RecordCompaction(1000.0, 900.0, 1.0, 2.0, 0, 20, 18)

	// L1→L2: 900MB → 891MB
	sim.metrics.RecordCompaction(900.0, 891.0, 2.0, 3.0, 1, 18, 16)

	// Total disk writes = 1000 + 900 + 891 = 2791MB
	// WA = 2791 / 1000 = 2.791

	t.Run("WA should be >= 1.0", func(t *testing.T) {
		if sim.metrics.WriteAmplification < 1.0 {
			t.Errorf("WA should never be < 1.0, got %.2f", sim.metrics.WriteAmplification)
		}
	})

	t.Run("WA should be reasonable (< 10.0 for this workload)", func(t *testing.T) {
		// For a well-configured LSM with good reduction factor,
		// WA shouldn't exceed ~10x for this workload
		maxReasonableWA := 10.0
		if sim.metrics.WriteAmplification > maxReasonableWA {
			t.Errorf("WA seems unreasonably high: %.2f > %.2f", sim.metrics.WriteAmplification, maxReasonableWA)
		}
	})

	t.Run("WA increases with more compaction rounds", func(t *testing.T) {
		waBefore := sim.metrics.WriteAmplification

		// Add another compaction round
		sim.metrics.RecordCompaction(891.0, 882.0, 3.0, 4.0, 2, 16, 14)

		if sim.metrics.WriteAmplification <= waBefore {
			t.Errorf("WA should increase after more compactions: before=%.2f, after=%.2f",
				waBefore, sim.metrics.WriteAmplification)
		}
	})
}
