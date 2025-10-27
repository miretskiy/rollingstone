package simulator

import (
	"testing"
)

func TestEventQueueBasicOperations(t *testing.T) {
	q := NewEventQueue()

	t.Run("new queue is empty", func(t *testing.T) {
		if q.Len() != 0 {
			t.Errorf("Expected empty queue, got length %d", q.Len())
		}

		event := q.Pop()
		if event != nil {
			t.Error("Expected nil from empty queue")
		}
	})

	t.Run("push and pop single event", func(t *testing.T) {
		q := NewEventQueue()
		e := NewWriteEvent(10.0, 64)

		q.Push(e)
		if q.Len() != 1 {
			t.Errorf("Expected length 1, got %d", q.Len())
		}

		popped := q.Pop()
		if popped == nil {
			t.Fatal("Expected event, got nil")
		}

		if popped.Timestamp() != 10.0 {
			t.Errorf("Expected timestamp 10.0, got %.1f", popped.Timestamp())
		}

		if q.Len() != 0 {
			t.Errorf("Expected empty queue after pop, got length %d", q.Len())
		}
	})
}

func TestEventQueueOrdering(t *testing.T) {
	q := NewEventQueue()

	// Push events in non-chronological order
	events := []struct {
		timestamp float64
		sizeMB    float64
	}{
		{15.0, 64},
		{5.0, 32},
		{20.0, 128},
		{1.0, 16},
		{10.0, 64},
	}

	for _, e := range events {
		q.Push(NewWriteEvent(e.timestamp, e.sizeMB))
	}

	if q.Len() != 5 {
		t.Fatalf("Expected 5 events, got %d", q.Len())
	}

	// Events should come out in timestamp order
	expectedTimestamps := []float64{1.0, 5.0, 10.0, 15.0, 20.0}

	for i, expected := range expectedTimestamps {
		event := q.Pop()
		if event == nil {
			t.Fatalf("Expected event at position %d, got nil", i)
		}

		if event.Timestamp() != expected {
			t.Errorf("At position %d: expected timestamp %.1f, got %.1f",
				i, expected, event.Timestamp())
		}
	}

	if q.Len() != 0 {
		t.Errorf("Expected empty queue, got length %d", q.Len())
	}
}

func TestEventQueuePeek(t *testing.T) {
	q := NewEventQueue()

	t.Run("peek empty queue", func(t *testing.T) {
		event := q.Peek()
		if event != nil {
			t.Error("Expected nil from empty queue")
		}
	})

	t.Run("peek does not remove event", func(t *testing.T) {
		q := NewEventQueue()
		q.Push(NewWriteEvent(10.0, 64))
		q.Push(NewWriteEvent(5.0, 32))

		// Peek multiple times
		for i := 0; i < 3; i++ {
			event := q.Peek()
			if event == nil {
				t.Fatalf("Peek %d: expected event, got nil", i)
			}

			if event.Timestamp() != 5.0 {
				t.Errorf("Peek %d: expected timestamp 5.0, got %.1f", i, event.Timestamp())
			}

			if q.Len() != 2 {
				t.Errorf("Peek %d: expected length 2, got %d", i, q.Len())
			}
		}

		// Now pop should remove it
		popped := q.Pop()
		if popped == nil || popped.Timestamp() != 5.0 {
			t.Error("Pop after peek should return same event")
		}

		if q.Len() != 1 {
			t.Errorf("Expected length 1 after pop, got %d", q.Len())
		}
	})
}

func TestEventQueueDifferentEventTypes(t *testing.T) {
	q := NewEventQueue()

	// Push different event types
	q.Push(NewWriteEvent(10.0, 64))
	q.Push(NewFlushEvent(5.0, 64, 5.0))
	q.Push(NewCompactionEvent(15.0, 0, 1, 128, 100, 15.0))
	q.Push(NewCompactionCheckEvent(8.0))

	// Should still be ordered by timestamp
	timestamps := []float64{5.0, 8.0, 10.0, 15.0}
	eventTypes := []EventType{EventTypeFlush, EventTypeCompactionCheck, EventTypeWrite, EventTypeCompaction}

	for i := 0; i < len(timestamps); i++ {
		event := q.Pop()
		if event == nil {
			t.Fatalf("Expected event at position %d, got nil", i)
		}

		if event.Timestamp() != timestamps[i] {
			t.Errorf("Position %d: expected timestamp %.1f, got %.1f",
				i, timestamps[i], event.Timestamp())
		}

		if event.Type() != eventTypes[i] {
			t.Errorf("Position %d: expected type %s, got %s",
				i, eventTypes[i].String(), event.Type().String())
		}
	}
}

func TestEventQueueStressTest(t *testing.T) {
	q := NewEventQueue()

	// Push many events
	n := 1000
	for i := 0; i < n; i++ {
		// Mix timestamps to ensure proper sorting
		timestamp := float64((i * 7) % n)
		q.Push(NewWriteEvent(timestamp, 64))
	}

	if q.Len() != n {
		t.Fatalf("Expected %d events, got %d", n, q.Len())
	}

	// Pop all and verify order
	lastTimestamp := -1.0
	for i := 0; i < n; i++ {
		event := q.Pop()
		if event == nil {
			t.Fatalf("Expected event at position %d, got nil", i)
		}

		ts := event.Timestamp()
		if ts < lastTimestamp {
			t.Errorf("Order violation at position %d: %.1f < %.1f", i, ts, lastTimestamp)
		}
		lastTimestamp = ts
	}

	if q.Len() != 0 {
		t.Errorf("Expected empty queue, got length %d", q.Len())
	}
}

func TestEventQueueSameTimestamp(t *testing.T) {
	q := NewEventQueue()

	// Push events with same timestamp
	for i := 0; i < 5; i++ {
		q.Push(NewWriteEvent(10.0, float64(i+1)*10))
	}

	// All should have timestamp 10.0
	for i := 0; i < 5; i++ {
		event := q.Pop()
		if event == nil {
			t.Fatalf("Expected event at position %d, got nil", i)
		}

		if event.Timestamp() != 10.0 {
			t.Errorf("Position %d: expected timestamp 10.0, got %.1f", i, event.Timestamp())
		}
	}
}
