package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func testServer(extra ...Tool) *Server {
	tools := append([]Tool{
		{
			Name:        "echo",
			Description: "echoes text",
			InputSchema: map[string]any{"type": "object"},
			Handler: func(ctx context.Context, args map[string]any) (string, bool) {
				s, _ := args["text"].(string)
				return "echo: " + s, false
			},
		},
		{
			Name:        "fail",
			Description: "always fails",
			InputSchema: map[string]any{"type": "object"},
			Handler: func(ctx context.Context, args map[string]any) (string, bool) {
				return "it broke", true
			},
		},
	}, extra...)
	return &Server{
		Name: "test-server", Title: "Test Server", Version: "1.2.3",
		Instructions: "test instructions",
		Tools:        tools,
	}
}

// harness runs a server over pipes and gives send/recv helpers.
type harness struct {
	t    *testing.T
	in   io.WriteCloser
	out  *bufio.Reader
	done chan error
}

func newHarness(t *testing.T, s *Server) *harness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), inR, outW) }()
	return &harness{t: t, in: inW, out: bufio.NewReader(outR), done: done}
}

func (h *harness) send(line string) {
	h.t.Helper()
	if _, err := h.in.Write([]byte(line + "\n")); err != nil {
		h.t.Fatalf("send: %v", err)
	}
}

func (h *harness) recv() map[string]any {
	h.t.Helper()
	type res struct {
		line string
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		l, err := h.out.ReadString('\n')
		ch <- res{l, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			h.t.Fatalf("recv: %v", r.err)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(r.line), &m); err != nil {
			h.t.Fatalf("recv: bad JSON %q: %v", r.line, err)
		}
		return m
	case <-time.After(5 * time.Second):
		h.t.Fatal("recv: timeout")
		return nil
	}
}

func (h *harness) close() {
	h.t.Helper()
	h.in.Close()
	select {
	case err := <-h.done:
		if err != nil {
			h.t.Fatalf("serve returned %v", err)
		}
	case <-time.After(5 * time.Second):
		h.t.Fatal("server did not stop on EOF")
	}
}

func result(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	r, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result, got %v", m)
	}
	return r
}

func rpcError(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	e, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error, got %v", m)
	}
	return e
}

func TestHandshakeAndToolCall(t *testing.T) {
	h := newHarness(t, testServer())
	defer h.close()

	h.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"c","version":"0"}}}`)
	r := result(t, h.recv())
	if r["protocolVersion"] != "2025-06-18" {
		t.Fatalf("known handshake version must be echoed, got %v", r["protocolVersion"])
	}
	si := r["serverInfo"].(map[string]any)
	if si["name"] != "test-server" || si["version"] != "1.2.3" {
		t.Fatalf("bad serverInfo %v", si)
	}
	if _, ok := r["capabilities"].(map[string]any)["tools"]; !ok {
		t.Fatal("tools capability missing")
	}

	h.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	h.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	r = result(t, h.recv())
	tools := r["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("want 2 tools, got %d", len(tools))
	}
	first := tools[0].(map[string]any)
	if first["name"] != "echo" || first["inputSchema"] == nil || first["description"] == "" {
		t.Fatalf("bad tool descriptor %v", first)
	}

	h.send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`)
	r = result(t, h.recv())
	if r["isError"] != false {
		t.Fatalf("echo must not be an error: %v", r)
	}
	content := r["content"].([]any)[0].(map[string]any)
	if content["type"] != "text" || content["text"] != "echo: hi" {
		t.Fatalf("bad content %v", content)
	}
}

func TestUnknownHandshakeVersionGetsLatestHandshake(t *testing.T) {
	h := newHarness(t, testServer())
	defer h.close()
	h.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`)
	r := result(t, h.recv())
	if r["protocolVersion"] != latestHandshakeVersion {
		t.Fatalf("unknown version must get %s, got %v", latestHandshakeVersion, r["protocolVersion"])
	}
}

func TestStatelessEra(t *testing.T) {
	h := newHarness(t, testServer())
	defer h.close()

	// server/discover before any handshake.
	h.send(`{"jsonrpc":"2.0","id":"d1","method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`)
	r := result(t, h.recv())
	if r["resultType"] != "complete" {
		t.Fatalf("bad discover result %v", r)
	}
	vers := r["supportedVersions"].([]any)
	if vers[0] != "2026-07-28" {
		t.Fatalf("supportedVersions must lead with the stateless revision: %v", vers)
	}

	// tools/call with per-request _meta version, no initialize ever sent.
	h.send(`{"jsonrpc":"2.0","id":"c1","method":"tools/call","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"},"name":"echo","arguments":{"text":"x"}}}`)
	r = result(t, h.recv())
	meta, ok := r["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("modern-era result must carry _meta serverInfo: %v", r)
	}
	if meta["io.modelcontextprotocol/serverInfo"].(map[string]any)["name"] != "test-server" {
		t.Fatalf("bad _meta %v", meta)
	}

	// Unsupported per-request version → -32022 with supported list.
	h.send(`{"jsonrpc":"2.0","id":"c2","method":"tools/call","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"1900-01-01"},"name":"echo","arguments":{}}}`)
	e := rpcError(t, h.recv())
	if e["code"].(float64) != -32022 {
		t.Fatalf("want -32022, got %v", e)
	}
	data := e["data"].(map[string]any)
	if data["requested"] != "1900-01-01" || len(data["supported"].([]any)) == 0 {
		t.Fatalf("bad error data %v", data)
	}
}

func TestToolErrorsAndUnknowns(t *testing.T) {
	h := newHarness(t, testServer())
	defer h.close()

	// Tool-level failure is isError, not a protocol error.
	h.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"fail","arguments":{}}}`)
	r := result(t, h.recv())
	if r["isError"] != true {
		t.Fatalf("want isError true: %v", r)
	}

	// Unknown tool is a protocol error.
	h.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	if e := rpcError(t, h.recv()); e["code"].(float64) != -32602 {
		t.Fatalf("want -32602: %v", e)
	}

	// Unknown method with id is -32601.
	h.send(`{"jsonrpc":"2.0","id":3,"method":"resources/list"}`)
	if e := rpcError(t, h.recv()); e["code"].(float64) != -32601 {
		t.Fatalf("want -32601: %v", e)
	}

	// Parse error is -32700 with null id.
	h.send(`{nope`)
	m := h.recv()
	if e := rpcError(t, m); e["code"].(float64) != -32700 {
		t.Fatalf("want -32700: %v", e)
	}
	if m["id"] != nil {
		t.Fatalf("parse-error id must be null: %v", m)
	}

	// Unknown notification (no id) must be silently ignored: ping right
	// after must be the next response.
	h.send(`{"jsonrpc":"2.0","method":"notifications/whatever"}`)
	h.send(`{"jsonrpc":"2.0","id":4,"method":"ping"}`)
	m = h.recv()
	if m["id"].(float64) != 4 {
		t.Fatalf("notification must not get a response; got %v", m)
	}
	if _, ok := m["result"]; !ok {
		t.Fatalf("ping must return an empty result: %v", m)
	}
}

func TestCancellationDropsResponse(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	slow := Tool{
		Name: "slow", Description: "blocks until cancelled",
		InputSchema: map[string]any{"type": "object"},
		Handler: func(ctx context.Context, args map[string]any) (string, bool) {
			once.Do(func() { close(started) })
			<-ctx.Done()
			return "should never be sent", false
		},
	}
	h := newHarness(t, testServer(slow))

	h.send(`{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"slow","arguments":{}}}`)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("slow tool never started")
	}
	// While the slow call runs, other requests must still be answered.
	h.send(`{"jsonrpc":"2.0","id":43,"method":"ping"}`)
	if m := h.recv(); m["id"].(float64) != 43 {
		t.Fatalf("ping blocked behind a tool call: %v", m)
	}
	h.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":42}}`)
	// The cancelled call must NOT produce a response; a follow-up ping's
	// answer must be the very next message.
	h.send(`{"jsonrpc":"2.0","id":44,"method":"ping"}`)
	if m := h.recv(); m["id"].(float64) != 44 {
		t.Fatalf("cancelled call leaked a response: %v", m)
	}
	h.close()
}

func TestServeStopsOnEOFWaitingForTools(t *testing.T) {
	h := newHarness(t, testServer())
	h.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"text":"bye"}}}`)
	result(t, h.recv())
	h.close() // asserts clean shutdown
}

func TestInstructionsEverywhere(t *testing.T) {
	h := newHarness(t, testServer())
	defer h.close()
	h.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`)
	if r := result(t, h.recv()); r["instructions"] != "test instructions" {
		t.Fatalf("initialize missing instructions: %v", r)
	}
	h.send(`{"jsonrpc":"2.0","id":2,"method":"server/discover"}`)
	if r := result(t, h.recv()); r["instructions"] != "test instructions" {
		t.Fatalf("discover missing instructions: %v", r)
	}
}

func TestOutputIsOneJSONPerLine(t *testing.T) {
	// The transport forbids embedded newlines; a tool result containing
	// newlines must still serialize to exactly one line.
	nl := Tool{
		Name: "multiline", Description: "returns newlines",
		InputSchema: map[string]any{"type": "object"},
		Handler: func(ctx context.Context, args map[string]any) (string, bool) {
			return "line1\nline2\nline3", false
		},
	}
	var buf strings.Builder
	s := testServer(nl)
	inR, inW := io.Pipe()
	done := make(chan error, 1)
	var mu sync.Mutex
	go func() { done <- s.Serve(context.Background(), inR, syncWriter{&mu, &buf}) }()
	inW.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"multiline","arguments":{}}}` + "\n"))
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := buf.String()
		mu.Unlock()
		if strings.Count(got, "\n") == 1 && strings.Contains(got, "line3") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bad transport framing: %q", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	inW.Close()
	<-done
}

type syncWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (s syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
