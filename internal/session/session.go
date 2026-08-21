// Package session runs a harness CLI login flow in a pty, detects URLs,
// codes, and prompts in its output, and exposes a redacted output stream
// for the kiosk terminal, ring buffer, and SSE feed.
package session

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"

	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/source"
)

// State is the lifecycle state of a session.
type State string

const (
	StateRunning   State = "running"
	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
	StateCanceled  State = "canceled"
)

const (
	ringCap    = 64 << 10
	maxLineBuf = 16 << 10
)

// Options configures a session. All fields are optional.
type Options struct {
	// Home overrides $HOME for the child process (and defaults to the
	// current user's home).
	Home     string
	StateDir string
	// ExtraEnv is appended to the child environment.
	ExtraEnv map[string]string
	// OnEvent receives detector and shim events. The Prompt field is
	// already redacted.
	OnEvent func(adapter.Event)
	// OnOutput receives the redacted output stream.
	OnOutput func([]byte)
}

// Session is a running (or finished) login flow.
type Session struct {
	harness string
	method  adapter.Method

	cmd  *exec.Cmd
	ptmx *os.File
	shim *browserShim

	redactor *StreamRedactor
	detector adapter.Detector
	lineBuf  []byte
	osc8Buf  []byte

	mu       sync.Mutex
	state    State
	exitCode int
	ring     []byte
	subs     map[int]chan []byte
	nextSub  int
	seenURLs map[string]bool
	sawOK    bool
	sawFail  bool
	canceled bool
	timedOut bool
	closed   bool
	timer    *time.Timer

	onEvent  func(adapter.Event)
	onOutput func([]byte)

	done chan struct{}
}

// Start launches the method's command in a pty. The returned session is
// running; completion is signaled on Done().
func Start(ctx context.Context, harness string, m adapter.Method, opts Options) (*Session, error) {
	if len(m.Argv) == 0 {
		return nil, errors.New("session: method has no command")
	}
	if opts.Home == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		opts.Home = home
	}
	if opts.StateDir == "" {
		opts.StateDir = source.StateDir()
	}

	var det adapter.Detector
	if m.Detect != nil {
		det = m.Detect()
	} else {
		det = adapter.NewLineDetector()
	}

	s := &Session{
		harness:  harness,
		method:   m,
		redactor: NewStreamRedactor(),
		detector: det,
		state:    StateRunning,
		exitCode: -1,
		subs:     map[int]chan []byte{},
		seenURLs: map[string]bool{},
		onEvent:  opts.OnEvent,
		onOutput: opts.OnOutput,
		done:     make(chan struct{}),
	}

	shim, err := newBrowserShim(func(url string) {
		s.handleEvent(adapter.Event{Kind: adapter.EventURL, URL: url})
	})
	if err != nil {
		return nil, err
	}
	s.shim = shim

	env := os.Environ()
	env = append(env, "HOME="+opts.Home)
	for k, v := range opts.ExtraEnv {
		env = append(env, k+"="+v)
	}
	env = append(env, "TERM=xterm-256color", "NO_COLOR=1")
	for k, v := range m.Env {
		env = append(env, k+"="+v)
	}
	// Shim entries come last so its BROWSER and PATH interception win.
	env = append(env, shim.Env(os.Getenv("PATH"))...)

	cmd := exec.Command(m.Argv[0], m.Argv[1:]...)
	cmd.Dir = m.Cwd
	cmd.Env = env
	// Wide by default: narrow ptys make TUIs hard-wrap long OAuth URLs,
	// which line-based detection then truncates. The kiosk terminal sends
	// its real size when someone actually attaches.
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 50, Cols: 240})
	if err != nil {
		shim.Close()
		return nil, err
	}
	s.cmd = cmd
	s.ptmx = ptmx

	if m.Timeout > 0 {
		s.timer = time.AfterFunc(m.Timeout, func() {
			s.mu.Lock()
			s.timedOut = true
			s.mu.Unlock()
			s.kill()
		})
	}
	go func() {
		select {
		case <-ctx.Done():
			s.Cancel()
		case <-s.done:
		}
	}()

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 8192)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				s.consumeOutput(buf[:n])
				s.consumeDetect(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		_ = cmd.Wait()
		_ = ptmx.Close()
		<-readerDone
		if rest := s.redactor.Flush(); len(rest) > 0 {
			s.deliver(rest)
		}
		s.finalize()
	}()
	return s, nil
}

func (s *Session) Harness() string  { return s.harness }
func (s *Session) MethodID() string { return s.method.ID }

// Write sends raw bytes to the pty.
func (s *Session) Write(input string) error {
	_, err := s.ptmx.Write([]byte(input))
	return err
}

// SendLine submits one line of input (a pasted code or token). CR is what a
// raw-mode TUI reads as Enter; cooked-mode line discipline translates it to
// NL. Token-reading commands consume stdin to EOF, so a ctrl-D follows.
func (s *Session) SendLine(text string) error {
	in := text + "\r"
	if s.method.Input == adapter.InputToken {
		in += "\x04"
	}
	return s.Write(in)
}

// Resize sets the pty window size.
func (s *Session) Resize(rows, cols uint16) error {
	return pty.Setsize(s.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
}

// Cancel kills the login process. Safe to call multiple times.
func (s *Session) Cancel() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.canceled = true
	s.mu.Unlock()
	s.kill()
}

// Done is closed when the process has exited and all output is drained.
func (s *Session) Done() <-chan struct{} { return s.done }

// State returns the current state and, once finished, the exit code
// (-1 while running or when killed).
func (s *Session) State() (State, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.exitCode
}

// Ring returns a copy of the redacted output ring buffer (last 64 KiB).
func (s *Session) Ring() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.ring...)
}

// Subscribe returns a channel that first delivers the ring buffer, then
// live redacted output. The channel is closed when the session ends or the
// cancel function is called. Slow consumers lose chunks rather than block
// the session.
func (s *Session) Subscribe() (<-chan []byte, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan []byte, 256)
	if len(s.ring) > 0 {
		ch <- append([]byte(nil), s.ring...)
	}
	if s.closed {
		close(ch)
		return ch, func() {}
	}
	id := s.nextSub
	s.nextSub++
	s.subs[id] = ch
	return ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if c, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(c)
		}
	}
}

func (s *Session) kill() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	if s.ptmx != nil {
		_ = s.ptmx.Close()
	}
}

func (s *Session) consumeOutput(p []byte) {
	if out := s.redactor.Write(p); len(out) > 0 {
		s.deliver(out)
	}
}

func (s *Session) deliver(out []byte) {
	s.mu.Lock()
	s.ring = append(s.ring, out...)
	if len(s.ring) > ringCap {
		s.ring = s.ring[len(s.ring)-ringCap:]
	}
	for _, ch := range s.subs {
		select {
		case ch <- append([]byte(nil), out...):
		default:
		}
	}
	cb := s.onOutput
	s.mu.Unlock()
	if cb != nil {
		cb(out)
	}
}

// consumeDetect feeds complete lines to the detector, treating both CR and
// NL as separators so spinner redraws are seen. A trailing partial line is
// fed provisionally (without being consumed) so prompts that never end in a
// newline, like "Paste code: ", are still detected; detector rules use Once
// to stay idempotent under re-feeding.
// osc8URI matches a complete OSC 8 hyperlink open sequence. TUIs often
// render a shortened label linked to the full URI, so the escape sequence
// is the only place the real URL exists.
var osc8URI = regexp.MustCompile(`\x1b\]8;[^;\x07\x1b]*;([^\x07\x1b]+)(?:\x07|\x1b\\)`)

func (s *Session) consumeDetect(p []byte) {
	s.osc8Buf = append(s.osc8Buf, p...)
	for _, m := range osc8URI.FindAllSubmatch(s.osc8Buf, -1) {
		uri := string(m[1])
		if strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://") {
			s.handleEvent(adapter.Event{Kind: adapter.EventURL, URL: uri})
		}
	}
	if idx := bytes.LastIndexByte(s.osc8Buf, 0x1b); idx > 0 {
		// Keep only a possible unterminated trailing sequence.
		s.osc8Buf = append(s.osc8Buf[:0], s.osc8Buf[idx:]...)
	}
	if len(s.osc8Buf) > maxLineBuf {
		s.osc8Buf = s.osc8Buf[len(s.osc8Buf)-maxLineBuf:]
	}

	s.lineBuf = append(s.lineBuf, p...)
	for {
		i := bytes.IndexAny(s.lineBuf, "\r\n")
		if i < 0 {
			break
		}
		line := string(s.lineBuf[:i])
		j := i + 1
		if s.lineBuf[i] == '\r' && j < len(s.lineBuf) && s.lineBuf[j] == '\n' {
			j++
		}
		s.lineBuf = s.lineBuf[j:]
		s.feedLine(line, false)
	}
	if len(s.lineBuf) > maxLineBuf {
		s.lineBuf = s.lineBuf[len(s.lineBuf)-maxLineBuf:]
	}
	if len(s.lineBuf) > 0 {
		s.feedLine(string(s.lineBuf), true)
	}
}

// partialFeeder lets a detector handle incomplete lines safely; detectors
// without it only ever see complete lines.
type partialFeeder interface {
	FeedPartial(line string) []adapter.Event
}

func (s *Session) feedLine(line string, partial bool) {
	clean := StripANSI(line)
	if len(bytes.TrimSpace([]byte(clean))) == 0 {
		return
	}
	if partial {
		pf, ok := s.detector.(partialFeeder)
		if !ok {
			return
		}
		for _, ev := range pf.FeedPartial(clean) {
			s.handleEvent(ev)
		}
		return
	}
	for _, ev := range s.detector.Feed(clean) {
		s.handleEvent(ev)
	}
}

func (s *Session) handleEvent(ev adapter.Event) {
	s.mu.Lock()
	switch ev.Kind {
	case adapter.EventURL:
		if ev.URL == "" || s.seenURLs[ev.URL] {
			s.mu.Unlock()
			return
		}
		s.seenURLs[ev.URL] = true
	case adapter.EventSuccess:
		firstOK := !s.sawOK
		s.sawOK = true
		if firstOK && s.method.EndOnSuccess {
			// REPL-style CLIs (claude, agy) stay open after a successful
			// login; end the session for them after a short grace so file
			// writes settle and the user sees the success output.
			time.AfterFunc(5*time.Second, s.Cancel)
		}
	case adapter.EventFailure:
		s.sawFail = true
	}
	cb := s.onEvent
	s.mu.Unlock()
	if ev.Respond != "" {
		// A rule answered a gate itself (e.g. gh's "Press Enter to open").
		_ = s.Write(ev.Respond)
		if ev.Kind == adapter.EventPrompt {
			return // auto-answered; nothing for the user to do
		}
	}
	ev.Prompt = string(Redact([]byte(ev.Prompt)))
	if cb != nil {
		cb(ev)
	}
}

func (s *Session) finalize() {
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
	}
	exit := -1
	if s.cmd.ProcessState != nil {
		exit = s.cmd.ProcessState.ExitCode()
	}
	s.exitCode = exit
	switch {
	// A detected success outlives cancel and timeout: REPL-style CLIs
	// keep running after logging in, and ending them must not demote a
	// login that already happened.
	case s.sawOK && !s.sawFail:
		s.state = StateSucceeded
	case s.canceled:
		s.state = StateCanceled
	case s.timedOut:
		s.state = StateFailed
	case s.sawFail:
		s.state = StateFailed
	case exit == 0:
		s.state = StateSucceeded
	default:
		s.state = StateFailed
	}
	for id, ch := range s.subs {
		delete(s.subs, id)
		close(ch)
	}
	s.closed = true
	s.mu.Unlock()
	s.shim.Close()
	close(s.done)
}
