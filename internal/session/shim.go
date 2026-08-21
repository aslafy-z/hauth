package session

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
)

// browserShim intercepts the CLI's attempt to open a browser. A shell
// script installed as BROWSER (and as the common opener names on PATH)
// posts the URL back to a per-session loopback listener, which is the
// primary URL source; output regexes are the fallback.
type browserShim struct {
	dir    string
	script string
	token  string
	port   int
	server *http.Server
	ln     net.Listener
}

// The script needs an HTTP client; curl and wget cover every workspace
// image we target. wget cannot urlencode, so the raw body is parsed
// leniently on the receiving side.
const shimScript = `#!/bin/sh
# hauth browser shim: reports the URL to the owning session instead of
# opening a browser.
url="$1"
[ -n "$url" ] || exit 0
if command -v curl >/dev/null 2>&1; then
  curl -fsS -m 5 -X POST -H "X-Hauth-Shim-Token: ${HAUTH_SHIM_TOKEN:-}" \
    --data-urlencode "url=${url}" \
    "http://127.0.0.1:${HAUTH_SHIM_PORT:-0}/browser" >/dev/null 2>&1 || true
elif command -v wget >/dev/null 2>&1; then
  wget -q -T 5 -O /dev/null \
    --header "X-Hauth-Shim-Token: ${HAUTH_SHIM_TOKEN:-}" \
    --post-data "url=${url}" \
    "http://127.0.0.1:${HAUTH_SHIM_PORT:-0}/browser" || true
fi
exit 0
`

var openerNames = []string{"xdg-open", "open", "x-www-browser", "sensible-browser", "browser"}

func newBrowserShim(onURL func(string)) (*browserShim, error) {
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "hauth-shim-*")
	if err != nil {
		ln.Close()
		return nil, err
	}
	b := &browserShim{
		dir:    dir,
		script: filepath.Join(dir, "hauth-browser-shim"),
		token:  hex.EncodeToString(tok),
		port:   ln.Addr().(*net.TCPAddr).Port,
		ln:     ln,
	}
	if err := os.WriteFile(b.script, []byte(shimScript), 0o755); err != nil {
		b.Close()
		return nil, err
	}
	for _, name := range openerNames {
		if err := os.Symlink(b.script, filepath.Join(dir, name)); err != nil {
			b.Close()
			return nil, err
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/browser", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		got := []byte(r.Header.Get("X-Hauth-Shim-Token"))
		if subtle.ConstantTimeCompare(got, []byte(b.token)) != 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if url := parseShimBody(string(body)); url != "" {
			onURL(url)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	b.server = &http.Server{Handler: mux}
	go func() { _ = b.server.Serve(ln) }()
	return b, nil
}

// parseShimBody accepts both a urlencoded form (curl) and a raw
// "url=<verbatim>" body (wget), where the verbatim URL may itself contain
// '&' and '=' characters.
func parseShimBody(body string) string {
	i := strings.Index(body, "url=")
	if i < 0 {
		return ""
	}
	v := body[i+len("url="):]
	if u, err := neturl.QueryUnescape(v); err == nil {
		v = u
	}
	return strings.TrimSpace(v)
}

// Env returns the environment entries that route browser opens through the
// shim. basePATH is the PATH the child would otherwise get.
func (b *browserShim) Env(basePATH string) []string {
	return []string{
		"BROWSER=" + b.script,
		"HAUTH_SHIM_TOKEN=" + b.token,
		"HAUTH_SHIM_PORT=" + fmt.Sprint(b.port),
		"PATH=" + b.dir + string(os.PathListSeparator) + basePATH,
	}
}

func (b *browserShim) Close() {
	if b.server != nil {
		_ = b.server.Close()
	} else if b.ln != nil {
		_ = b.ln.Close()
	}
	_ = os.RemoveAll(b.dir)
}
