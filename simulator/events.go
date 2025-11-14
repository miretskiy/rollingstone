package simulator

import "fmt"

// EventType represents the type of simulation event
type EventType int

const (
	EventTypeWrite EventType = iota
	EventTypeFlush
	EventTypeCompaction
	EventTypeCompactionCheck
	EventTypeScheduleWrite
)

func (et EventType) String() string {
	switch et {
	case EventTypeWrite:
		return "write"
	case EventTypeFlush:
		return "flush"
	case EventTypeCompaction:
		return "compaction"
	case EventTypeCompactionCheck:
		return "compaction_check"
	case EventTypeScheduleWrite:
		return "schedule_write"
	default:
		return "unknown"
	}
}

// Event is the base interface for all simulation events
type Event interface {
	Timestamp() float64 // Virtual time in seconds
	Type() EventType
	String() string
}

// WriteEvent represents a write operation
type WriteEvent struct {
	timestamp float64
	sizeMB    float64
	isStalled bool // true if this write is stalled (for logging)
}

func NewWriteEvent(timestamp, sizeMB float64) *WriteEvent {
	return &WriteEvent{
		timestamp: timestamp,
		sizeMB:    sizeMB,
		isStalled: false,
	}
}

// NewStalledWriteEvent creates a stalled write event that logs the stall
func NewStalledWriteEvent(timestamp, sizeMB float64) *WriteEvent {
	return &WriteEvent{
		timestamp: timestamp,
		sizeMB:    sizeMB,
		isStalled: true,
	}
}

func (e *WriteEvent) Timestamp() float64 { return e.timestamp }
func (e *WriteEvent) Type() EventType    { return EventTypeWrite }
func (e *WriteEvent) String() string {
	return fmt.Sprintf("Write(t=%.3fs, size=%.2fMB)", e.timestamp, e.sizeMB)
}
func (e *WriteEvent) SizeMB() float64 { return e.sizeMB }

// FlushEvent represents a memtable flush to L0
type FlushEvent struct {
	timestamp float64
	startTime float64 // When the flush started
	sizeMB    float64
}

func NewFlushEvent(timestamp, startTime, sizeMB float64) *FlushEvent {
	return &FlushEvent{
		timestamp: timestamp,
		startTime: startTime,
		sizeMB:    sizeMB,
	}
}

func (e *FlushEvent) Timestamp() float64 { return e.timestamp }
func (e *FlushEvent) StartTime() float64 { return e.startTime }
func (e *FlushEvent) Type() EventType    { return EventTypeFlush }
func (e *FlushEvent) String() string {
	return fmt.Sprintf("Flush(t=%.3fs, size=%.2fMB)", e.timestamp, e.sizeMB)
}
func (e *FlushEvent) SizeMB() float64 { return e.sizeMB }

// CompactionEvent represents a compaction from one level to another
type CompactionEvent struct {
	timestamp          float64
	startTime          float64 // When the compaction started
	compactionID       int     // Unique ID to look up the compaction job
	fromLevel          int
	toLevel            int
	inputSizeMB        float64
	outputSizeMB       float64
	subcompactionCount int // Number of subcompactions (0 = single compaction, >0 = subcompactions)
}

func NewCompactionEvent(timestamp, startTime float64, compactionID, fromLevel, toLevel int, inputSizeMB, outputSizeMB float64) *CompactionEvent {
	return &CompactionEvent{
		timestamp:          timestamp,
		startTime:          startTime,
		compactionID:       compactionID,
		fromLevel:          fromLevel,
		toLevel:            toLevel,
		inputSizeMB:        inputSizeMB,
		outputSizeMB:       outputSizeMB,
		subcompactionCount: 0, // Default: single compaction
	}
}

// NewCompactionEventWithSubcompactions creates a compaction event with subcompaction count
func NewCompactionEventWithSubcompactions(timestamp, startTime float64, compactionID, fromLevel, toLevel int, inputSizeMB, outputSizeMB float64, subcompactionCount int) *CompactionEvent {
	return &CompactionEvent{
		timestamp:          timestamp,
		startTime:          startTime,
		compactionID:       compactionID,
		fromLevel:          fromLevel,
		toLevel:            toLevel,
		inputSizeMB:        inputSizeMB,
		outputSizeMB:       outputSizeMB,
		subcompactionCount: subcompactionCount,
	}
}

func (e *CompactionEvent) Timestamp() float64 { return e.timestamp }
func (e *CompactionEvent) StartTime() float64 { return e.startTime }
func (e *CompactionEvent) Type() EventType    { return EventTypeCompaction }
func (e *CompactionEvent) String() string {
	if e.subcompactionCount > 0 {
		return fmt.Sprintf("Compaction(t=%.3fs, L%d->L%d, in=%.2fMB, out=%.2fMB, %d subcompactions)",
			e.timestamp, e.fromLevel, e.toLevel, e.inputSizeMB, e.outputSizeMB, e.subcompactionCount)
	}
	return fmt.Sprintf("Compaction(t=%.3fs, L%d->L%d, in=%.2fMB, out=%.2fMB)",
		e.timestamp, e.fromLevel, e.toLevel, e.inputSizeMB, e.outputSizeMB)
}
func (e *CompactionEvent) CompactionID() int       { return e.compactionID }
func (e *CompactionEvent) FromLevel() int          { return e.fromLevel }
func (e *CompactionEvent) ToLevel() int            { return e.toLevel }
func (e *CompactionEvent) InputSizeMB() float64    { return e.inputSizeMB }
func (e *CompactionEvent) OutputSizeMB() float64   { return e.outputSizeMB }
func (e *CompactionEvent) SubcompactionCount() int { return e.subcompactionCount }

// CompactionCheckEvent represents a periodic check for compactions (simulates background threads)
type CompactionCheckEvent struct {
	timestamp float64
}

func NewCompactionCheckEvent(timestamp float64) *CompactionCheckEvent {
	return &CompactionCheckEvent{
		timestamp: timestamp,
	}
}

func (e *CompactionCheckEvent) Timestamp() float64 { return e.timestamp }
func (e *CompactionCheckEvent) Type() EventType    { return EventTypeCompactionCheck }
func (e *CompactionCheckEvent) String() string {
	return fmt.Sprintf("CompactionCheck(t=%.3fs)", e.timestamp)
}

// ScheduleWriteEvent represents a periodic event that schedules new writes
// This separates write scheduling from write processing, allowing for flexible
// write arrival patterns (e.g., different distributions in the future)
type ScheduleWriteEvent struct {
	timestamp float64
}

func NewScheduleWriteEvent(timestamp float64) *ScheduleWriteEvent {
	return &ScheduleWriteEvent{
		timestamp: timestamp,
	}
}

func (e *ScheduleWriteEvent) Timestamp() float64 { return e.timestamp }
func (e *ScheduleWriteEvent) Type() EventType    { return EventTypeScheduleWrite }
func (e *ScheduleWriteEvent) String() string {
	return fmt.Sprintf("ScheduleWrite(t=%.3fs)", e.timestamp)
}
