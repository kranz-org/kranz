package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/kranz-org/kranz/internal/app"
)

const maxMessageSize = 16 * 1024 * 1024

type toolHandler func(context.Context, json.RawMessage) ResultEnvelope
type resourceHandler func(context.Context) ResultEnvelope

type pendingRequest struct {
	cancel context.CancelFunc
}

type Server struct {
	api     app.API
	session SessionIdentity
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer

	writeMu     sync.Mutex
	requestWG   sync.WaitGroup
	pendingMu   sync.Mutex
	pending     map[string]*pendingRequest
	initialized atomic.Bool

	tools         map[string]toolDefinition
	toolOrder     []string
	resources     map[string]resourceDefinition
	resourceOrder []string

	runtimeListOverride   func(context.Context) ([]RuntimeEntry, error)
	selectorMatchOverride func(context.Context, string) ([]RuntimeSelectorMatch, error)
}

func NewServer(api app.API, session SessionIdentity, stdin io.Reader, stdout, stderr io.Writer) *Server {
	server := &Server{api: api, session: session, stdin: stdin, stdout: stdout, stderr: stderr, pending: map[string]*pendingRequest{}}
	server.installResources()
	server.installTools()
	return server
}

// Serve runs newline-delimited UTF-8 JSON-RPC. stdout is touched only by
// writeResponse; diagnostics, including malformed notification details, go to
// stderr so MCP framing cannot be corrupted.
func (s *Server) Serve(ctx context.Context) error {
	lines := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(s.stdin)
		scanner.Buffer(make([]byte, 64*1024), maxMessageSize)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-ctx.Done():
				readErr <- ctx.Err()
				return
			}
		}
		readErr <- scanner.Err()
	}()
	for {
		select {
		case <-ctx.Done():
			s.cancelAll()
			s.requestWG.Wait()
			return ctx.Err()
		case err := <-readErr:
			if err == nil {
				s.cancelAll()
				s.requestWG.Wait()
				return nil
			}
			s.cancelAll()
			s.requestWG.Wait()
			return err
		case line := <-lines:
			if len(line) == 0 {
				continue
			}
			var message rpcMessage
			if err := json.Unmarshal(line, &message); err != nil {
				_ = s.writeResponse(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "Parse error"}})
				continue
			}
			if message.JSONRPC != "2.0" || message.Method == "" {
				if len(message.ID) > 0 {
					_ = s.writeProtocolError(message.ID, -32600, "Invalid Request", nil)
				}
				continue
			}
			if len(message.ID) == 0 {
				s.handleNotification(message)
				continue
			}
			s.requestWG.Add(1)
			go func() {
				defer s.requestWG.Done()
				s.handleRequest(ctx, message)
			}()
		}
	}
}

func (s *Server) handleNotification(message rpcMessage) {
	switch message.Method {
	case "notifications/initialized":
		s.initialized.Store(true)
	case "notifications/cancelled":
		var params struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(message.Params, &params) != nil || len(params.RequestID) == 0 {
			return
		}
		s.pendingMu.Lock()
		pending := s.pending[requestKey(params.RequestID)]
		s.pendingMu.Unlock()
		if pending != nil {
			pending.cancel()
		}
	}
}

// handleRequest answers one request. It recovers from a handler panic because
// this process may also be the supervisor: a nil dereference inside a tool
// must fail that one call, not take every managed service down with it.
func (s *Server) handleRequest(parent context.Context, message rpcMessage) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if s.stderr != nil {
				_, _ = fmt.Fprintf(s.stderr, "Kranz MCP handler panic in %s: %v\n%s\n", message.Method, recovered, debug.Stack())
			}
			_ = s.writeProtocolError(message.ID, -32603, "Internal error", map[string]any{"method": message.Method})
		}
	}()
	key := requestKey(message.ID)
	ctx, cancel := context.WithCancel(parent)
	pending := &pendingRequest{cancel: cancel}
	s.pendingMu.Lock()
	s.pending[key] = pending
	s.pendingMu.Unlock()
	defer func() { cancel(); s.pendingMu.Lock(); delete(s.pending, key); s.pendingMu.Unlock() }()

	var result any
	var protocolErr *rpcError
	switch message.Method {
	case "initialize":
		result, protocolErr = s.initialize(message.Params)
	case "ping":
		result = map[string]any{}
	default:
		if !s.initialized.Load() {
			protocolErr = &rpcError{Code: -32002, Message: "Server is not initialized"}
			break
		}
		switch message.Method {
		case "tools/list":
			result = map[string]any{"tools": s.listTools()}
		case "tools/call":
			result, protocolErr = s.callTool(ctx, message.Params)
		case "resources/list":
			result = map[string]any{"resources": s.listResources()}
		case "resources/templates/list":
			// Kranz publishes fixed resource URIs. Answering with an empty
			// list is the honest response; a method-not-found error only makes
			// clients that probe on connect log a failure they cannot act on.
			result = map[string]any{"resourceTemplates": []any{}}
		case "resources/read":
			result, protocolErr = s.readResource(ctx, message.Params)
		default:
			protocolErr = &rpcError{Code: -32601, Message: "Method not found"}
		}
	}
	_ = s.writeResponse(rpcResponse{JSONRPC: "2.0", ID: message.ID, Result: result, Error: protocolErr})
}

func (s *Server) initialize(raw json.RawMessage) (any, *rpcError) {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || params.ProtocolVersion == "" {
		return nil, &rpcError{Code: -32602, Message: "Invalid initialize parameters"}
	}
	negotiated := ProtocolVersion
	if supportedProtocolVersions[params.ProtocolVersion] {
		negotiated = params.ProtocolVersion
	}
	s.initialized.Store(true)
	return map[string]any{
		"protocolVersion": negotiated,
		"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}, "resources": map[string]any{"subscribe": false, "listChanged": false}},
		"serverInfo":      map[string]any{"name": "kranz", "title": "Kranz project runtime", "version": s.session.KranzVersion},
		"instructions":    "Inspect and operate the selected live Kranz runtime. Use explicit start/stop operations and honor confirmation_required results.",
	}, nil
}

func (s *Server) callTool(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || params.Name == "" {
		return nil, &rpcError{Code: -32602, Message: "Invalid tool call parameters"}
	}
	definition, ok := s.tools[params.Name]
	if !ok {
		return nil, &rpcError{Code: -32602, Message: fmt.Sprintf("Unknown tool %q", params.Name)}
	}
	if len(params.Arguments) == 0 {
		params.Arguments = json.RawMessage("{}")
	}
	envelope := definition.handler(ctx, params.Arguments)
	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: "encode tool result", Data: err.Error()}
	}
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": string(payload)}}, "structuredContent": envelope, "isError": envelope.Error != nil}, nil
}

func (s *Server) readResource(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || params.URI == "" {
		return nil, &rpcError{Code: -32602, Message: "Invalid resource parameters"}
	}
	definition, ok := s.resources[params.URI]
	if !ok {
		return nil, &rpcError{Code: -32002, Message: "Resource not found", Data: map[string]any{"uri": params.URI}}
	}
	envelope := definition.handler(ctx)
	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: "encode resource", Data: err.Error()}
	}
	return map[string]any{"contents": []any{map[string]any{"uri": params.URI, "mimeType": "application/json", "text": string(payload)}}}, nil
}

func (s *Server) listTools() []toolDefinition {
	result := make([]toolDefinition, 0, len(s.toolOrder))
	for _, name := range s.toolOrder {
		result = append(result, s.tools[name])
	}
	return result
}

func (s *Server) listResources() []resourceDefinition {
	result := make([]resourceDefinition, 0, len(s.resourceOrder))
	for _, uri := range s.resourceOrder {
		result = append(result, s.resources[uri])
	}
	return result
}

// requestKey normalizes a JSON-RPC id for pending-request lookup. The raw
// bytes of an id are not a reliable key: a client may write 1 in the request
// and 1.0 in the cancellation and mean the same request.
func requestKey(id json.RawMessage) string {
	var value any
	if err := json.Unmarshal(id, &value); err != nil {
		return string(id)
	}
	switch typed := value.(type) {
	case float64:
		return "n:" + strconv.FormatFloat(typed, 'f', -1, 64)
	case string:
		return "s:" + typed
	default:
		return string(id)
	}
}

func (s *Server) writeProtocolError(id json.RawMessage, code int, message string, data any) error {
	return s.writeResponse(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message, Data: data}})
}

func (s *Server) writeResponse(response rpcResponse) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	encoder := json.NewEncoder(s.stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(response); err != nil {
		if s.stderr != nil {
			_, _ = fmt.Fprintf(s.stderr, "Kranz MCP write error: %v\n", err)
		}
		return err
	}
	return nil
}

func (s *Server) cancelAll() {
	s.pendingMu.Lock()
	pending := make([]*pendingRequest, 0, len(s.pending))
	for _, request := range s.pending {
		pending = append(pending, request)
	}
	s.pendingMu.Unlock()
	for _, request := range pending {
		request.cancel()
	}
}
