package runtime

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

func TestDialRejectsAnIncompatibleProtocolRange(t *testing.T) {
	cfg := &config.Config{Project: "Version Mismatch"}
	local := app.NewLocal(cfg, nil, app.Options{})
	defer local.Shutdown()
	supervisor := NewSupervisor(local)
	_, socketPath, cleanupDir, err := NewSocketDir()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupDir()
	if err := supervisor.Listen(socketPath); err != nil {
		t.Fatal(err)
	}
	go func() { _ = supervisor.Serve() }()
	defer func() { _ = supervisor.Close() }()

	var conn net.Conn
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err = net.Dial("unix", socketPath)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() { _ = conn.Close() }()

	c := newCodec(conn)
	body, _ := json.Marshal(helloRequest{ProtocolMin: 99, ProtocolMax: 99, ClientVersion: "future-kranz"})
	if err := c.send(envelope{Type: messageRequest, ID: 0, Method: methodHello, Body: body}); err != nil {
		t.Fatal(err)
	}
	reply, err := c.receive()
	if err != nil {
		t.Fatal(err)
	}
	if reply.Type != messageError {
		t.Fatalf("reply type = %s, want error", reply.Type)
	}
	var payload errorPayload
	if err := json.Unmarshal(reply.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Kind != errorVersionMismatch {
		t.Fatalf("error kind = %s, want version_mismatch", payload.Kind)
	}
	if !strings.Contains(payload.Message, "future-kranz") || !strings.Contains(payload.Message, "Upgrade kranz") {
		t.Fatalf("message = %q, missing expected content", payload.Message)
	}
}

func TestDialRejectsAConnectionThatNeverSendsHello(t *testing.T) {
	cfg := &config.Config{Project: "No Hello"}
	local := app.NewLocal(cfg, nil, app.Options{})
	defer local.Shutdown()
	supervisor := NewSupervisor(local)
	_, socketPath, cleanupDir, err := NewSocketDir()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupDir()
	if err := supervisor.Listen(socketPath); err != nil {
		t.Fatal(err)
	}
	go func() { _ = supervisor.Serve() }()
	defer func() { _ = supervisor.Close() }()

	var conn net.Conn
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err = net.Dial("unix", socketPath)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() { _ = conn.Close() }()

	c := newCodec(conn)
	body, _ := json.Marshal(reloadRequest{Force: true})
	if err := c.send(envelope{Type: messageRequest, ID: 0, Method: methodReload, Body: body}); err != nil {
		t.Fatal(err)
	}
	// The server closes the connection instead of dispatching a method
	// before a successful handshake; the read must observe that, not a
	// dispatched response.
	if _, err := c.receive(); err == nil {
		t.Fatal("expected the connection to be closed for skipping the handshake")
	}
}
