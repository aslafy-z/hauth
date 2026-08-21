package kiosk

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type stubController struct {
	mu        sync.Mutex
	views     []HarnessView
	inputs    []string
	session   *stubSession
	events    chan Event
	canceled  []string
	installed string
}

func newStubController() *stubController {
	return &stubController{
		views: []HarnessView{{
			Name:     "gh",
			Source:   "env",
			Detail:   "env GH_TOKEN",
			Validity: "valid",
			Methods:  []MethodView{{ID: "device", Title: "Device flow", Input: "none"}},
		}},
		events: make(chan Event, 8),
	}
}

func (c *stubController) Statuses(context.Context) []HarnessView { return c.views }
func (c *stubController) Check(_ context.Context, name string) (HarnessView, error) {
	for _, v := range c.views {
		if v.Name == name {
			return v, nil
		}
	}
	return HarnessView{}, errors.New("unknown harness")
}
func (c *stubController) StartLogin(_ context.Context, harness, method string) (string, error) {
	return "sess-" + harness + "-" + method, nil
}
func (c *stubController) SendInput(_ context.Context, id, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inputs = append(c.inputs, id+":"+text)
	return nil
}
func (c *stubController) CancelSession(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.canceled = append(c.canceled, id)
	return nil
}
func (c *stubController) Logout(context.Context, string) error { return nil }
func (c *stubController) Relogin(_ context.Context, h string) (string, error) {
	return "sess-" + h, nil
}
func (c *stubController) Adopt(context.Context, string) error { return nil }
func (c *stubController) Install(_ context.Context, harness string) (string, error) {
	c.installed = harness
	return "install-1", nil
}
func (c *stubController) Attach(id string) (AttachedSession, bool) {
	if c.session == nil || id != "sess-1" {
		return nil, false
	}
	return c.session, true
}
func (c *stubController) Subscribe() (<-chan Event, func()) {
	return c.events, func() {}
}

type stubSession struct {
	mu      sync.Mutex
	written []byte
	lines   []string
	output  chan []byte
	done    chan struct{}
}

func newStubSession(ring ...[]byte) *stubSession {
	s := &stubSession{output: make(chan []byte, 16), done: make(chan struct{})}
	for _, chunk := range ring {
		s.output <- chunk
	}
	return s
}

func (s *stubSession) Subscribe() (<-chan []byte, func()) { return s.output, func() {} }
func (s *stubSession) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.written = append(s.written, p...)
	return len(p), nil
}
func (s *stubSession) SendLine(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, text)
	return nil
}
func (s *stubSession) Resize(uint16, uint16) error { return nil }
func (s *stubSession) Done() <-chan struct{}       { return s.done }

func newTestServer(t *testing.T, ctrl Controller, origins ...string) *Server {
	t.Helper()
	s, err := New(ctrl, Options{Listen: "127.0.0.1:0", AllowedOrigins: origins})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLoopbackBindRefusal(t *testing.T) {
	ctrl := newStubController()
	for _, addr := range []string{"0.0.0.0:43117", ":43117", "192.168.1.4:1", "example.com:80"} {
		if _, err := New(ctrl, Options{Listen: addr}); err == nil {
			t.Errorf("New accepted non-loopback %q", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:43117", "localhost:43117", "[::1]:43117"} {
		if _, err := New(ctrl, Options{Listen: addr}); err != nil {
			t.Errorf("New refused loopback %q: %v", addr, err)
		}
	}
}

func TestHostAndOriginChecks(t *testing.T) {
	s := newTestServer(t, newStubController(), "https://kiosk.example.com")
	cases := []struct {
		host, origin string
		want         int
	}{
		{"127.0.0.1:43117", "", http.StatusOK},
		{"localhost:43117", "http://localhost:8080", http.StatusOK},
		{"kiosk.example.com", "https://kiosk.example.com", http.StatusOK},
		{"evil.example.net", "", http.StatusForbidden},
		{"127.0.0.1:43117", "https://evil.example.net", http.StatusForbidden},
		{"127.0.0.1:43117", "http://[::1]:9999", http.StatusOK},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", "/api/status", nil)
		req.Host = tc.host
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("host=%q origin=%q: got %d want %d", tc.host, tc.origin, rec.Code, tc.want)
		}
	}
}

func TestStatusEndpoint(t *testing.T) {
	s := newTestServer(t, newStubController())
	req := httptest.NewRequest("GET", "/api/status", nil)
	req.Host = "127.0.0.1:43117"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var views []HarnessView
	if err := json.Unmarshal(rec.Body.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Name != "gh" || views[0].Source != "env" {
		t.Fatalf("unexpected views: %+v", views)
	}
}

func TestSessionInputUsesSendLineRoute(t *testing.T) {
	ctrl := newStubController()
	s := newTestServer(t, ctrl)
	req := httptest.NewRequest("POST", "/api/sessions/abc/input", strings.NewReader(`{"text":"XYZ-123"}`))
	req.Host = "127.0.0.1:43117"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if len(ctrl.inputs) != 1 || ctrl.inputs[0] != "abc:XYZ-123" {
		t.Fatalf("input not forwarded: %v", ctrl.inputs)
	}
}

func TestSSESendsSnapshotFirst(t *testing.T) {
	s := newTestServer(t, newStubController())
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/events", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	scanner := bufio.NewScanner(res.Body)
	var first string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			first = strings.TrimPrefix(line, "data: ")
			break
		}
	}
	var snap sseSnapshot
	if err := json.Unmarshal([]byte(first), &snap); err != nil {
		t.Fatalf("bad snapshot %q: %v", first, err)
	}
	if snap.Type != "snapshot" || len(snap.Statuses) != 1 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
}

func TestWebSocketSessionEcho(t *testing.T) {
	ctrl := newStubController()
	ring := []byte("ring-replay")
	ctrl.session = newStubSession(ring)
	s := newTestServer(t, ctrl)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/sessions/sess-1"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageBinary || string(data) != "ring-replay" {
		t.Fatalf("got %v %q", typ, data)
	}

	if err := conn.Write(ctx, websocket.MessageBinary, []byte("typed")); err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","rows":40,"cols":120}`)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		ctrl.session.mu.Lock()
		got := string(ctrl.session.written)
		ctrl.session.mu.Unlock()
		if got == "typed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pty never received input, got %q", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWebSocketUnknownSession(t *testing.T) {
	s := newTestServer(t, newStubController())
	req := httptest.NewRequest("GET", "/ws/sessions/nope", nil)
	req.Host = "127.0.0.1:43117"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestInstallRoute(t *testing.T) {
	ctrl := newStubController()
	s := newTestServer(t, ctrl)
	req := httptest.NewRequest("POST", "/api/harness/claude/install", nil)
	req.Host = "127.0.0.1:43117"
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("install returned %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["session_id"] != "install-1" || ctrl.installed != "claude" {
		t.Fatalf("unexpected install result: %+v (installed=%q)", resp, ctrl.installed)
	}
}
