package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kranz-org/kranz/internal/app"
)

const metadataVersion = 1

type SessionState string

const (
	SessionRunning      SessionState = "running"
	SessionIncompatible SessionState = "incompatible"
	SessionUnreachable  SessionState = "unreachable"
)

type SessionMetadata struct {
	MetadataVersion    int       `json:"metadata_version"`
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Project            string    `json:"project"`
	PID                int       `json:"pid"`
	ProcessStarted     time.Time `json:"process_started_at"`
	ProcessFingerprint string    `json:"process_birth_fingerprint"`
	KranzVersion       string    `json:"kranz_version"`
	ProtocolMin        int       `json:"protocol_min"`
	ProtocolMax        int       `json:"protocol_max"`
	Socket             string    `json:"socket"`
	Mode               string    `json:"mode"`
	StartedAt          time.Time `json:"started_at"`
	Directory          string    `json:"directory"`
}

type SessionRecord struct {
	SessionMetadata
	State    SessionState `json:"state"`
	Services *int         `json:"services"`
}

type Registry struct {
	root       string
	socketRoot string
}

func DefaultRegistry() (*Registry, error) {
	return NewRegistry(filepath.Join(os.TempDir(), fmt.Sprintf("kranz-%d", os.Getuid())))
}

func NewRegistry(root string) (*Registry, error) {
	if err := ensureOwnedDirectory(root); err != nil {
		return nil, fmt.Errorf("create runtime registry: %w", err)
	}
	socketRoot := root
	if len(filepath.Join(socketRoot, "s-0000000000000000.sock")) >= 104 {
		socketRoot = filepath.Join("/tmp", fmt.Sprintf("kz-%d", os.Getuid()))
		if err := ensureOwnedDirectory(socketRoot); err != nil {
			return nil, fmt.Errorf("create runtime socket directory: %w", err)
		}
	}
	return &Registry{root: root, socketRoot: socketRoot}, nil
}

func ensureOwnedDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s is not a real directory", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("%s is not owned by the current user", path)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	return nil
}

type SessionConflictError struct{ Name string }

func (e *SessionConflictError) Error() string {
	return fmt.Sprintf("runtime %q is already active", e.Name)
}

type SessionNotFoundError struct{ Reference string }

func (e *SessionNotFoundError) Error() string {
	return fmt.Sprintf("runtime %q was not found", e.Reference)
}

type AmbiguousSessionError struct {
	Reference string
	Matches   []string
}

func (e *AmbiguousSessionError) Error() string {
	return fmt.Sprintf("runtime reference %q is ambiguous (%s)", e.Reference, strings.Join(e.Matches, ", "))
}

type SessionHandle struct {
	registry      *Registry
	lock          *os.File
	meta          SessionMetadata
	mu            sync.Mutex
	lastOwnership string
	closed        bool
}

func (h *SessionHandle) Metadata() SessionMetadata { return h.meta }

type OwnershipSnapshot struct {
	Version   int            `json:"ownership_version"`
	SessionID string         `json:"session_id"`
	Processes []OwnedProcess `json:"processes"`
}

type OwnedProcess struct {
	Service     string `json:"service"`
	PID         int    `json:"pid"`
	PGID        int    `json:"pgid"`
	Fingerprint string `json:"birth_fingerprint"`
}

type ForceDownError struct{ Reason string }

func (e *ForceDownError) Error() string { return "refusing forced shutdown: " + e.Reason }

func (r *Registry) Acquire(name string) (*SessionHandle, error) {
	return r.acquire(name, nil)
}

func (r *Registry) acquire(name string, afterOpen func()) (*SessionHandle, error) {
	lockPath := r.lockPath(name)
	for attempts := 0; attempts < 8; attempts++ {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_CLOEXEC, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open runtime lock: %w", err)
		}
		if afterOpen != nil {
			afterOpen()
			afterOpen = nil
		}
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			if errors.Is(err, syscall.EWOULDBLOCK) {
				matched, statErr := sameOpenFile(file, lockPath)
				_ = file.Close()
				if statErr == nil && matched {
					return nil, &SessionConflictError{Name: name}
				}
				continue
			}
			_ = file.Close()
			return nil, fmt.Errorf("lock runtime %q: %w", name, err)
		}
		matched, err := sameOpenFile(file, lockPath)
		if err == nil && matched {
			live, socket := r.existingSessionLive(name)
			if live {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
				return nil, &SessionConflictError{Name: name}
			}
			if socket != "" {
				_ = os.Remove(socket)
			}
			_ = os.Remove(r.metadataPath(name))
			_ = os.Remove(r.ownershipPath(name))
			return &SessionHandle{registry: r, lock: file}, nil
		}
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}
	return nil, fmt.Errorf("runtime lock %q kept changing", name)
}

func (r *Registry) existingSessionLive(name string) (bool, string) {
	data, err := os.ReadFile(r.metadataPath(name))
	if err != nil {
		return false, ""
	}
	var metadata SessionMetadata
	if json.Unmarshal(data, &metadata) != nil || metadata.Socket == "" {
		return false, ""
	}
	conn, err := net.DialTimeout("unix", metadata.Socket, 150*time.Millisecond)
	if err != nil {
		return false, metadata.Socket
	}
	_ = conn.Close()
	return true, metadata.Socket
}

func sameOpenFile(file *os.File, path string) (bool, error) {
	opened, err := file.Stat()
	if err != nil {
		return false, err
	}
	current, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return os.SameFile(opened, current), nil
}

func (h *SessionHandle) Prepare(project, version, mode, directory string) (SessionMetadata, error) {
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return SessionMetadata{}, fmt.Errorf("create session id: %w", err)
	}
	now := time.Now().UTC()
	started, err := processStartedAt(os.Getpid())
	if err != nil {
		return SessionMetadata{}, fmt.Errorf("identify runtime process start: %w", err)
	}
	_, fingerprint, err := processIdentity(os.Getpid())
	if err != nil {
		return SessionMetadata{}, fmt.Errorf("identify runtime process: %w", err)
	}
	hash := sha256.Sum256([]byte(h.registry.root + "\x00" + hex.EncodeToString(idBytes)))
	socket := filepath.Join(h.registry.socketRoot, "s-"+hex.EncodeToString(hash[:8])+".sock")
	_ = os.Remove(socket)
	h.meta = SessionMetadata{MetadataVersion: metadataVersion, ID: hex.EncodeToString(idBytes), Name: strings.TrimSuffix(filepath.Base(h.lock.Name()), ".lock"), Project: project, PID: os.Getpid(), ProcessStarted: started, ProcessFingerprint: fingerprint, KranzVersion: version, ProtocolMin: protocolVersion, ProtocolMax: protocolVersion, Socket: socket, Mode: mode, StartedAt: now, Directory: directory}
	return h.meta, nil
}

// Publish atomically makes a listening session discoverable. Call it only
// after the socket has been bound and permissioned.
func (h *SessionHandle) Publish() error {
	if h.meta.ID == "" {
		return errors.New("runtime metadata was not prepared")
	}
	if err := atomicJSON(h.registry.metadataPath(h.meta.Name), h.meta); err != nil {
		return err
	}
	return h.UpdateOwnership(nil)
}

// UpdateOwnership replaces the recovery snapshot without recording commands,
// environment, or other secrets. A birth fingerprint makes PID reuse safe to
// reject during a later forced shutdown.
func (h *SessionHandle) UpdateOwnership(services []*app.ServiceSnapshot) error {
	processes := make([]OwnedProcess, 0, len(services))
	for _, service := range services {
		if service == nil || service.State.PID <= 0 {
			continue
		}
		pgid, fingerprint, err := processIdentity(service.State.PID)
		if err != nil {
			continue
		}
		processes = append(processes, OwnedProcess{Service: service.Name, PID: service.State.PID, PGID: pgid, Fingerprint: fingerprint})
	}
	sort.Slice(processes, func(i, j int) bool { return processes[i].Service < processes[j].Service })
	snapshot := OwnershipSnapshot{Version: 1, SessionID: h.meta.ID, Processes: processes}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if string(encoded) == h.lastOwnership {
		return nil
	}
	if err := atomicJSON(h.registry.ownershipPath(h.meta.Name), snapshot); err != nil {
		return err
	}
	h.lastOwnership = string(encoded)
	return nil
}

func (h *SessionHandle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	if h.meta.Socket != "" {
		_ = os.Remove(h.meta.Socket)
	}
	if h.meta.Name != "" {
		_ = os.Remove(h.registry.metadataPath(h.meta.Name))
		_ = os.Remove(h.registry.ownershipPath(h.meta.Name))
	}
	err := syscall.Flock(int(h.lock.Fd()), syscall.LOCK_UN)
	return errors.Join(err, h.lock.Close())
}

func atomicJSON(path string, value any) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".kranz-meta-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	err = json.NewEncoder(tmp).Encode(value)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (r *Registry) List(ctx context.Context, clientVersion string) ([]SessionRecord, error) {
	paths, err := filepath.Glob(filepath.Join(r.root, "*.json"))
	if err != nil {
		return nil, err
	}
	records := make([]SessionRecord, 0, len(paths))
	for _, path := range paths {
		if strings.HasSuffix(path, ".ownership.json") {
			continue
		}
		var metadata SessionMetadata
		data, readErr := os.ReadFile(path)
		if readErr != nil || json.Unmarshal(data, &metadata) != nil || metadata.MetadataVersion != metadataVersion {
			continue
		}
		locked, probeErr := r.isLocked(metadata.Name)
		if probeErr != nil {
			continue
		}
		record := SessionRecord{SessionMetadata: metadata, State: SessionUnreachable}
		client, dialErr := DialContext(ctx, metadata.Socket, clientVersion)
		if dialErr == nil {
			count := len(client.Services())
			record.Services = &count
			record.State = SessionRunning
			_ = client.Close()
		} else {
			var mismatch *VersionMismatchError
			if errors.As(dialErr, &mismatch) {
				record.State = SessionIncompatible
			}
		}
		if !locked && dialErr != nil && record.State == SessionUnreachable {
			_ = os.Remove(metadata.Socket)
			_ = os.Remove(path)
			_ = os.Remove(r.ownershipPath(metadata.Name))
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Name < records[j].Name })
	return records, nil
}

// Resolve accepts an exact NAME, a full ID, or a unique ID prefix.
func (r *Registry) Resolve(ctx context.Context, reference, clientVersion string) (SessionRecord, error) {
	records, err := r.List(ctx, clientVersion)
	if err != nil {
		return SessionRecord{}, err
	}
	for _, record := range records {
		if record.Name == reference || record.ID == reference {
			return record, nil
		}
	}
	matches := make([]SessionRecord, 0)
	for _, record := range records {
		if strings.HasPrefix(record.ID, reference) {
			matches = append(matches, record)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return SessionRecord{}, &SessionNotFoundError{Reference: reference}
	}
	names := make([]string, len(matches))
	for i, match := range matches {
		names[i] = match.ID[:8] + " (" + match.Name + ")"
	}
	return SessionRecord{}, &AmbiguousSessionError{Reference: reference, Matches: names}
}

// ForceDown performs the recovery-only shutdown path for a session whose RPC
// endpoint cannot be reached. Every signal target is proven again immediately
// before use; a PID, metadata file, or ownership snapshot alone is never
// accepted as evidence of ownership.
func (r *Registry) ForceDown(ctx context.Context, record SessionRecord) error {
	metadata, ownership, err := r.forceEvidence(record)
	if err != nil {
		return err
	}
	for _, process := range ownership.Processes {
		if err := validateOwnedProcess(process, metadata.PID); err != nil {
			return err
		}
	}
	for _, process := range ownership.Processes {
		if err := signalOwnedGroup(process, syscall.SIGTERM); err != nil && !processGone(err) {
			return err
		}
	}
	if err := signalSupervisor(metadata, syscall.SIGTERM); err != nil && !processGone(err) {
		return err
	}
	if r.waitUnlocked(ctx, metadata.Name, 3*time.Second) {
		return r.cleanupForced(metadata.Name)
	}

	// Only identities that still match the immutable evidence may receive
	// SIGKILL. An identity mismatch is a hard refusal, never a reason to widen
	// or substitute the target set.
	for _, process := range ownership.Processes {
		if err := validateOwnedProcess(process, metadata.PID); err != nil {
			if processGone(err) {
				continue
			}
			return err
		}
		if err := signalOwnedGroup(process, syscall.SIGKILL); err != nil && !processGone(err) {
			return err
		}
	}
	if err := validateSupervisor(metadata); err != nil {
		if !processGone(err) {
			return err
		}
	} else if err := syscall.Kill(metadata.PID, syscall.SIGKILL); err != nil && !processGone(err) {
		return err
	}
	if !r.waitUnlocked(ctx, metadata.Name, 3*time.Second) {
		return &ForceDownError{Reason: "runtime lock was not released after validated signals"}
	}
	return r.cleanupForced(metadata.Name)
}

func (r *Registry) forceEvidence(record SessionRecord) (SessionMetadata, OwnershipSnapshot, error) {
	locked, err := r.isLocked(record.Name)
	if err != nil {
		return SessionMetadata{}, OwnershipSnapshot{}, err
	}
	if !locked {
		return SessionMetadata{}, OwnershipSnapshot{}, &ForceDownError{Reason: "session lock is not held"}
	}
	data, err := os.ReadFile(r.metadataPath(record.Name))
	if err != nil {
		return SessionMetadata{}, OwnershipSnapshot{}, &ForceDownError{Reason: "immutable metadata is unavailable"}
	}
	var metadata SessionMetadata
	if err := json.Unmarshal(data, &metadata); err != nil || metadata.MetadataVersion != metadataVersion || metadata.Name != record.Name || metadata.ID != record.ID {
		return SessionMetadata{}, OwnershipSnapshot{}, &ForceDownError{Reason: "immutable metadata does not match the selected lock generation"}
	}
	if err := validateSupervisor(metadata); err != nil {
		return SessionMetadata{}, OwnershipSnapshot{}, err
	}
	data, err = os.ReadFile(r.ownershipPath(record.Name))
	if err != nil {
		return SessionMetadata{}, OwnershipSnapshot{}, &ForceDownError{Reason: "ownership snapshot is unavailable"}
	}
	var ownership OwnershipSnapshot
	if err := json.Unmarshal(data, &ownership); err != nil || ownership.Version != 1 || ownership.SessionID != metadata.ID {
		return SessionMetadata{}, OwnershipSnapshot{}, &ForceDownError{Reason: "ownership snapshot does not match the selected session"}
	}
	return metadata, ownership, nil
}

func validateSupervisor(metadata SessionMetadata) error {
	_, fingerprint, err := processIdentity(metadata.PID)
	if err != nil {
		return err
	}
	started, err := processStartedAt(metadata.PID)
	if err != nil {
		return err
	}
	if metadata.ProcessFingerprint == "" || fingerprint != metadata.ProcessFingerprint || !started.Equal(metadata.ProcessStarted) {
		return &ForceDownError{Reason: "runtime process birth identity does not match metadata"}
	}
	return nil
}

func validateOwnedProcess(process OwnedProcess, supervisorPID int) error {
	pgid, fingerprint, err := processIdentity(process.PID)
	if err != nil {
		return err
	}
	if process.PID <= 1 || process.PGID <= 1 || process.PGID != process.PID || pgid != process.PGID || fingerprint != process.Fingerprint {
		return &ForceDownError{Reason: fmt.Sprintf("service %q process identity does not match ownership evidence", process.Service)}
	}
	pid := process.PID
	for depth := 0; depth < 256 && pid > 1; depth++ {
		parent, err := processParent(pid)
		if err != nil {
			return err
		}
		if parent == supervisorPID {
			return nil
		}
		if parent <= 1 || parent == pid {
			break
		}
		pid = parent
	}
	return &ForceDownError{Reason: fmt.Sprintf("service %q is outside the runtime process ancestry", process.Service)}
}

func signalOwnedGroup(process OwnedProcess, signal syscall.Signal) error {
	// The full ancestry check is performed by the caller. Refresh the birth
	// identity immediately before signalling to close the PID-reuse race.
	pgid, fingerprint, err := processIdentity(process.PID)
	if err != nil {
		return err
	}
	if pgid != process.PGID || fingerprint != process.Fingerprint {
		return &ForceDownError{Reason: fmt.Sprintf("service %q changed identity before signal", process.Service)}
	}
	return syscall.Kill(-process.PGID, signal)
}

func signalSupervisor(metadata SessionMetadata, signal syscall.Signal) error {
	if err := validateSupervisor(metadata); err != nil {
		return err
	}
	return syscall.Kill(metadata.PID, signal)
}

func processGone(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH)
}

func (r *Registry) waitUnlocked(ctx context.Context, name string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		locked, err := r.isLocked(name)
		if err == nil && !locked {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (r *Registry) cleanupForced(name string) error {
	handle, err := r.Acquire(name)
	if err != nil {
		return err
	}
	return handle.Close()
}

func (r *Registry) isLocked(name string) (bool, error) {
	path := r.lockPath(name)
	for attempts := 0; attempts < 8; attempts++ {
		file, err := os.OpenFile(path, os.O_RDWR|syscall.O_CLOEXEC, 0)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if errors.Is(err, syscall.EWOULDBLOCK) {
			matched, statErr := sameOpenFile(file, path)
			_ = file.Close()
			if statErr == nil && matched {
				return true, nil
			}
			continue
		}
		if err != nil {
			_ = file.Close()
			return false, err
		}
		matched, statErr := sameOpenFile(file, path)
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		if statErr == nil && matched {
			return false, nil
		}
	}
	return false, fmt.Errorf("runtime lock %q kept changing", name)
}

func (r *Registry) lockPath(name string) string     { return filepath.Join(r.root, name+".lock") }
func (r *Registry) metadataPath(name string) string { return filepath.Join(r.root, name+".json") }
func (r *Registry) ownershipPath(name string) string {
	return filepath.Join(r.root, name+".ownership.json")
}
