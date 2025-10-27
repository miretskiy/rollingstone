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
