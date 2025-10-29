package simulator

import (
	"testing"
)

// TestUpdateConfig_RateZeroToNonZero verifies that changing rate from 0 to non-zero
// reschedules write events
func TestUpdateConfig_RateZeroToNonZero(t *testing.T) {
	// Create simulator with rate = 100 MB/s
	config := DefaultConfig()
	config.WriteRateMBps = 100.0

	sim, err := NewSimulator(config)
	if err != nil {
		t.Fatalf("Failed to create simulator: %v", err)
	}

	// Reset to get events scheduled
	sim.Reset()

	// Verify queue has events (should have WriteEvent + CompactionCheckEvent)
	if sim.queue.Len() < 2 {
		t.Errorf("Expected at least 2 events after reset, got %d", sim.queue.Len())
	}

	// Change rate to 0
	config.WriteRateMBps = 0.0
	err = sim.UpdateConfig(config)
	if err != nil {
		t.Fatalf("Failed to update config to rate=0: %v", err)
	}

	// Step the simulation to consume events
	// With rate=0, no new WriteEvents should be scheduled
	for i := 0; i < 10 && !sim.queue.IsEmpty(); i++ {
		sim.Step()
	}

	// Queue should only have CompactionCheckEvent (1 event)
	queueLen := sim.queue.Len()
	t.Logf("Queue length after rate=0: %d", queueLen)

	// Now change rate back to non-zero
	config.WriteRateMBps = 200.0
	err = sim.UpdateConfig(config)
	if err != nil {
		t.Fatalf("Failed to update config to rate=200: %v", err)
	}

	// Verify events were rescheduled
	// Should have WriteEvent + CompactionCheckEvent again
	newQueueLen := sim.queue.Len()
	t.Logf("Queue length after rate=200: %d", newQueueLen)

	if newQueueLen < 2 {
		t.Errorf("Expected at least 2 events after changing rate from 0 to 200, got %d", newQueueLen)
		t.Errorf("This means write events were NOT rescheduled!")
	}

	// Step once and verify WriteEvent is being processed
	sim.Step()

	// Check that memtable has grown (writes are happening)
	if sim.lsm.MemtableCurrentSize == 0 {
		t.Errorf("Expected memtable to have data after stepping with rate=200, but it's empty")
		t.Errorf("This confirms write events are not being scheduled correctly")
	}
}

// TestUpdateConfig_PauseChangeRateResume simulates the exact user scenario
func TestUpdateConfig_PauseChangeRateResume(t *testing.T) {
	// Start with rate = 100
	config := DefaultConfig()
	config.WriteRateMBps = 100.0

	sim, err := NewSimulator(config)
	if err != nil {
		t.Fatalf("Failed to create simulator: %v", err)
	}

	sim.Reset()

	// Run for a few steps
	for i := 0; i < 5; i++ {
		sim.Step()
	}

	initialWrites := sim.metrics.TotalDataWrittenMB
	t.Logf("Total writes after initial run: %.1f MB", initialWrites)

	// "Pause" - just stop stepping (in real code, server sets paused=true)
	// Change rate to 0
	config.WriteRateMBps = 0.0
	err = sim.UpdateConfig(config)
	if err != nil {
		t.Fatalf("Failed to update config to rate=0: %v", err)
	}

	// "Resume" - continue stepping
	for i := 0; i < 5; i++ {
		sim.Step()
	}

	writesAfterZero := sim.metrics.TotalDataWrittenMB
	t.Logf("Total writes after rate=0: %.1f MB", writesAfterZero)

	// Writes should have stopped (may have completed in-flight writes but no new ones)
	if writesAfterZero > initialWrites+5.0 { // Allow small epsilon for in-flight
		t.Errorf("Expected writes to stop with rate=0, but got %.1f new MB", writesAfterZero-initialWrites)
	}

	// Change rate back to non-zero while "paused"
	config.WriteRateMBps = 200.0
	err = sim.UpdateConfig(config)
	if err != nil {
		t.Fatalf("Failed to update config to rate=200: %v", err)
	}

	// "Resume" - continue stepping
	for i := 0; i < 5; i++ {
		sim.Step()
	}

	finalWrites := sim.metrics.TotalDataWrittenMB
	t.Logf("Total writes after rate=200: %.1f MB", finalWrites)

	// Writes should have resumed
	if finalWrites <= writesAfterZero {
		t.Errorf("Expected writes to resume after setting rate to 200, but they didn't")
		t.Errorf("Before: %.1f MB, After: %.1f MB", writesAfterZero, finalWrites)
	}
}
