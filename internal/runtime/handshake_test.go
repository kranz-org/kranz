package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

func TestShutdownClientNegotiatesOldProtocolsAndUsesTheirEnvelope(t *testing.T) {
	for _, oldProtocol := range []int{1, 2} {
		t.Run(fmt.Sprint(oldProtocol), func(t *testing.T) {
			_, socketPath, cleanupDir, err := NewSocketDir()
			if err != nil {
				t.Fatal(err)
			}
			defer cleanupDir()
			listener, err := listenUnix(socketPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()

			serverDone := make(chan error, 1)
			go func() {
				conn, err := listener.AcceptUnix()
				if err != nil {
					serverDone <- err
					return
				}
				defer func() { _ = conn.Close() }()
				c := newCodec(conn)
				hello, err := c.receive()
				if err != nil {
					serverDone <- err
					return
				}
				var request helloRequest
				if err := json.Unmarshal(hello.Body, &request); err != nil {
					serverDone <- err
					return
				}
				if request.ProtocolMin != 1 || request.ProtocolMax != protocolVersion {
					serverDone <- fmt.Errorf("hello range = %d..%d", request.ProtocolMin, request.ProtocolMax)
					return
				}
				body, _ := json.Marshal(helloResponse{ProtocolMin: oldProtocol, ProtocolMax: oldProtocol, AgreedProtocol: oldProtocol})
				if err := c.send(envelope{V: oldProtocol, Type: messageResponse, ID: hello.ID, Body: body}); err != nil {
					serverDone <- err
					return
				}
				for _, method := range []string{methodShutdownPlan, methodShutdown} {
					request, err := c.receive()
					if err != nil {
						serverDone <- err
						return
					}
					if request.V != oldProtocol || request.Method != method {
						serverDone <- fmt.Errorf("request = protocol %d method %q, want protocol %d method %q", request.V, request.Method, oldProtocol, method)
						return
					}
					var response []byte
					if method == methodShutdownPlan {
						response, _ = json.Marshal(shutdownPlanResponse{Plan: app.ShutdownPlan{Managed: []string{"worker"}}})
					} else {
						response, _ = json.Marshal(emptyResponse{})
					}
					if err := c.send(envelope{V: oldProtocol, Type: messageResponse, ID: request.ID, Body: response}); err != nil {
						serverDone <- err
						return
					}
				}
				serverDone <- nil
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			client, err := DialContextForShutdown(ctx, socketPath, "v0.16.2")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.Close() }()
			plan, err := client.ShutdownPlanChecked()
			if err != nil || len(plan.Managed) != 1 || plan.Managed[0] != "worker" {
				t.Fatalf("shutdown plan = %#v, error %v", plan, err)
			}
			if err := client.Shutdown(); err != nil {
				t.Fatal(err)
			}
			if err := <-serverDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

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

func TestDialRejectsAnOldServerThatIncorrectlyReturnsHelloSuccess(t *testing.T) {
	_, socketPath, cleanupDir, err := NewSocketDir()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupDir()
	listener, err := listenUnix(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer func() { _ = conn.Close() }()
		codec := newCodec(conn)
		request, receiveErr := codec.receive()
		if receiveErr != nil {
			serverDone <- receiveErr
			return
		}
		body, marshalErr := json.Marshal(helloResponse{
			ProtocolMin: 0, ProtocolMax: 0, ServerVersion: "v0.7.2", AgreedProtocol: 0,
		})
		if marshalErr != nil {
			serverDone <- marshalErr
			return
		}
		serverDone <- codec.send(envelope{Type: messageResponse, ID: request.ID, Body: body})
	}()

	_, err = Dial(socketPath, "v0.8.0")
	var mismatch *VersionMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("Dial error = %T %v, want VersionMismatchError", err, err)
	}
	if mismatch.ServerProtocol != 0 || mismatch.ServerVersion != "v0.7.2" {
		t.Fatalf("mismatch = %#v", mismatch)
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatalf("fake server: %v", serverErr)
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
