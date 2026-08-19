package runtime

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
)

// codec serializes envelopes over one connection as length-prefixed frames.
// Sends are safe to call from multiple goroutines; receives are not, since
// only one goroutine (the connection's read loop) ever calls receive.
type codec struct {
	conn    net.Conn
	writeMu sync.Mutex
}

func newCodec(conn net.Conn) *codec {
	return &codec{conn: conn}
}

func (c *codec) send(msg envelope) error {
	if msg.V == 0 {
		msg.V = protocolVersion
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeFrame(c.conn, payload)
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
