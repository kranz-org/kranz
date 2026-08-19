package runtime

import "encoding/json"

// protocolVersion is both protocol_min and protocol_max Kranz speaks today.
// The README's versioning contract (a session advertises a [min, max] range
// so a client and an older or newer server can find a compatible overlap)
// only has one build to negotiate between right now; a later stream that
// changes the wire format widens the range instead of just bumping this
// constant.
const protocolVersion = 1

// messageType identifies what an envelope carries. The envelope shape itself
// (v, type, id, method, body) is deliberately small and stable so a peer can
// always decode enough of it to at least report a version mismatch, even
// when it does not understand the negotiated protocol's method set — the
// README calls this out explicitly for hello, inspect, and down.
type messageType string

const (
	messageRequest  messageType = "request"
	messageResponse messageType = "response"
	messageError    messageType = "error"
	messageCancel   messageType = "cancel"
)

// envelope is the fixed outer shape of every frame's JSON payload. body is
// left as a raw message so decoding it into a concrete request or response
// type can happen only once the method is known.
type envelope struct {
	V      int             `json:"v"`
	Type   messageType     `json:"type"`
	ID     uint64          `json:"id"`
	Method string          `json:"method,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// helloRequest is the bootstrap payload a client sends immediately after
// dialing, before any other method is available.
type helloRequest struct {
	ProtocolMin   int    `json:"protocol_min"`
	ProtocolMax   int    `json:"protocol_max"`
	ClientVersion string `json:"client_version"`
}

// helloResponse is the server's answer once it has picked a protocol version
// in the overlap of its own range and the client's.
type helloResponse struct {
	ProtocolMin    int    `json:"protocol_min"`
	ProtocolMax    int    `json:"protocol_max"`
	ServerVersion  string `json:"server_version"`
	AgreedProtocol int    `json:"agreed_protocol"`
}

// errorKind distinguishes the one error shape a client must reconstruct as a
// specific Go type (portConflict, so internal/ui can errors.As it into a
// modal) from every other error, which crosses the wire as plain text.
type errorKind string

const (
	errorGeneric         errorKind = "generic"
	errorVersionMismatch errorKind = "version_mismatch"
	errorPortConflict    errorKind = "port_conflict"
)

// errorPayload is the body of a messageError envelope.
type errorPayload struct {
	Kind    errorKind `json:"kind"`
	Message string    `json:"message"`

	// Populated only when Kind == errorVersionMismatch, mirroring the
	// README's example text: "session %q speaks protocol %d, this client
	// (%s) speaks %d."
	ServerProtocol int    `json:"server_protocol,omitempty"`
	ServerVersion  string `json:"server_version,omitempty"`

	// Populated only when Kind == errorPortConflict, mirroring
	// app.PortConflictError's fields.
	Service      string `json:"service,omitempty"`
	Port         int    `json:"port,omitempty"`
	PID          int    `json:"pid,omitempty"`
	Process      string `json:"process,omitempty"`
	Command      string `json:"command,omitempty"`
	OwnerService string `json:"ownerService,omitempty"`
	External     bool   `json:"external,omitempty"`
}
