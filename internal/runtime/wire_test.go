package runtime

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestWriteReadFrameRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte(`{"hello":"world"}`)
	if err := writeFrame(&buf, payload); err != nil {
		t.Fatal(err)
	}
	got, err := readFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("readFrame = %q, want %q", got, payload)
	}
}

func TestReadFrameRejectsBadMagic(t *testing.T) {
	buf := bytes.NewBuffer([]byte{'X', 'X', 'X', 'X', 0, 0, 0, 0})
	if _, err := readFrame(buf); !errors.Is(err, errFrameMagicMismatch) {
		t.Fatalf("err = %v, want errFrameMagicMismatch", err)
	}
}

func TestReadFrameRejectsOversizedLength(t *testing.T) {
	header := []byte{'K', 'R', 'Z', '1', 0xFF, 0xFF, 0xFF, 0xFF}
	buf := bytes.NewBuffer(header)
	if _, err := readFrame(buf); !errors.Is(err, errFramePayloadTooBig) {
		t.Fatalf("err = %v, want errFramePayloadTooBig", err)
	}
}

func TestWriteFrameRejectsOversizedPayload(t *testing.T) {
	var buf bytes.Buffer
	oversized := make([]byte, maxFramePayload+1)
	if err := writeFrame(&buf, oversized); !errors.Is(err, errFramePayloadTooBig) {
		t.Fatalf("err = %v, want errFramePayloadTooBig", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("writeFrame wrote %d bytes before rejecting an oversized payload", buf.Len())
	}
}

func TestReadFrameReportsCleanEOFBetweenFrames(t *testing.T) {
	if _, err := readFrame(bytes.NewReader(nil)); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}

func TestReadFrameReportsTruncatedHeaderAsUnexpectedEOF(t *testing.T) {
	buf := bytes.NewBuffer([]byte{'K', 'R', 'Z'})
	_, err := readFrame(buf)
	if err == nil {
		t.Fatal("expected an error reading a truncated header")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want to wrap io.ErrUnexpectedEOF", err)
	}
}

func TestReadFrameHandlesAnEmptyPayload(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFrame(&buf, nil); err != nil {
		t.Fatal(err)
	}
	got, err := readFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("readFrame = %v, want empty", got)
	}
}
