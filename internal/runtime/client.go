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
	"github.com/kranz-org/kranz/internal/port"
)

// cancelGrace bounds how long a canceled call waits for the server's actual
// response before giving up on it. The server always answers a canceled
// request (with a response or a "context canceled" error) once its handler
// goroutine returns; this is a backstop against a wedged connection, not the
// expected path.
const cancelGrace = 5 * time.Second

// Client implements app.API by encoding each call as a request over one
// persistent connection to a Supervisor and decoding the response. A
// delivery surface cannot tell a Client from a Local except by how it was
// constructed — that is the whole point of routing every call through
// app.API instead of a concrete type.
type Client struct {
	conn *net.UnixConn
	c    *codec

	idSeq atomic.Uint64

	pendingMu sync.Mutex
	pending   map[uint64]chan envelope

	readErr atomic.Value // error
}

// Dial connects to a Supervisor listening on socketPath and performs the
// hello handshake. clientVersion is advertised to the server for its own
// diagnostics; it does not affect protocol negotiation in this stream, since
// client and server are always built from the same binary.
func Dial(socketPath, clientVersion string) (*Client, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", socketPath, err)
	}
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("connection to %s was not a Unix socket connection", socketPath)
	}
	client := &Client{
		conn:    unixConn,
		c:       newCodec(unixConn),
		pending: make(map[uint64]chan envelope),
	}

	helloBody, err := json.Marshal(helloRequest{ProtocolMin: protocolVersion, ProtocolMax: protocolVersion, ClientVersion: clientVersion})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := client.c.send(envelope{Type: messageRequest, ID: 0, Method: methodHello, Body: helloBody}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("send hello: %w", err)
	}
	reply, err := client.c.receive()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("receive hello response: %w", err)
	}
	if reply.Type == messageError {
		var payload errorPayload
		_ = json.Unmarshal(reply.Body, &payload)
		_ = conn.Close()
		return nil, decodeError(payload)
	}
	if reply.Type != messageResponse {
		_ = conn.Close()
		return nil, fmt.Errorf("unexpected hello response type %q", reply.Type)
	}
	var hello helloResponse
	if err := json.Unmarshal(reply.Body, &hello); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("decode hello response: %w", err)
	}
	if hello.ProtocolMin > protocolVersion || hello.ProtocolMax < protocolVersion || hello.AgreedProtocol != protocolVersion {
		_ = conn.Close()
		return nil, &VersionMismatchError{
			Message: fmt.Sprintf(
				"Kranz: session speaks protocol %d, this client (%s) speaks %d. Upgrade kranz, or stop the session with the matching binary.",
				hello.AgreedProtocol, clientVersion, protocolVersion,
			),
			ServerProtocol: hello.AgreedProtocol,
			ServerVersion:  hello.ServerVersion,
		}
	}

	go client.readLoop()
	return client, nil
}

// Close closes the underlying connection. In-flight calls fail with the
// connection error once the read loop observes the close.
func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) readLoop() {
	for {
		msg, err := c.c.receive()
		if err != nil {
			c.readErr.Store(err)
			c.failAllPending(err)
			return
		}
		c.pendingMu.Lock()
		ch, ok := c.pending[msg.ID]
		c.pendingMu.Unlock()
		if ok {
			ch <- msg
		}
	}
}

func (c *Client) failAllPending(err error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	body, _ := json.Marshal(errorPayload{Kind: errorGeneric, Message: fmt.Sprintf("connection lost: %v", err)})
	for id, ch := range c.pending {
		ch <- envelope{Type: messageError, ID: id, Body: body}
	}
}

// roundTrip sends one request and returns its response body, or the
// reconstructed error if the server answered with one.
func (c *Client) roundTrip(ctx context.Context, method string, reqBody []byte) (json.RawMessage, error) {
	id := c.idSeq.Add(1)
	replyCh := make(chan envelope, 1)
	c.pendingMu.Lock()
	c.pending[id] = replyCh
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	if err := c.c.send(envelope{Type: messageRequest, ID: id, Method: method, Body: reqBody}); err != nil {
		return nil, fmt.Errorf("send %s: %w", method, err)
	}

	select {
	case reply := <-replyCh:
		return decodeReply(reply)
	case <-ctx.Done():
		_ = c.c.send(envelope{Type: messageCancel, ID: id})
		select {
		case reply := <-replyCh:
			return decodeReply(reply)
		case <-time.After(cancelGrace):
			return nil, ctx.Err()
		}
	}
}

func decodeReply(reply envelope) (json.RawMessage, error) {
	if reply.Type == messageError {
		var payload errorPayload
		if err := json.Unmarshal(reply.Body, &payload); err != nil {
			return nil, fmt.Errorf("decode error response: %w", err)
		}
		return nil, decodeError(payload)
	}
	return reply.Body, nil
}

func call[Req any, Resp any](c *Client, ctx context.Context, method string, req Req) (Resp, error) {
	var zero Resp
	body, err := json.Marshal(req)
	if err != nil {
		return zero, fmt.Errorf("encode %s request: %w", method, err)
	}
	respBody, err := c.roundTrip(ctx, method, body)
	if err != nil {
		return zero, err
	}
	var resp Resp
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &resp); err != nil {
			return zero, fmt.Errorf("decode %s response: %w", method, err)
		}
	}
	return resp, nil
}

// The methods below implement app.API. Each just shapes a request, calls
// call, and unwraps the response — the RPC mechanics live once in
// roundTrip/call above, matching handlers.go's server-side symmetry.

func (c *Client) Project() app.ProjectSnapshot {
	resp, _ := call[emptyRequest, app.ProjectSnapshot](c, context.Background(), methodProject, emptyRequest{})
	return resp
}

func (c *Client) Config() *config.Config {
	resp, _ := call[emptyRequest, *config.Config](c, context.Background(), methodConfig, emptyRequest{})
	return resp
}

func (c *Client) Reload(force bool) (app.ReloadResult, error) {
	return call[reloadRequest, app.ReloadResult](c, context.Background(), methodReload, reloadRequest{Force: force})
}

func (c *Client) AcknowledgeExternalWrite() {
	_, _ = call[emptyRequest, emptyResponse](c, context.Background(), methodAcknowledgeExternalWrite, emptyRequest{})
}

func (c *Client) Services() []*app.ServiceSnapshot {
	resp, _ := call[emptyRequest, servicesResponse](c, context.Background(), methodServices, emptyRequest{})
	return resp.Services
}

func (c *Client) Service(name string) (*app.ServiceSnapshot, bool) {
	resp, err := call[nameRequest, serviceResponse](c, context.Background(), methodService, nameRequest{Name: name})
	if err != nil {
		return nil, false
	}
	return resp.Service, resp.Ok
}

func (c *Client) Tags() []string {
	resp, _ := call[emptyRequest, tagsResponse](c, context.Background(), methodTags, emptyRequest{})
	return resp.Tags
}

func (c *Client) ManagedServiceForPID(pid int) string {
	resp, _ := call[pidRequest, serviceNameResponse](c, context.Background(), methodManagedServiceForPID, pidRequest{PID: pid})
	return resp.Name
}

func (c *Client) StartConfirmationNames(names []string, includeDependencies bool) []string {
	resp, _ := call[startConfirmationNamesRequest, namesResponse](c, context.Background(), methodStartConfirmationNames,
		startConfirmationNamesRequest{Names: names, IncludeDependencies: includeDependencies})
	return resp.Names
}

func (c *Client) RequiresStopConfirmation(names []string) bool {
	resp, _ := call[namesRequest, boolResponse](c, context.Background(), methodRequiresStopConfirmation, namesRequest{Names: names})
	return resp.Value
}

func (c *Client) AffectedServices(name string) []string {
	resp, _ := call[nameRequest, namesResponse](c, context.Background(), methodAffectedServices, nameRequest{Name: name})
	return resp.Names
}

func (c *Client) ShutdownPlan() app.ShutdownPlan {
	resp, _ := call[emptyRequest, shutdownPlanResponse](c, context.Background(), methodShutdownPlan, emptyRequest{})
	return resp.Plan
}

func (c *Client) StartServicesContext(ctx context.Context, names []string) error {
	_, err := call[namesRequest, emptyResponse](c, ctx, methodStartServicesContext, namesRequest{Names: names})
	return err
}

func (c *Client) StopServices(names []string) error {
	_, err := call[namesRequest, emptyResponse](c, context.Background(), methodStopServices, namesRequest{Names: names})
	return err
}

func (c *Client) ForceStopServices(names []string) error {
	_, err := call[namesRequest, emptyResponse](c, context.Background(), methodForceStopServices, namesRequest{Names: names})
	return err
}

func (c *Client) ForceStartServices(names []string) error {
	_, err := call[namesRequest, emptyResponse](c, context.Background(), methodForceStartServices, namesRequest{Names: names})
	return err
}

func (c *Client) StopAll() error {
	_, err := call[emptyRequest, emptyResponse](c, context.Background(), methodStopAll, emptyRequest{})
	return err
}

func (c *Client) RestartAll() error {
	_, err := call[emptyRequest, emptyResponse](c, context.Background(), methodRestartAll, emptyRequest{})
	return err
}

func (c *Client) RestartService(name string) error {
	_, err := call[nameRequest, emptyResponse](c, context.Background(), methodRestartService, nameRequest{Name: name})
	return err
}

func (c *Client) HasRunningServices() bool {
	resp, _ := call[emptyRequest, boolResponse](c, context.Background(), methodHasRunningServices, emptyRequest{})
	return resp.Value
}

func (c *Client) ProjectExitRequested() (bool, int) {
	resp, _ := call[emptyRequest, projectExitRequestedResponse](c, context.Background(), methodProjectExitRequested, emptyRequest{})
	return resp.Requested, resp.Code
}

func (c *Client) Shutdown() error {
	_, err := call[emptyRequest, emptyResponse](c, context.Background(), methodShutdown, emptyRequest{})
	return errors.Join(err, c.Close())
}

func (c *Client) RunAction(ctx context.Context, id config.ActionID) (app.ActionResult, error) {
	return call[actionIDRequest, app.ActionResult](c, ctx, methodRunAction, actionIDRequest{ID: id})
}

func (c *Client) ActionState(id config.ActionID) (app.ActionResult, bool) {
	resp, err := call[actionIDRequest, actionStateResponse](c, context.Background(), methodActionState, actionIDRequest{ID: id})
	if err != nil {
		return app.ActionResult{}, false
	}
	return resp.Result, resp.Ok
}

func (c *Client) CancelAction(id config.ActionID) bool {
	resp, _ := call[actionIDRequest, cancelActionResponse](c, context.Background(), methodCancelAction, actionIDRequest{ID: id})
	return resp.Cancelled
}

func (c *Client) AcquireInteractiveAction(id config.ActionID) (config.Action, string, error) {
	resp, err := call[actionIDRequest, acquireInteractiveActionResponse](c, context.Background(), methodAcquireInteractiveAction, actionIDRequest{ID: id})
	return resp.Action, resp.Lease, err
}

func (c *Client) CompleteInteractiveAction(id config.ActionID, lease string, execErr error, exitCode, pid int) (app.ActionResult, error) {
	req := completeInteractiveActionRequest{ID: id, Lease: lease, ExitCode: exitCode, PID: pid}
	if execErr != nil {
		req.ExecErr = execErr.Error()
	}
	return call[completeInteractiveActionRequest, app.ActionResult](c, context.Background(), methodCompleteInteractiveAction, req)
}

func (c *Client) Logs(name string) []config.LogEntry {
	resp, _ := call[nameRequest, logsResponse](c, context.Background(), methodLogs, nameRequest{Name: name})
	return resp.Entries
}

func (c *Client) ClearLogs(name string) {
	_, _ = call[nameRequest, emptyResponse](c, context.Background(), methodClearLogs, nameRequest{Name: name})
}

func (c *Client) MarkLogsRead(name string) {
	_, _ = call[nameRequest, emptyResponse](c, context.Background(), methodMarkLogsRead, nameRequest{Name: name})
}

func (c *Client) HealthHistory(name string) []string {
	resp, _ := call[nameRequest, healthHistoryResponse](c, context.Background(), methodHealthHistory, nameRequest{Name: name})
	return resp.Lines
}

func (c *Client) InspectPorts(ports []int) (map[int]*config.PortInfo, error) {
	resp, err := call[inspectPortsRequest, inspectPortsResponse](c, context.Background(), methodInspectPorts, inspectPortsRequest{Ports: ports})
	return resp.Details, err
}

func (c *Client) ReleaseExternalPort(portNumber, expectedPID int) (bool, error) {
	resp, err := call[releaseExternalPortRequest, releaseExternalPortResponse](c, context.Background(), methodReleaseExternalPort,
		releaseExternalPortRequest{Port: portNumber, ExpectedPID: expectedPID})
	return resp.AlreadyFree, err
}

// SetPortChecker cannot cross the wire: port.Checker is an interface backed
// by a live implementation, not a value. It exists on app.API only for tests
// that construct a Local directly with a fake checker (see app.Options);
// tests exercising a Client-backed API should configure the fake on the
// Local passed to NewSupervisor instead.
func (c *Client) SetPortChecker(checker port.Checker) {}

func (c *Client) SetServiceStatusForTest(name string, status config.ServiceStatus) {
	_, _ = call[setServiceStatusForTestRequest, emptyResponse](c, context.Background(), methodSetServiceStatusForTest,
		setServiceStatusForTestRequest{Name: name, Status: status})
}

func (c *Client) SetServiceStateForTest(name string, state config.ServiceState) {
	_, _ = call[setServiceStateForTestRequest, emptyResponse](c, context.Background(), methodSetServiceStateForTest,
		setServiceStateForTestRequest{Name: name, State: state})
}

func (c *Client) SetServiceDesiredRunningForTest(name string, desiredRunning bool) {
	_, _ = call[setServiceDesiredRunningForTestRequest, emptyResponse](c, context.Background(), methodSetServiceDesiredRunningForTest,
		setServiceDesiredRunningForTestRequest{Name: name, DesiredRunning: desiredRunning})
}

func (c *Client) AppendLogForTest(name, line string) {
	_, _ = call[appendLogForTestRequest, emptyResponse](c, context.Background(), methodAppendLogForTest,
		appendLogForTestRequest{Name: name, Line: line})
}

func (c *Client) AppendLogAtForTest(name string, timestamp time.Time, line string) {
	_, _ = call[appendLogAtForTestRequest, emptyResponse](c, context.Background(), methodAppendLogAtForTest,
		appendLogAtForTestRequest{Name: name, Timestamp: timestamp, Line: line})
}

var _ app.API = (*Client)(nil)
