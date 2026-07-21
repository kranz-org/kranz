// Package ringbuffer provides a concurrency-safe, fixed-capacity string buffer.
package ringbuffer

import "sync"

// RingBuffer overwrites its oldest strings when it reaches capacity.
type RingBuffer struct {
	lines    []string
	capacity int
	writeIdx int
	count    int
	mu       sync.RWMutex
}

// New creates a buffer. Non-positive capacities use the default of 1,000 lines.
func New(capacity int) *RingBuffer {
	if capacity <= 0 {
		capacity = 1000
	}
	return &RingBuffer{
		lines:    make([]string, capacity),
		capacity: capacity,
	}
}

// Write appends one string, overwriting the oldest entry when full.
func (rb *RingBuffer) Write(line string) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.lines[rb.writeIdx] = line
	rb.writeIdx = (rb.writeIdx + 1) % rb.capacity
	if rb.count < rb.capacity {
		rb.count++
	}
}

// WriteMulti appends several strings while acquiring the lock once.
func (rb *RingBuffer) WriteMulti(lines []string) {
	if len(lines) == 0 {
		return
	}
	rb.mu.Lock()
	defer rb.mu.Unlock()

	for _, line := range lines {
		rb.lines[rb.writeIdx] = line
		rb.writeIdx = (rb.writeIdx + 1) % rb.capacity
		if rb.count < rb.capacity {
			rb.count++
		}
	}
}

// Lines returns a copy ordered from oldest to newest.
func (rb *RingBuffer) Lines() []string {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.linesUnsafe()
}

// Len returns the current number of entries.
func (rb *RingBuffer) Len() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.count
}

// Clear removes every entry.
func (rb *RingBuffer) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.writeIdx = 0
	rb.count = 0
}

// Drain atomically returns every entry and clears the buffer. It avoids losing
// writes that could otherwise occur between separate Lines and Clear calls.
func (rb *RingBuffer) Drain() []string {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	lines := rb.linesUnsafe()
	rb.writeIdx = 0
	rb.count = 0
	return lines
}

// linesUnsafe returns a snapshot while the caller holds mu.
func (rb *RingBuffer) linesUnsafe() []string {
	if rb.count == 0 {
		return nil
	}

	result := make([]string, 0, rb.count)
	if rb.count < rb.capacity {
		result = append(result, rb.lines[:rb.count]...)
	} else {
		start := rb.writeIdx % rb.capacity
		result = append(result, rb.lines[start:]...)
		result = append(result, rb.lines[:start]...)
	}
	return result
}

// Snapshot returns an independent buffer copy.
func (rb *RingBuffer) Snapshot() *RingBuffer {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	snap := New(rb.capacity)
	snap.count = rb.count
	snap.writeIdx = rb.writeIdx
	copy(snap.lines, rb.lines)
	return snap
}
