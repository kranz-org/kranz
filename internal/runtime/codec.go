package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
)

// codec serializes envelopes over one connection as length-prefixed frames.
// Sends are safe to call from multiple goroutines; receives are not, since
// only one goroutine (the connection's read loop) ever calls receive.
type codec struct {
	conn    net.Conn
	version int
	writeMu sync.Mutex
}

func newCodec(conn net.Conn) *codec {
	return &codec{conn: conn, version: protocolVersion}
}

func (c *codec) send(msg envelope) error {
	if msg.V == 0 {
		msg.V = c.version
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	err = writeFrame(c.conn, payload)
	if errors.Is(err, errFramePayloadTooBig) && (msg.Type == messageResponse || msg.Type == messageError) {
		// No bytes have been written. Reply with a small error instead of
		// leaving the caller waiting forever for an unsent response.
		body, _ := json.Marshal(errorPayload{Kind: errorGeneric, Code: "response_too_large", Message: ErrResponseTooLarge.Error()})
		payload, _ = json.Marshal(envelope{V: msg.V, Type: messageError, ID: msg.ID, Body: body})
		err = writeFrame(c.conn, payload)
		if err == nil {
			return ErrResponseTooLarge
		}
	}
	if err != nil && !errors.Is(err, errFramePayloadTooBig) {
		// A partial frame cannot be recovered on this stream.
		_ = c.conn.Close()
	}
	return err
}

func (c *codec) receive() (envelope, error) {
	payload, err := readFrame(c.conn)
	if err != nil {
		return envelope{}, err
	}
	var msg envelope
	if err := json.Unmarshal(payload, &msg); err != nil {
		return envelope{}, fmt.Errorf("unmarshal envelope: %w", err)
	}
	return msg, nil
}
