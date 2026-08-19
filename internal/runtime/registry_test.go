package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
	"golang.org/x/sys/unix"
)

func TestRegistrySessionLifecycleAndPermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "registry")
	registry, err := NewRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	assertMode(t, root, 0o700)
	handle, err := registry.Acquire("shop-dev")
	if err != nil {
		t.Fatal(err)
	}
	lockPath := registry.lockPath("shop-dev")
	assertMode(t, lockPath, 0o600)
	flags, err := unix.FcntlInt(handle.lock.Fd(), unix.F_GETFD, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("runtime lock is inherited across exec")
	}
	metadata, err := handle.Prepare("Shop", "dev", "tui", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" && len(metadata.Socket) >= 104 {
		t.Fatalf("socket path is %d bytes", len(metadata.Socket))
	}
	listener, err := listenUnix(metadata.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	assertMode(t, metadata.Socket, 0o600)
	if err := handle.Publish(); err != nil {
		t.Fatal(err)
	}
	assertMode(t, registry.metadataPath("shop-dev"), 0o600)
	assertMode(t, registry.ownershipPath("shop-dev"), 0o600)
	services := []*app.ServiceSnapshot{{Name: "web", State: config.ServiceState{PID: os.Getpid()}}}
	if err := handle.UpdateOwnership(services); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(registry.ownershipPath("shop-dev"))
	if err != nil {
		t.Fatal(err)
	}
	var ownership OwnershipSnapshot
	if err := json.Unmarshal(data, &ownership); err != nil {
		t.Fatal(err)
	}
	if ownership.SessionID != metadata.ID || len(ownership.Processes) != 1 || ownership.Processes[0].PID != os.Getpid() || ownership.Processes[0].Fingerprint == "" {
		t.Fatalf("ownership snapshot = %+v", ownership)
	}
	if _, err := registry.Acquire("shop-dev"); err == nil {
		t.Fatal("duplicate runtime acquired")
	} else {
		var conflict *SessionConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("duplicate error = %T: %v", err, err)
		}
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("stable lock path disappeared: %v", err)
	}
	if _, err := os.Stat(metadata.Socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket remains after close: %v", err)
	}
}

func TestRegistryListCleansStaleMetadataWithoutRemovingLock(t *testing.T) {
	registry, err := NewRegistry(filepath.Join(t.TempDir(), "registry"))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := registry.Acquire("stale")
	if err != nil {
		t.Fatal(err)
	}
	_, err = handle.Prepare("Stale", "dev", "background", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Publish(); err != nil {
		t.Fatal(err)
	}
	if err := syscallUnlockAndClose(handle); err != nil {
		t.Fatal(err)
	}
	records, err := registry.List(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("List returned stale session: %+v", records)
	}
	if _, err := os.Stat(registry.lockPath("stale")); err != nil {
		t.Fatalf("cleanup removed lock: %v", err)
	}
	if _, err := os.Stat(registry.metadataPath("stale")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata was not cleaned: %v", err)
	}
}

func TestForceDownRefusesMismatchedSupervisorBirthIdentity(t *testing.T) {
	registry, err := NewRegistry(filepath.Join(t.TempDir(), "registry"))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := registry.Acquire("mismatch")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := handle.Prepare("Mismatch", "dev", "background", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Publish(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = handle.Close() }()
	metadata.ProcessFingerprint = "not-the-current-process"
	if err := atomicJSON(registry.metadataPath(metadata.Name), metadata); err != nil {
		t.Fatal(err)
	}
	record := SessionRecord{SessionMetadata: metadata, State: SessionUnreachable}
	err = registry.ForceDown(context.Background(), record)
	var refused *ForceDownError
	if !errors.As(err, &refused) {
		t.Fatalf("ForceDown error = %T %v, want ForceDownError", err, err)
	}
}

func syscallUnlockAndClose(handle *SessionHandle) error {
	if err := unix.Flock(int(handle.lock.Fd()), unix.LOCK_UN); err != nil {
		return err
	}
	handle.closed = true
	return handle.lock.Close()
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func TestDialContextBoundsHello(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "slow.sock")
	listener, err := listenUnix(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr == nil {
			defer func() { _ = conn.Close() }()
			time.Sleep(time.Second)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = DialContext(ctx, socket, "dev")
	if err == nil {
		t.Fatal("DialContext unexpectedly completed")
	}
	if time.Since(started) > 500*time.Millisecond {
		t.Fatalf("DialContext ignored deadline")
	}
}

func TestClientCloseIsIdempotentAfterPeerCloses(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "kz-close-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cleanupErr := os.RemoveAll(dir); cleanupErr != nil {
			t.Error(cleanupErr)
		}
	})
	socket := filepath.Join(dir, "close.sock")
	listener, err := listenUnix(socket)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			return
		}
		codec := newCodec(conn)
		request, receiveErr := codec.receive()
		if receiveErr == nil && request.Method == methodHello {
			body, _ := json.Marshal(helloResponse{ProtocolMin: protocolVersion, ProtocolMax: protocolVersion, AgreedProtocol: protocolVersion, ServerVersion: "dev"})
			_ = codec.send(envelope{Type: messageResponse, ID: request.ID, Body: body})
		}
		_ = conn.Close()
		_ = listener.Close()
	}()
	client, err := Dial(socket, "dev")
	if err != nil {
		t.Fatal(err)
	}
	<-done
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("client Done did not close after peer disconnect")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestAcquireRetriesWhenLockPathIsReplaced(t *testing.T) {
	registry, err := NewRegistry(filepath.Join(t.TempDir(), "registry"))
	if err != nil {
		t.Fatal(err)
	}
	var replacement *os.File
	_, err = registry.acquire("race", func() {
		if removeErr := os.Remove(registry.lockPath("race")); removeErr != nil {
			t.Fatal(removeErr)
		}
		replacement, err = os.OpenFile(registry.lockPath("race"), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if err = unix.Flock(int(replacement.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
			t.Fatal(err)
		}
	})
	if replacement != nil {
		defer func() { _ = unix.Flock(int(replacement.Fd()), unix.LOCK_UN); _ = replacement.Close() }()
	}
	var conflict *SessionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Acquire after replacement = %T %v, want conflict", err, err)
	}
}

func TestRegistryListDiscoversRunningSupervisor(t *testing.T) {
	registry, err := NewRegistry(filepath.Join(t.TempDir(), "registry"))
	if err != nil {
		t.Fatal(err)
	}
	handle, err := registry.Acquire("shop")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := handle.Prepare("Shop", "dev", "background", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Project: "Shop", Services: map[string]config.Service{"web": {Command: "true"}}}
	supervisor := NewSupervisor(app.NewLocal(cfg, nil, app.Options{}))
	if err := supervisor.Listen(metadata.Socket); err != nil {
		t.Fatal(err)
	}
	if err := handle.Publish(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- supervisor.Serve() }()
	defer func() { _ = supervisor.Close(); <-done; _ = handle.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	records, err := registry.List(ctx, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].State != SessionRunning || records[0].Services == nil || *records[0].Services != 1 {
		t.Fatalf("List = %+v", records)
	}
}

func TestAcquireDoesNotReplaceLiveSessionAfterLockPathReplacement(t *testing.T) {
	registry, err := NewRegistry(filepath.Join(t.TempDir(), "registry"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := registry.Acquire("live")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := owner.Prepare("Live", "dev", "tui", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	listener, err := listenUnix(metadata.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	if err := owner.Publish(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(registry.lockPath("live")); err != nil {
		t.Fatal(err)
	}
	replacement, err := os.OpenFile(registry.lockPath("live"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = registry.Acquire("live")
	var conflict *SessionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Acquire = %T %v, want conflict", err, err)
	}
	if _, err := os.Stat(metadata.Socket); err != nil {
		t.Fatalf("live socket was removed: %v", err)
	}
	if _, err := os.Stat(registry.metadataPath("live")); err != nil {
		t.Fatalf("live metadata was removed: %v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
}
