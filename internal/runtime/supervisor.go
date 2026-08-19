package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kranz-org/kranz/internal/app"
	"github.com/kranz-org/kranz/internal/config"
)

// backgroundReloadInterval is how often the Supervisor's own watcher checks
// the configuration when no client is connected to drive Reload itself. It
// only runs in that gap — see runReloadWatcher — so this can be short without
// wasting work; Local.Reload's own one-second debounce is still the real
// rate limit.
const backgroundReloadInterval = 300 * time.Millisecond

// Supervisor hosts one app.API implementation (always a *app.Local today)
// and answers requests from Client connections over a Unix socket. It is the
// runtime's supervisor host: the README's "Runtime supervisor является
// единственным владельцем process-supervised сервисов" — Supervisor does not
// duplicate that ownership, it just exposes the one Local instance it wraps
// to more than one process.
type Supervisor struct {
	local *app.Local

	listener   *net.UnixListener
	socketPath string

	clients atomic.Int64

	stopWatch chan struct{}
	watchDone chan struct{}

	connWG sync.WaitGroup

	closeOnce sync.Once
	closed    chan struct{}
}

// NewSupervisor wraps local. local must not be driven by any other caller
// once Serve starts — Supervisor becomes its sole owner, matching how
// service.Manager already requires a single owner for process-supervised
// services.
func NewSupervisor(local *app.Local) *Supervisor {
	return &Supervisor{
		local:     local,
		stopWatch: make(chan struct{}),
		watchDone: make(chan struct{}),
		closed:    make(chan struct{}),
	}
}

// Serve binds socketPath, starts the background reload watcher, and accepts
// connections until Close is called or the listener otherwise fails. It
// blocks; callers run it in its own goroutine, after Listen has bound the
// socket synchronously. Splitting bind from accept this way means the
// listener field is only ever written before any goroutine could concurrently
// read it from Close — a Serve that bound the socket itself, in the goroutine
// Close might already be racing against, could not make that guarantee.
func (s *Supervisor) Listen(socketPath string) error {
	listener, err := listenUnix(socketPath)
	if err != nil {
		return err
	}
	s.listener = listener
	s.socketPath = socketPath
	return nil
}

// Serve accepts connections until Close is called or the listener otherwise
// fails. It blocks; callers run it in its own goroutine, after a successful
// Listen.
func (s *Supervisor) Serve() error {
	if s.listener == nil {
		return errors.New("runtime: Serve called before Listen")
	}
	go s.runReloadWatcher()

	for {
		conn, err := s.listener.AcceptUnix()
		if err != nil {
			select {
			case <-s.closed:
				s.connWG.Wait()
				return nil
			default:
				return fmt.Errorf("accept connection: %w", err)
			}
		}
		s.connWG.Add(1)
		go func() {
			defer s.connWG.Done()
			s.handleConn(conn)
		}()
	}
}

// Close stops accepting connections, stops the reload watcher, and closes
// the listener. It does not shut down the wrapped Local — a client's
// "shutdown" request does that explicitly, so a Supervisor going away is
// distinguishable from a runtime being torn down.
func (s *Supervisor) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
		close(s.stopWatch)
	})
	var err error
	if s.listener != nil {
		err = s.listener.Close()
	}
	<-s.watchDone
	return err
}

// runReloadWatcher drives configuration reload while no client is connected,
// so a background session that nobody is attached to still notices a config
// change (README: "background runtime reload работает без TUI"). While a
// client is connected, that client's own periodic Reload(false) calls do the
// same debounced work; letting both race to be "the one that observed the
// change" would make the loser silently miss the notification its caller
// expects, so the watcher steps back rather than compete with it.
func (s *Supervisor) runReloadWatcher() {
	defer close(s.watchDone)
	ticker := time.NewTicker(backgroundReloadInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopWatch:
			return
		case <-ticker.C:
			if s.clients.Load() == 0 {
				_, _ = s.local.Reload(false)
			}
		}
	}
}

func (s *Supervisor) handleConn(conn *net.UnixConn) {
	defer func() { _ = conn.Close() }()
	if err := verifyPeerUser(conn); err != nil {
		return
	}

	c := newCodec(conn)
	if !s.handshake(c) {
		return
	}

	s.clients.Add(1)
	defer s.clients.Add(-1)

	pendingMu := sync.Mutex{}
	pending := make(map[uint64]context.CancelFunc)
	leases := newConnectionLeases()
	var wg sync.WaitGroup

	for {
		msg, err := c.receive()
		if err != nil {
			break
		}
		switch msg.Type {
		case messageCancel:
			pendingMu.Lock()
			if cancel, ok := pending[msg.ID]; ok {
				cancel()
			}
			pendingMu.Unlock()
		case messageRequest:
			ctx, cancel := context.WithCancel(context.Background())
			pendingMu.Lock()
			pending[msg.ID] = cancel
			pendingMu.Unlock()
			wg.Add(1)
			go func(msg envelope) {
				defer wg.Done()
				defer func() {
					pendingMu.Lock()
					delete(pending, msg.ID)
					pendingMu.Unlock()
					cancel()
				}()
				s.dispatch(ctx, c, msg, leases)
			}(msg)
		default:
			// A response or an unrecognized type from a client is not
			// something the server ever expects; ignore it rather than
			// tearing down the connection over a stray message.
		}
	}
	pendingMu.Lock()
	for _, cancel := range pending {
		cancel()
	}
	pendingMu.Unlock()
	wg.Wait()
	leases.releaseAll(s.local)
}

// connectionLeases ties an out-of-process interactive reservation to the
// client connection that acquired it. A disconnected client cannot complete
// the lease, so teardown records a failed result and frees the action owner for
// another client instead of leaving it busy until the supervisor exits.
type connectionLeases struct {
	mu      sync.Mutex
	byToken map[string]config.ActionID
}

func newConnectionLeases() *connectionLeases {
	return &connectionLeases{byToken: make(map[string]config.ActionID)}
}

func (l *connectionLeases) add(id config.ActionID, token string) {
	l.mu.Lock()
	l.byToken[token] = id
	l.mu.Unlock()
}

func (l *connectionLeases) remove(token string) {
	l.mu.Lock()
	delete(l.byToken, token)
	l.mu.Unlock()
}

func (l *connectionLeases) owns(id config.ActionID, token string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	ownedID, ok := l.byToken[token]
	return ok && ownedID == id
}

func (l *connectionLeases) releaseAll(local *app.Local) {
	l.mu.Lock()
	leases := l.byToken
	l.byToken = make(map[string]config.ActionID)
	l.mu.Unlock()
	for token, id := range leases {
		_, _ = local.CompleteInteractiveAction(
			id, token, errors.New("interactive action client disconnected"), -1, 0,
		)
	}
}

// handshake answers the client's hello request. It is the one request the
// server decodes before any method dispatch table applies, matching the
// README's requirement that hello, inspect, and down stay readable across
// protocol versions.
func (s *Supervisor) handshake(c *codec) bool {
	msg, err := c.receive()
	if err != nil || msg.Type != messageRequest || msg.Method != methodHello {
		return false
	}
	var req helloRequest
	if err := json.Unmarshal(msg.Body, &req); err != nil {
		return false
	}
	if req.ProtocolMin > protocolVersion || req.ProtocolMax < protocolVersion {
		payload := errorPayload{
			Kind: errorVersionMismatch,
			Message: fmt.Sprintf(
				"Kranz: session speaks protocol %d, this client (%s) speaks %d. Upgrade kranz, or stop the session with the matching binary.",
				protocolVersion, req.ClientVersion, req.ProtocolMax,
			),
			ServerProtocol: protocolVersion,
			ServerVersion:  kranzVersion(),
		}
		body, _ := json.Marshal(payload)
		_ = c.send(envelope{Type: messageError, ID: msg.ID, Body: body})
		return false
	}
	resp := helloResponse{
		ProtocolMin: protocolVersion, ProtocolMax: protocolVersion,
		ServerVersion: kranzVersion(), AgreedProtocol: protocolVersion,
	}
	body, err := json.Marshal(resp)
	if err != nil {
		return false
	}
	return c.send(envelope{Type: messageResponse, ID: msg.ID, Body: body}) == nil
}

func (s *Supervisor) dispatch(ctx context.Context, c *codec, msg envelope, leases *connectionLeases) {
	entry, ok := handlers[msg.Method]
	if !ok {
		body, _ := json.Marshal(errorPayload{Kind: errorGeneric, Message: fmt.Sprintf("unknown method %q", msg.Method)})
		_ = c.send(envelope{Type: messageError, ID: msg.ID, Body: body})
		return
	}
	if err := validateInteractiveLease(msg, leases); err != nil {
		body, _ := json.Marshal(errorPayload{Kind: errorGeneric, Message: err.Error()})
		_ = c.send(envelope{Type: messageError, ID: msg.ID, Body: body})
		return
	}
	result, err := entry(ctx, s.local, msg.Body)
	if err != nil {
		body, marshalErr := json.Marshal(encodeError(err))
		if marshalErr != nil {
			body, _ = json.Marshal(errorPayload{Kind: errorGeneric, Message: err.Error()})
		}
		_ = c.send(envelope{Type: messageError, ID: msg.ID, Body: body})
		return
	}
	if err := trackInteractiveLease(msg, result, leases); err != nil {
		body, _ := json.Marshal(errorPayload{Kind: errorGeneric, Message: err.Error()})
		_ = c.send(envelope{Type: messageError, ID: msg.ID, Body: body})
		return
	}
	body, err := json.Marshal(result)
	if err != nil {
		body, _ = json.Marshal(errorPayload{Kind: errorGeneric, Message: fmt.Sprintf("marshal response: %v", err)})
		_ = c.send(envelope{Type: messageError, ID: msg.ID, Body: body})
		return
	}
	_ = c.send(envelope{Type: messageResponse, ID: msg.ID, Body: body})
}

func validateInteractiveLease(msg envelope, leases *connectionLeases) error {
	if msg.Method != methodCompleteInteractiveAction {
		return nil
	}
	var req completeInteractiveActionRequest
	if err := json.Unmarshal(msg.Body, &req); err != nil {
		return fmt.Errorf("validate interactive lease: %w", err)
	}
	if !leases.owns(req.ID, req.Lease) {
		return errors.New("interactive lease is not owned by this client")
	}
	return nil
}

func trackInteractiveLease(msg envelope, result any, leases *connectionLeases) error {
	switch msg.Method {
	case methodAcquireInteractiveAction:
		var req actionIDRequest
		if err := json.Unmarshal(msg.Body, &req); err != nil {
			return fmt.Errorf("track acquired interactive action: %w", err)
		}
		response, ok := result.(acquireInteractiveActionResponse)
		if !ok || response.Lease == "" {
			return errors.New("track acquired interactive action: invalid response")
		}
		leases.add(req.ID, response.Lease)
	case methodCompleteInteractiveAction:
		var req completeInteractiveActionRequest
		if err := json.Unmarshal(msg.Body, &req); err != nil {
			return fmt.Errorf("track completed interactive action: %w", err)
		}
		leases.remove(req.Lease)
	}
	return nil
}

// kranzVersion reports the build version a Supervisor advertises during
// handshake. Streams 3+ wire this to the same version main.go already
// injects with -ldflags; for now, "dev" is enough to exercise the field.
var kranzVersion = func() string { return "dev" }
