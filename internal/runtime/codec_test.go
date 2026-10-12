package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestCallAfterReadLoopExitDoesNotWaitForReply(t *testing.T) {
	server, conn := net.Pipe()
	defer server.Close()
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	client := &Client{c: newCodec(conn), pending: make(map[uint64]chan envelope), done: make(chan struct{})}
	close(client.done)
	go func() { _, _ = newCodec(server).receive() }()
	_, err := client.roundTrip(context.Background(), methodProject, nil)
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("call after read loop exit = %v", err)
	}
}

func TestLogQueryCancellationDoesNotWaitForMissingResponse(t *testing.T) {
	server, conn := net.Pipe()
	defer server.Close()
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	client := &Client{c: newCodec(conn), pending: make(map[uint64]chan envelope), done: make(chan struct{})}
	go func() {
		peer := newCodec(server)
		_, _ = peer.receive()
		_, _ = peer.receive() // Cancellation, with no response from an old peer.
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := client.roundTrip(ctx, methodQueryLogs, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unanswered query = %v", err)
	}
	if time.Since(start) >= time.Second {
		t.Fatal("log query waited for a lost cancellation reply")
	}
}

func TestOversizedResponseReturnsErrorAndKeepsConnectionUsable(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	_ = server.SetDeadline(time.Now().Add(5 * time.Second))
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	sender, receiver := newCodec(server), newCodec(client)
	body, err := json.Marshal(strings.Repeat("x", maxFramePayload))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- sender.send(envelope{Type: messageResponse, ID: 42, Body: body}) }()
	reply, err := receiver.receive()
	if err != nil {
		t.Fatal(err)
	}
	if reply.ID != 42 {
		t.Fatalf("reply ID = %d", reply.ID)
	}
	if _, err := decodeReply(reply); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("oversized response error = %v", err)
	}
	if err := <-done; !errors.Is(err, ErrResponseTooLarge) {
		t.Fatal(err)
	}
	go func() { done <- sender.send(envelope{Type: messageResponse, ID: 43, Body: json.RawMessage(`{}`)}) }()
	reply, err = receiver.receive()
	if err != nil || reply.ID != 43 || reply.Type != messageResponse {
		t.Fatalf("next response = %#v, %v", reply, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
