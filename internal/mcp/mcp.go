// Package mcp implements a minimal, dependency-free Model Context Protocol
// stdio server: newline-delimited JSON-RPC 2.0 on stdin/stdout.
//
// It speaks both protocol eras:
//
//   - handshake era (2025-06-18, 2025-11-25): initialize /
//     notifications/initialized, version negotiated in the handshake;
//   - stateless era (2026-07-28): server/discover, per-request
//     protocol version in _meta, no handshake required.
//
// Only the tools capability is exposed. The server never sends requests of
// its own, so client-to-server responses are ignored.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Protocol revisions this server supports, newest first. The handshake
// revisions share the tools/list + tools/call shapes we emit; 2026-07-28
// is the stateless revision (server/discover, _meta versioning).
var supportedVersions = []string{"2026-07-28", "2025-11-25", "2025-06-18"}

// latestHandshakeVersion is what initialize answers when the client asks
// for a revision we do not know (per spec: respond with the latest version
// the server supports; for the handshake method that is the latest
// handshake-era revision, not the stateless one).
const latestHandshakeVersion = "2025-11-25"

const (
	codeParseError     = -32700
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeUnsupportedVer = -32022 // UnsupportedProtocolVersionError (2026-07-28)
)

const metaProtocolVersion = "io.modelcontextprotocol/protocolVersion"
const metaServerInfo = "io.modelcontextprotocol/serverInfo"

// Tool is one callable tool. Handler returns the result text and whether it
// represents a tool-level error (isError in the MCP result). Handlers must
// honor ctx cancellation.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Handler     func(ctx context.Context, args map[string]any) (text string, isError bool)
}

// Server is an MCP stdio server. Fields must be set before Serve.
type Server struct {
	Name         string
	Title        string
	Version      string
	Instructions string
	Tools        []Tool

	wmu      sync.Mutex
	w        io.Writer
	imu      sync.Mutex
	inflight map[string]context.CancelFunc
	wg       sync.WaitGroup
}

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

// Serve reads newline-delimited JSON-RPC messages from r until EOF.
// Tool calls run concurrently (a slow analysis must not block pings);
// all other methods are answered inline. Returns nil on clean EOF.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	s.w = w
	s.inflight = map[string]context.CancelFunc{}
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			s.handleLine(ctx, line)
		}
		if err != nil {
			s.wg.Wait()
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (s *Server) handleLine(ctx context.Context, line []byte) {
	trimmed := trimSpace(line)
	if len(trimmed) == 0 {
		return
	}
	var msg rpcMsg
	if err := json.Unmarshal(trimmed, &msg); err != nil {
		s.writeError(json.RawMessage("null"), codeParseError, "parse error: not a JSON-RPC message", nil)
		return
	}
	if msg.Method == "" {
		// A response to a server-initiated request; we never send any.
		return
	}
	isRequest := len(msg.ID) > 0 && string(msg.ID) != "null"

	// Stateless-era requests carry their protocol version per request.
	if v, ok := requestedMetaVersion(msg.Params); ok && !versionSupported(v) {
		if isRequest {
			s.writeError(msg.ID, codeUnsupportedVer, "unsupported protocol version", map[string]any{
				"supported": supportedVersions,
				"requested": v,
			})
		}
		return
	}
	modern, _ := requestedMetaVersion(msg.Params)
	isModern := modern != ""

	switch msg.Method {
	case "initialize":
		if !isRequest {
			return
		}
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(msg.Params, &p)
		ver := latestHandshakeVersion
		if versionSupported(p.ProtocolVersion) && p.ProtocolVersion != "2026-07-28" {
			ver = p.ProtocolVersion
		}
		s.writeResult(msg.ID, map[string]any{
			"protocolVersion": ver,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      s.serverInfo(),
			"instructions":    s.Instructions,
		})
	case "notifications/initialized":
		// Handshake complete; nothing to do (we hold no session state).
	case "ping":
		if isRequest {
			s.writeResult(msg.ID, map[string]any{})
		}
	case "server/discover":
		if !isRequest {
			return
		}
		s.writeResult(msg.ID, map[string]any{
			"resultType":        "complete",
			"supportedVersions": supportedVersions,
			"capabilities":      map[string]any{"tools": map[string]any{}},
			"instructions":      s.Instructions,
			"_meta":             map[string]any{metaServerInfo: s.serverInfo()},
		})
	case "tools/list":
		if !isRequest {
			return
		}
		list := make([]map[string]any, 0, len(s.Tools))
		for _, t := range s.Tools {
			list = append(list, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": t.InputSchema,
			})
		}
		res := map[string]any{"tools": list}
		if isModern {
			res["_meta"] = map[string]any{metaServerInfo: s.serverInfo()}
		}
		s.writeResult(msg.ID, res)
	case "tools/call":
		if !isRequest {
			return
		}
		s.startToolCall(ctx, msg, isModern)
	case "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		json.Unmarshal(msg.Params, &p)
		s.imu.Lock()
		if cancel, ok := s.inflight[string(p.RequestID)]; ok {
			cancel()
		}
		s.imu.Unlock()
	default:
		if isRequest {
			s.writeError(msg.ID, codeMethodNotFound, fmt.Sprintf("method not found: %s", msg.Method), nil)
		}
		// Unknown notifications are ignored per JSON-RPC.
	}
}

// startToolCall dispatches a tools/call in its own goroutine so that a
// long-running analysis does not block pings or further requests. If the
// call is cancelled via notifications/cancelled, no response is written.
func (s *Server) startToolCall(ctx context.Context, msg rpcMsg, isModern bool) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(msg.Params, &p); err != nil || p.Name == "" {
		s.writeError(msg.ID, codeInvalidParams, "tools/call needs params.name and params.arguments", nil)
		return
	}
	var tool *Tool
	for i := range s.Tools {
		if s.Tools[i].Name == p.Name {
			tool = &s.Tools[i]
			break
		}
	}
	if tool == nil {
		s.writeError(msg.ID, codeInvalidParams, "unknown tool: "+p.Name, nil)
		return
	}
	args := p.Arguments
	if args == nil {
		args = map[string]any{}
	}

	callCtx, cancel := context.WithCancel(ctx)
	key := string(msg.ID)
	s.imu.Lock()
	s.inflight[key] = cancel
	s.imu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.imu.Lock()
			delete(s.inflight, key)
			s.imu.Unlock()
			cancel()
		}()
		text, isErr := tool.Handler(callCtx, args)
		if callCtx.Err() == context.Canceled {
			// Cancelled by the client: it no longer expects a response.
			return
		}
		res := map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": isErr,
		}
		if isModern {
			res["_meta"] = map[string]any{metaServerInfo: s.serverInfo()}
		}
		s.writeResult(msg.ID, res)
	}()
}

func (s *Server) serverInfo() map[string]any {
	return map[string]any{"name": s.Name, "title": s.Title, "version": s.Version}
}

func (s *Server) writeResult(id json.RawMessage, result any) {
	s.writeMsg(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *Server) writeError(id json.RawMessage, code int, message string, data any) {
	e := map[string]any{"code": code, "message": message}
	if data != nil {
		e["data"] = data
	}
	s.writeMsg(map[string]any{"jsonrpc": "2.0", "id": id, "error": e})
}

func (s *Server) writeMsg(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.w.Write(append(b, '\n'))
}

func requestedMetaVersion(params json.RawMessage) (string, bool) {
	if len(params) == 0 {
		return "", false
	}
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return "", false
	}
	raw, ok := p.Meta[metaProtocolVersion]
	if !ok {
		return "", false
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", false
	}
	return v, true
}

func versionSupported(v string) bool {
	for _, s := range supportedVersions {
		if s == v {
			return true
		}
	}
	return false
}

func trimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && (b[start] == ' ' || b[start] == '\t' || b[start] == '\r' || b[start] == '\n') {
		start++
	}
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\r' || b[end-1] == '\n') {
		end--
	}
	return b[start:end]
}
