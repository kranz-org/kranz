// Package runtime carries the application layer (internal/app) across a
// process boundary over a local Unix socket, so a delivery surface no longer
// has to run in the same process as the runtime it drives. Supervisor hosts
// an app.API implementation and answers requests; Client implements app.API
// itself by encoding each call as a request and decoding the response, so a
// caller cannot tell the two apart.
package runtime

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// frameMagic identifies a Kranz runtime frame and its protocol generation.
// Changing the wire format bumps this value; a peer that reads a mismatched
// magic knows immediately it is not talking to a compatible frame reader,
// before it ever reaches JSON decoding or protocol version negotiation.
var frameMagic = [4]byte{'K', 'R', 'Z', '1'}

// maxFramePayload bounds one frame's JSON payload. It exists to stop a
// malformed or hostile peer from making a reader allocate an unbounded
// buffer from a forged length prefix; every real payload today (service
// snapshots, log entries, action output) is far smaller.
const maxFramePayload = 16 * 1024 * 1024

var (
	errFrameMagicMismatch = errors.New("runtime: frame magic mismatch")
	errFramePayloadTooBig = errors.New("runtime: frame payload exceeds the maximum size")
)

// writeFrame writes one length-prefixed, magic-tagged frame. It is safe to
// call from one writer goroutine at a time; callers that share a connection
// across goroutines must serialize their own writes.
func writeFrame(w io.Writer, payload []byte) error {
	if len(payload) > maxFramePayload {
		return errFramePayloadTooBig
	}
	header := make([]byte, 8)
	copy(header[:4], frameMagic[:])
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if len(payload) == 0 {
		return nil
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("write frame payload: %w", err)
	}
	return nil
}

// readFrame reads one length-prefixed, magic-tagged frame written by
// writeFrame. It returns io.EOF unmodified when the peer closed the
// connection cleanly between frames, so callers can distinguish a graceful
// disconnect from a malformed stream.
func readFrame(r io.Reader) ([]byte, error) {
	header := make([]byte, 8)
	if _, err := io.ReadFull(r, header); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("read frame header: %w", err)
		}
		return nil, err
	}
	if [4]byte(header[:4]) != frameMagic {
		return nil, errFrameMagicMismatch
	}
	length := binary.BigEndian.Uint32(header[4:])
	if length > maxFramePayload {
		return nil, errFramePayloadTooBig
	}
	if length == 0 {
		return nil, nil
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("read frame payload: %w", err)
	}
	return payload, nil
}
