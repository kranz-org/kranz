package ringbuffer

import (
	"testing"
)

func TestNew(t *testing.T) {
	rb := New(100)
	if rb.Len() != 0 {
		t.Errorf("new buffer should be empty, got %d", rb.Len())
	}
	if rb.capacity != 100 {
		t.Errorf("capacity should be 100, got %d", rb.capacity)
	}
}

func TestDefaultCapacity(t *testing.T) {
	rb := New(0)
	if rb.capacity != 1000 {
		t.Errorf("default capacity should be 1000, got %d", rb.capacity)
	}

	rb2 := New(-5)
	if rb2.capacity != 1000 {
		t.Errorf("default capacity for negative should be 1000, got %d", rb2.capacity)
	}
}

func TestWriteAndRead(t *testing.T) {
	rb := New(5)

	rb.Write("line1")
	rb.Write("line2")
	rb.Write("line3")

	lines := rb.Lines()
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(lines))
	}
	if lines[0] != "line1" {
		t.Errorf("expected 'line1', got '%s'", lines[0])
	}
	if lines[2] != "line3" {
		t.Errorf("expected 'line3', got '%s'", lines[2])
	}
}

func TestOverflow(t *testing.T) {
	rb := New(3)

	rb.Write("line1")
	rb.Write("line2")
	rb.Write("line3")
	rb.Write("line4")
	rb.Write("line5")

	lines := rb.Lines()
	if len(lines) != 3 {
		t.Errorf("expected 3 lines after overflow, got %d", len(lines))
	}
	if lines[0] != "line3" {
		t.Errorf("expected 'line3' as oldest after overflow, got '%s'", lines[0])
	}
	if lines[2] != "line5" {
		t.Errorf("expected 'line5' as newest, got '%s'", lines[2])
	}
}

func TestClear(t *testing.T) {
	rb := New(10)
	rb.Write("line1")
	rb.Write("line2")
	rb.Clear()

	if rb.Len() != 0 {
		t.Errorf("buffer should be empty after clear, got %d", rb.Len())
	}
}

func TestWriteMulti(t *testing.T) {
	rb := New(10)
	rb.WriteMulti([]string{"a", "b", "c"})

	if rb.Len() != 3 {
		t.Errorf("expected 3 lines, got %d", rb.Len())
	}
	lines := rb.Lines()
	if lines[0] != "a" || lines[1] != "b" || lines[2] != "c" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

func TestSnapshot(t *testing.T) {
	rb := New(10)
	rb.Write("line1")
	rb.Write("line2")

	snap := rb.Snapshot()
	if snap.Len() != 2 {
		t.Errorf("snapshot should have 2 lines, got %d", snap.Len())
	}

	// Original still has data
	if rb.Len() != 2 {
		t.Errorf("original should still have 2 lines, got %d", rb.Len())
	}
}

func TestConcurrentAccess(t *testing.T) {
	rb := New(1000)
	done := make(chan struct{})

	go func() {
		for i := 0; i < 100; i++ {
			rb.Write("goroutine1")
		}
		done <- struct{}{}
	}()

	go func() {
		for i := 0; i < 100; i++ {
			rb.Write("goroutine2")
		}
		done <- struct{}{}
	}()

	go func() {
		for i := 0; i < 50; i++ {
			_ = rb.Lines()
			_ = rb.Len()
		}
		done <- struct{}{}
	}()

	<-done
	<-done
	<-done

	if rb.Len() != 200 {
		t.Errorf("expected 200 lines, got %d", rb.Len())
	}
}
