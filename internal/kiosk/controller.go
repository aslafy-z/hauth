package kiosk

import (
	"context"
	"time"
)

// Controller is the seam between the kiosk HTTP layer and the rest of hauth.
// The kiosk never touches adapters, sessions, or the store directly; the
// integrator provides an implementation of this interface.
type Controller interface {
	// Statuses returns the cached view of every harness. It must be cheap.
	Statuses(ctx context.Context) []HarnessView
	// Check runs an expensive probe for one harness on user demand.
	Check(ctx context.Context, harness string) (HarnessView, error)
	// StartLogin starts a login session for harness with the given method.
	// Starting a new session cancels any active one for that harness.
	StartLogin(ctx context.Context, harness, method string) (sessionID string, err error)
	SendInput(ctx context.Context, sessionID, text string) error
	CancelSession(ctx context.Context, sessionID string) error
	Logout(ctx context.Context, harness string) error
	// Relogin performs logout followed by the default login method as one
	// operation, so the user never ends up half logged out.
	Relogin(ctx context.Context, harness string) (sessionID string, err error)
	// Adopt imports externally created credential files into the store.
	Adopt(ctx context.Context, harness string) error
	// Install installs the harness's CLI in a session so the UI can
	// stream progress like a login flow.
	Install(ctx context.Context, harness string) (sessionID string, err error)
	Attach(sessionID string) (AttachedSession, bool)
	// Subscribe returns a channel of status and session events plus a
	// cancel function. The channel is closed after cancel.
	Subscribe() (<-chan Event, func())
}

// HarnessView is the kiosk-facing state of one harness.
type HarnessView struct {
	Name      string    `json:"name"`
	Source    string    `json:"source"` // none | store | file-external | env
	Detail    string    `json:"detail"`
	Validity  string    `json:"validity"` // valid | invalid | unknown
	Account   string    `json:"account,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
	Refresh   string    `json:"refresh"` // none | stable | rotating

	Methods []MethodView `json:"methods"`

	SessionID    string `json:"session_id,omitempty"`
	SessionState string `json:"session_state,omitempty"`
	// Accumulated artifacts of the active session, so clients that
	// connect (or reconnect) mid-login can rebuild the flow: live events
	// are not replayed.
	SessionURL    string `json:"session_url,omitempty"`
	SessionCode   string `json:"session_code,omitempty"`
	SessionPrompt string `json:"session_prompt,omitempty"`

	// Installed reports whether the harness's CLI is on PATH; CanInstall
	// is set when hauth knows how to install it.
	Installed  bool `json:"installed"`
	CanInstall bool `json:"can_install"`

	CanLogout    bool   `json:"can_logout"`
	LogoutReason string `json:"logout_reason,omitempty"`
	CanLogin     bool   `json:"can_login"`
	LoginReason  string `json:"login_reason,omitempty"`
	CanAdopt     bool   `json:"can_adopt"`
	// CheckIsExpensive marks harnesses whose validity probe makes a
	// network call; the UI shows an explicit "check" button for them.
	CheckIsExpensive bool   `json:"check_is_expensive"`
	LeaseHolder      string `json:"lease_holder,omitempty"`
}

// MethodView describes one login method button.
type MethodView struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Input string `json:"input"` // none | code | token | free_text
}

// AttachedSession is a live pty session as seen by the WebSocket handler.
type AttachedSession interface {
	// Subscribe returns a byte stream that replays the session's ring
	// buffer first, then follows live output. cancel detaches.
	Subscribe() (<-chan []byte, func())
	Write(p []byte) (int, error)
	// SendLine writes text plus a newline, used by the form input path.
	SendLine(text string) error
	Resize(rows, cols uint16) error
	Done() <-chan struct{}
}

// Event is pushed to SSE clients. Type is one of: status_changed,
// session_url, session_code, session_prompt, session_state.
type Event struct {
	Type      string `json:"type"`
	Harness   string `json:"harness,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	URL       string `json:"url,omitempty"`
	Code      string `json:"code,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
	State     string `json:"state,omitempty"`
}
