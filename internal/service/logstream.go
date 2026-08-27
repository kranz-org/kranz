package service

import (
	"strings"
	"sync"
	"time"

	"github.com/kranz-org/kranz/internal/config"
)

// logStream is one addressable log history: the bounded line buffer plus the
// metadata rings written in lockstep with it. Services and actions both keep
// one, so `kranz logs api` and `kranz logs analytics/stats` read the same
// structure through the same filters rather than two parallel implementations.
type logStream struct {
	lines   []string
	catalog *RunCatalog
	target  RunTarget

	mu        sync.RWMutex
	times     []time.Time
	sources   []string
	sequences []uint64
	// runs records which execution produced each line. A service produces one
	// continuous stream and leaves this zero; an action restarts the numbering
	// story on every invocation, which is what --run addresses.
	runs          []uint32
	lineBytes     []uint64
	write         int
	count         int
	retainedBytes uint64
	maxBytes      uint64
	nextSeq       uint64
	currentRun    uint32
}

func newLogStream(size int) *logStream {
	return newLogStreamWithLimits(size, defaultLogBufferBytes)
}

func newLogStreamWithLimits(size int, maxBytes uint64) *logStream {
	if size <= 0 {
		size = defaultLogBufferSize
	}
	if maxBytes == 0 {
		maxBytes = defaultLogBufferBytes
	}
	return &logStream{
		lines:     make([]string, size),
		times:     make([]time.Time, size),
		sources:   make([]string, size),
		sequences: make([]uint64, size),
		runs:      make([]uint32, size),
		lineBytes: make([]uint64, size),
		maxBytes:  maxBytes,
	}
}

func (s *logStream) SetCatalog(catalog *RunCatalog, target RunTarget) {
	s.mu.Lock()
	s.catalog = catalog
	s.target = target
	if catalog != nil {
		catalog.SetOutputLimits(target, len(s.lines), s.maxBytes)
	}
	s.mu.Unlock()
}

// BeginRun opens a new numbered execution and returns its number. Numbering is
// monotonic for the life of the stream, so a run number stays a stable address
// even after older runs age out of the buffer.
func (s *logStream) BeginRun() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentRun++
	return s.currentRun
}

// BeginRunNumber aligns the stream with the ActionRunner-owned run identity.
// It never moves backwards, so clearing or replacing a log buffer cannot make
// an old address refer to a new execution.
func (s *logStream) BeginRunNumber(run uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run > s.currentRun {
		s.currentRun = run
	}
}

// LastRun returns the most recent run number, or zero when nothing has run.
func (s *logStream) LastRun() uint32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentRun
}

// Append records text with the time Kranz received it, one entry per line. A
// pipe read is not line-aligned, so what arrives here can be fourteen lines in
// one string; storing that as a single entry is what made every consumer that
// counts entries — a tail, a search hit count, an "N lines omitted" cap —
// report a number the user could not reproduce by counting what they saw.
func (s *logStream) Append(timestamp time.Time, source, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if source == "" {
		source = "unknown"
	}
	for _, line := range splitCapturedLines(text) {
		s.appendLineLocked(timestamp, source, line)
	}
}

func (s *logStream) appendLineLocked(timestamp time.Time, source, line string) {
	if s.count == len(s.times) {
		s.evictOldestLocked()
	}
	s.nextSeq++
	s.lines[s.write] = line
	s.times[s.write] = timestamp
	s.sources[s.write] = source
	s.sequences[s.write] = s.nextSeq
	s.runs[s.write] = s.currentRun
	lineBytes := uint64(len(line))
	s.lineBytes[s.write] = lineBytes
	s.catalog.RecordOutput(s.target, s.currentRun, lineBytes)
	s.retainedBytes += lineBytes
	s.write = (s.write + 1) % len(s.times)
	if s.count < len(s.times) {
		s.count++
	}
	for s.count > 0 && s.retainedBytes > s.maxBytes {
		s.evictOldestLocked()
	}
}

func (s *logStream) evictOldestLocked() {
	if s.count == 0 {
		return
	}
	index := (s.write - s.count + len(s.lines)) % len(s.lines)
	bytes := s.lineBytes[index]
	s.catalog.EvictOutput(s.target, s.runs[index], bytes)
	if bytes <= s.retainedBytes {
		s.retainedBytes -= bytes
	} else {
		s.retainedBytes = 0
	}
	s.lines[index] = ""
	s.times[index] = time.Time{}
	s.sources[index] = ""
	s.sequences[index] = 0
	s.runs[index] = 0
	s.lineBytes[index] = 0
	s.count--
}

// splitCapturedLines breaks captured text into stored lines. A trailing
// newline ends the last line rather than starting an empty one; a blank line in
// the middle is real output and is kept.
func splitCapturedLines(text string) []string {
	if !strings.Contains(text, "\n") {
		return []string{text}
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for index, line := range lines {
		lines[index] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

// Entries returns an aligned snapshot of log text and its capture metadata.
func (s *logStream) Entries() []config.LogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := s.count
	entries := make([]config.LogEntry, 0, count)
	// The metadata rings are written in lockstep with the text buffer, so one
	// index walked back from the write cursor addresses all of them. Rebuilding
	// a separate ordered copy per field invites the fields to disagree.
	for index := range count {
		metadataIndex := (s.write - count + index + len(s.times)) % len(s.times)
		entries = append(entries, config.LogEntry{
			Sequence:  s.sequences[metadataIndex],
			Timestamp: s.times[metadataIndex],
			Source:    s.sources[metadataIndex],
			Run:       s.runs[metadataIndex],
			Raw:       s.lines[metadataIndex],
		})
	}
	return entries
}

// Lines returns the buffered text without its metadata.
func (s *logStream) Lines() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lines := make([]string, 0, s.count)
	for index := range s.count {
		metadataIndex := (s.write - s.count + index + len(s.lines)) % len(s.lines)
		lines = append(lines, s.lines[metadataIndex])
	}
	return lines
}

// Clear atomically discards both log text and its metadata. The run counter
// survives: clearing is about reclaiming the buffer, and reusing run numbers
// afterwards would make an address point at two different executions.
func (s *logStream) Clear() {
	s.mu.Lock()
	clear(s.lines)
	clear(s.times)
	clear(s.sources)
	clear(s.sequences)
	clear(s.runs)
	clear(s.lineBytes)
	s.write = 0
	s.count = 0
	s.retainedBytes = 0
	catalog, target := s.catalog, s.target
	s.mu.Unlock()
	catalog.ClearOutput(target)
}

// CopyFrom preserves a logical stream across a hot reload.
func (s *logStream) CopyFrom(previous *logStream) {
	previous.mu.RLock()
	defer previous.mu.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines[:0], previous.lines...)
	s.times = append(s.times[:0], previous.times...)
	s.sources = append(s.sources[:0], previous.sources...)
	s.sequences = append(s.sequences[:0], previous.sequences...)
	s.runs = append(s.runs[:0], previous.runs...)
	s.lineBytes = append(s.lineBytes[:0], previous.lineBytes...)
	s.write = previous.write
	s.count = previous.count
	s.retainedBytes = previous.retainedBytes
	s.maxBytes = previous.maxBytes
	s.nextSeq = previous.nextSeq
	s.currentRun = previous.currentRun
}

// defaultLogBufferSize bounds one stream's history when no size is configured.
const defaultLogBufferSize = 1000

// defaultLogBufferBytes independently bounds retained output for one target.
const defaultLogBufferBytes = 4 * 1024 * 1024
