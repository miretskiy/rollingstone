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
	EventTypeWALWrite
	EventTypeScheduleRead
	EventTypeReadBatch
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
	case EventTypeWALWrite:
		return "wal_write"
	case EventTypeScheduleRead:
		return "schedule_read"
	case EventTypeReadBatch:
		return "read_batch"
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
	timestamp     float64
	startTime     float64 // When the flush started
	sizeMB        float64
	bandwidthMBps float64 // Disk bandwidth reserved for this flush
}

func NewFlushEvent(timestamp, startTime, sizeMB float64) *FlushEvent {
	return &FlushEvent{
		timestamp:     timestamp,
		startTime:     startTime,
		sizeMB:        sizeMB,
		bandwidthMBps: 0, // Backward compatibility: 0 means no bandwidth tracking
	}
}

func (e *FlushEvent) Timestamp() float64     { return e.timestamp }
func (e *FlushEvent) StartTime() float64     { return e.startTime }
func (e *FlushEvent) Type() EventType        { return EventTypeFlush }
func (e *FlushEvent) SizeMB() float64        { return e.sizeMB }
func (e *FlushEvent) BandwidthMBps() float64 { return e.bandwidthMBps }
func (e *FlushEvent) SetBandwidthMBps(bw float64) { e.bandwidthMBps = bw }
func (e *FlushEvent) String() string {
	return fmt.Sprintf("Flush(t=%.3fs, size=%.2fMB)", e.timestamp, e.sizeMB)
}

// CompactionEvent represents a compaction from one level to another
type CompactionEvent struct {
	timestamp          float64
	startTime          float64 // When the compaction started
	compactionID       int     // Unique ID to look up the compaction job
	fromLevel          int
	toLevel            int
	inputSizeMB        float64
	outputSizeMB       float64
	subcompactionCount int     // Number of subcompactions (0 = single compaction, >0 = subcompactions)
	bandwidthMBps      float64 // Disk bandwidth reserved for this compaction
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
		bandwidthMBps:      0, // Backward compatibility: 0 means no bandwidth tracking
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
		bandwidthMBps:      0, // Backward compatibility: 0 means no bandwidth tracking
	}
}

func (e *CompactionEvent) Timestamp() float64          { return e.timestamp }
func (e *CompactionEvent) StartTime() float64          { return e.startTime }
func (e *CompactionEvent) Type() EventType             { return EventTypeCompaction }
func (e *CompactionEvent) CompactionID() int           { return e.compactionID }
func (e *CompactionEvent) FromLevel() int              { return e.fromLevel }
func (e *CompactionEvent) ToLevel() int                { return e.toLevel }
func (e *CompactionEvent) InputSizeMB() float64        { return e.inputSizeMB }
func (e *CompactionEvent) OutputSizeMB() float64       { return e.outputSizeMB }
func (e *CompactionEvent) SubcompactionCount() int     { return e.subcompactionCount }
func (e *CompactionEvent) BandwidthMBps() float64      { return e.bandwidthMBps }
func (e *CompactionEvent) SetBandwidthMBps(bw float64) { e.bandwidthMBps = bw }
func (e *CompactionEvent) String() string {
	if e.subcompactionCount > 0 {
		return fmt.Sprintf("Compaction(t=%.3fs, L%d->L%d, in=%.2fMB, out=%.2fMB, %d subcompactions)",
			e.timestamp, e.fromLevel, e.toLevel, e.inputSizeMB, e.outputSizeMB, e.subcompactionCount)
	}
	return fmt.Sprintf("Compaction(t=%.3fs, L%d->L%d, in=%.2fMB, out=%.2fMB)",
		e.timestamp, e.fromLevel, e.toLevel, e.inputSizeMB, e.outputSizeMB)
}

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

// WALWriteEvent represents a write-ahead log write to disk
type WALWriteEvent struct {
	timestamp     float64
	startTime     float64 // When the WAL write started
	sizeMB        float64
	bandwidthMBps float64 // Disk bandwidth reserved for this WAL write
}

func NewWALWriteEvent(timestamp, startTime, sizeMB float64) *WALWriteEvent {
	return &WALWriteEvent{
		timestamp:     timestamp,
		startTime:     startTime,
		sizeMB:        sizeMB,
		bandwidthMBps: 0, // Will be set when bandwidth is reserved
	}
}

func (e *WALWriteEvent) Timestamp() float64     { return e.timestamp }
func (e *WALWriteEvent) StartTime() float64     { return e.startTime }
func (e *WALWriteEvent) Type() EventType        { return EventTypeWALWrite }
func (e *WALWriteEvent) SizeMB() float64        { return e.sizeMB }
func (e *WALWriteEvent) BandwidthMBps() float64 { return e.bandwidthMBps }
func (e *WALWriteEvent) SetBandwidthMBps(bw float64) { e.bandwidthMBps = bw }
func (e *WALWriteEvent) String() string {
	return fmt.Sprintf("WALWrite(t=%.3fs, size=%.2fMB)", e.timestamp, e.sizeMB)
}

// ScheduleReadEvent represents a periodic event that schedules read batch processing
// This separates read scheduling from read processing, similar to ScheduleWriteEvent
type ScheduleReadEvent struct {
	timestamp float64
}

func NewScheduleReadEvent(timestamp float64) *ScheduleReadEvent {
	return &ScheduleReadEvent{
		timestamp: timestamp,
	}
}

func (e *ScheduleReadEvent) Timestamp() float64 { return e.timestamp }
func (e *ScheduleReadEvent) Type() EventType    { return EventTypeScheduleRead }
func (e *ScheduleReadEvent) String() string {
	return fmt.Sprintf("ScheduleRead(t=%.3fs)", e.timestamp)
}

// ReadBatchEvent represents an aggregate of read requests processed together
// This avoids creating thousands of individual read events
type ReadBatchEvent struct {
	timestamp           float64
	startTime           float64 // When the read batch started
	totalRequests       int     // Total number of read requests
	pointLookups        int     // Number of point lookups
	scans               int     // Number of scans
	cacheHits           int     // Number of cache hits
	bloomNegatives      int     // Number of bloom filter negatives
	bandwidthMBps       float64 // Disk bandwidth reserved for this read batch
}

func NewReadBatchEvent(timestamp, startTime float64, totalRequests, pointLookups, scans, cacheHits, bloomNegatives int) *ReadBatchEvent {
	return &ReadBatchEvent{
		timestamp:      timestamp,
		startTime:      startTime,
		totalRequests:  totalRequests,
		pointLookups:   pointLookups,
		scans:          scans,
		cacheHits:      cacheHits,
		bloomNegatives: bloomNegatives,
		bandwidthMBps:  0, // Will be set when bandwidth is reserved
	}
}

func (e *ReadBatchEvent) Timestamp() float64      { return e.timestamp }
func (e *ReadBatchEvent) StartTime() float64      { return e.startTime }
func (e *ReadBatchEvent) Type() EventType         { return EventTypeReadBatch }
func (e *ReadBatchEvent) TotalRequests() int      { return e.totalRequests }
func (e *ReadBatchEvent) PointLookups() int       { return e.pointLookups }
func (e *ReadBatchEvent) Scans() int              { return e.scans }
func (e *ReadBatchEvent) CacheHits() int          { return e.cacheHits }
func (e *ReadBatchEvent) BloomNegatives() int     { return e.bloomNegatives }
func (e *ReadBatchEvent) BandwidthMBps() float64  { return e.bandwidthMBps }
func (e *ReadBatchEvent) SetBandwidthMBps(bw float64) { e.bandwidthMBps = bw }
func (e *ReadBatchEvent) String() string {
	return fmt.Sprintf("ReadBatch(t=%.3fs, requests=%d)", e.timestamp, e.totalRequests)
}
