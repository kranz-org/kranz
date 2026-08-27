// Package mcp implements Kranz's foreground stdio delivery adapter. It owns
// JSON-RPC framing and an explicit resource/tool allow-list, while every
// runtime operation is delegated to app.API.
package mcp

import (
	"encoding/json"

	"github.com/kranz-org/kranz/internal/runtime"
)

const (
	SchemaVersion   = 1
	ProtocolVersion = "2025-11-25"
)

var supportedProtocolVersions = map[string]bool{
	"2025-11-25": true,
	"2025-06-18": true,
	"2025-03-26": true,
	"2024-11-05": true,
}

type SessionIdentity struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Project         string `json:"project"`
	Directory       string `json:"directory"`
	RuntimeMode     string `json:"runtime_mode"`
	ConnectionMode  string `json:"connection_mode"`
	OwnerKind       string `json:"owner_kind"`
	OwnerReason     string `json:"owner_reason,omitempty"`
	KranzVersion    string `json:"kranz_version"`
	ProtocolVersion int    `json:"runtime_protocol_version"`
}

func SessionFromMetadata(metadata runtime.SessionMetadata, connectionMode string) SessionIdentity {
	owner := metadata.Mode
	if connectionMode == "owner" {
		owner = "mcp"
	}
	return SessionIdentity{ID: metadata.ID, Name: metadata.Name, Project: metadata.Project, Directory: metadata.Directory, RuntimeMode: metadata.Mode, ConnectionMode: connectionMode, OwnerKind: owner, KranzVersion: metadata.KranzVersion, ProtocolVersion: metadata.ProtocolMax}
}

type CausalError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Hint    string         `json:"hint,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

type ResultEnvelope struct {
	SchemaVersion int             `json:"schema_version"`
	Generation    uint64          `json:"generation"`
	Session       SessionIdentity `json:"session"`
	Data          any             `json:"data,omitempty"`
	Error         *CausalError    `json:"error,omitempty"`
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type toolDefinition struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
	handler      toolHandler
}

// envelopeSchema describes the result envelope every tool returns. Declaring
// it means a client does not have to discover the shape of a Kranz answer by
// running a call and reading what came back.
func envelopeSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"schema_version": map[string]any{"type": "integer", "description": "Envelope contract version."},
			"generation":     map[string]any{"type": "integer", "description": "Configuration generation the answer was read at."},
			"session":        map[string]any{"type": "object", "description": "Runtime and session identity."},
			"data":           map[string]any{"description": "Tool-specific payload; absent on failure."},
			"error": map[string]any{"type": "object", "description": "Causal failure, absent on success.", "properties": map[string]any{
				"code":    map[string]any{"type": "string"},
				"message": map[string]any{"type": "string"},
				"hint":    map[string]any{"type": "string"},
				"details": map[string]any{"type": "object"},
			}},
		},
		"required": []string{"schema_version", "generation", "session"},
	}
}

type resourceDefinition struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
	handler     resourceHandler
}
