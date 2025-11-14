package simulator

import "container/heap"

// EventQueue is a priority queue for simulation events, ordered by timestamp
type EventQueue struct {
	events eventHeap
}

// NewEventQueue creates a new event queue
func NewEventQueue() *EventQueue {
	eq := &EventQueue{
		events: make(eventHeap, 0),
	}
	heap.Init(&eq.events)
	return eq
}

// Push adds an event to the queue
func (eq *EventQueue) Push(event Event) {
	heap.Push(&eq.events, event)
}

// Pop removes and returns the next event
func (eq *EventQueue) Pop() Event {
	if eq.IsEmpty() {
		return nil
	}
	return heap.Pop(&eq.events).(Event)
}

// Peek returns the next event without removing it
func (eq *EventQueue) Peek() Event {
	if eq.IsEmpty() {
		return nil
	}
	return eq.events[0]
}

// IsEmpty returns true if the queue is empty
func (eq *EventQueue) IsEmpty() bool {
	return eq.events.Len() == 0
}

// Len returns the number of events in the queue
func (eq *EventQueue) Len() int {
	return eq.events.Len()
}

// Clear removes all events from the queue
func (eq *EventQueue) Clear() {
	eq.events = make(eventHeap, 0)
	heap.Init(&eq.events)
}

// CountWriteEvents counts the number of WriteEvents in the queue
func (eq *EventQueue) CountWriteEvents() int {
	count := 0
	for _, event := range eq.events {
		if event.Type() == EventTypeWrite {
			count++
		}
	}
	return count
}

// FindNextFlushEvent finds the earliest FlushEvent in the queue
// Returns nil if no flush event is found
func (eq *EventQueue) FindNextFlushEvent() *FlushEvent {
	var earliestFlush *FlushEvent
	for _, event := range eq.events {
		if flushEvent, ok := event.(*FlushEvent); ok {
			if earliestFlush == nil || flushEvent.Timestamp() < earliestFlush.Timestamp() {
				earliestFlush = flushEvent
			}
		}
	}
	return earliestFlush
}

// Events returns all events in the queue (for inspection/debugging)
// Note: This returns a copy of the events slice to prevent external modification
func (eq *EventQueue) Events() []Event {
	events := make([]Event, len(eq.events))
	copy(events, eq.events)
	return events
}

// eventHeap implements heap.Interface for Event
type eventHeap []Event

func (h eventHeap) Len() int           { return len(h) }
func (h eventHeap) Less(i, j int) bool { return h[i].Timestamp() < h[j].Timestamp() }
func (h eventHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *eventHeap) Push(x interface{}) {
	*h = append(*h, x.(Event))
}

func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}
