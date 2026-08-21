// Package kiosk serves the loopback UI: status per harness, login flows,
// and a terminal panel over WebSocket.
//
// The kiosk has no authentication of its own. It binds to loopback only and
// relies on whatever fronts it (a Coder app with owner-only sharing, an SSH
// tunnel, Tailscale serve) to control who can reach it. A same-origin check
// on the Origin header stops a hostile web page in the user's browser from
// driving it cross-origin, without requiring the fronting hostname to be
// configured.
package kiosk

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed all:static
var staticFS embed.FS

// Options configures the kiosk server.
type Options struct {
	// Listen is the address to bind. The host part must resolve to a
	// loopback address; anything else is refused.
	Listen string
	// AllowedOrigins lists extra origins (or bare hosts) accepted even
	// when they differ from the request's Host. Same-origin requests and
	// loopback origins are always accepted, so proxied apps and SSH
	// tunnels work without configuration; this is only for fronts where
	// the page is served from a different host than the kiosk API.
	AllowedOrigins []string
}

// DefaultListen is the kiosk's default bind address.
const DefaultListen = "127.0.0.1:43117"

// Server is the kiosk HTTP server.
type Server struct {
	ctrl         Controller
	listen       string
	allowedHosts map[string]bool
	httpSrv      *http.Server
}

// New validates options and builds the server. It returns an error if the
// listen address is not loopback.
func New(ctrl Controller, opts Options) (*Server, error) {
	listen := opts.Listen
	if listen == "" {
		listen = DefaultListen
	}
	if err := ensureLoopbackAddr(listen); err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, o := range opts.AllowedOrigins {
		if h := hostnameOf(o); h != "" {
			allowed[h] = true
		}
	}
	s := &Server{ctrl: ctrl, listen: listen, allowedHosts: allowed}
	s.httpSrv = &http.Server{
		Handler:           s.checkOriginHost(s.routes()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s, nil
}

// Handler exposes the full middleware-wrapped handler, mainly for tests.
func (s *Server) Handler() http.Handler { return s.httpSrv.Handler }

// Start listens and serves until ctx is canceled, then shuts down
// gracefully.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.listen)
	if err != nil {
		return err
	}
	errc := make(chan error, 1)
	go func() { errc <- s.httpSrv.Serve(ln) }()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutCtx)
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Addr returns the configured listen address.
func (s *Server) Addr() string { return s.listen }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServerFS(static)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			// The app shell must not be cached: it reflects the
			// running binary's embedded assets.
			w.Header().Set("Cache-Control", "no-store")
		}
		fileServer.ServeHTTP(w, r)
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.ctrl.Statuses(r.Context()))
	})

	mux.HandleFunc("POST /api/harness/{name}/check", func(w http.ResponseWriter, r *http.Request) {
		view, err := s.ctrl.Check(r.Context(), r.PathValue("name"))
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
	})

	mux.HandleFunc("POST /api/harness/{name}/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Method string `json:"method"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		id, err := s.ctrl.StartLogin(r.Context(), r.PathValue("name"), body.Method)
		if err != nil {
			writeErr(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"session_id": id})
	})

	mux.HandleFunc("POST /api/harness/{name}/logout", func(w http.ResponseWriter, r *http.Request) {
		if err := s.ctrl.Logout(r.Context(), r.PathValue("name")); err != nil {
			writeErr(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /api/harness/{name}/relogin", func(w http.ResponseWriter, r *http.Request) {
		id, err := s.ctrl.Relogin(r.Context(), r.PathValue("name"))
		if err != nil {
			writeErr(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"session_id": id})
	})

	mux.HandleFunc("POST /api/harness/{name}/install", func(w http.ResponseWriter, r *http.Request) {
		id, err := s.ctrl.Install(r.Context(), r.PathValue("name"))
		if err != nil {
			writeErr(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"session_id": id})
	})

	mux.HandleFunc("POST /api/harness/{name}/adopt", func(w http.ResponseWriter, r *http.Request) {
		if err := s.ctrl.Adopt(r.Context(), r.PathValue("name")); err != nil {
			writeErr(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /api/sessions/{id}/input", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := s.ctrl.SendInput(r.Context(), r.PathValue("id"), body.Text); err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /api/sessions/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if err := s.ctrl.CancelSession(r.Context(), r.PathValue("id")); err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /events", s.handleSSE)
	mux.HandleFunc("GET /ws/sessions/{id}", s.handleSessionWS)

	return mux
}

// checkOriginHost enforces a same-origin policy: when a browser sends an
// Origin header it must match the request's Host (or be loopback or
// explicitly allowed). This stops cross-origin requests from other sites
// without the deployer having to configure the fronting hostname; proxied
// fronts like Coder subdomain apps work out of the box because the page and
// its API calls share one host. Requests without an Origin header pass: the
// fronting proxy is the authentication boundary, and non-browser clients
// have no origin to check.
func (s *Server) checkOriginHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && origin != "null" {
			oh := hostnameOf(origin)
			if oh != hostnameOf(r.Host) && !s.hostAllowed(oh) {
				writeErr(w, http.StatusForbidden, fmt.Errorf("origin %q does not match host %q", origin, r.Host))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) hostAllowed(host string) bool {
	if host == "" {
		return false
	}
	return isLoopbackHost(host) || s.allowedHosts[host]
}

// hostnameOf extracts a lowercase hostname from an origin URL, a host:port
// pair, or a bare host.
func hostnameOf(entry string) string {
	e := strings.TrimSpace(entry)
	if strings.Contains(e, "://") {
		u, err := url.Parse(e)
		if err != nil {
			return ""
		}
		e = u.Host
	}
	if h, _, err := net.SplitHostPort(e); err == nil {
		e = h
	}
	return strings.ToLower(strings.Trim(e, "[]"))
}

func isLoopbackHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// ensureLoopbackAddr refuses to bind anything that is not loopback. The
// kiosk must never be directly reachable from off-host.
func ensureLoopbackAddr(listen string) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", listen, err)
	}
	if host == "" {
		return fmt.Errorf("listen address %q binds all interfaces; the kiosk only serves loopback", listen)
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		if !ip.IsLoopback() {
			return fmt.Errorf("listen address %q is not loopback; the kiosk only serves loopback", listen)
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("cannot resolve listen host %q: %w", host, err)
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return fmt.Errorf("listen host %q resolves to non-loopback %s; the kiosk only serves loopback", host, ip)
		}
	}
	return nil
}

func decodeBody(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
