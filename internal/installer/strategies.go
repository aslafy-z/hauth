package installer

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Overridable in tests: no test may touch the network.
var (
	apiBase    = "https://api.github.com"
	httpClient = &http.Client{Timeout: 5 * time.Minute}
	osArch     = runtime.GOOS + "/" + runtime.GOARCH
)

func httpGet(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "hauth-installer")
	if strings.HasPrefix(url, apiBase) {
		if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp, nil
}

// releaseInstall downloads a binary from the project's latest GitHub
// release and installs it into ~/.local/bin. It needs nothing but network
// access.
type releaseInstall struct {
	Repo        string
	InstallName string
	// Assets maps "goos/goarch" to a path.Match pattern selecting the
	// release asset.
	Assets map[string]string
}

func (r *releaseInstall) describe() string {
	return "download release binary from github.com/" + r.Repo
}

func (r *releaseInstall) ready() bool {
	_, ok := r.Assets[osArch]
	return ok
}

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

func (r *releaseInstall) run(ctx context.Context, logf func(string, ...any)) error {
	pattern := r.Assets[osArch]
	resp, err := httpGet(ctx, apiBase+"/repos/"+r.Repo+"/releases/latest")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var release struct {
		TagName string         `json:"tag_name"`
		Assets  []releaseAsset `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return fmt.Errorf("decode release: %w", err)
	}
	var asset *releaseAsset
	for i := range release.Assets {
		if ok, _ := path.Match(pattern, release.Assets[i].Name); ok {
			asset = &release.Assets[i]
			break
		}
	}
	if asset == nil {
		return fmt.Errorf("release %s of %s has no asset matching %q", release.TagName, r.Repo, pattern)
	}
	logf("downloading %s (%s)", asset.Name, release.TagName)
	body, err := httpGet(ctx, asset.URL)
	if err != nil {
		return err
	}
	defer body.Body.Close()
	tmp, err := os.CreateTemp("", "hauth-install-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, body.Body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return installFromArchive(tmp.Name(), asset.Name, r.InstallName, logf)
}

// installFromArchive extracts the wanted binary out of a tar.gz, zip, or
// raw download and installs it 0755 into ~/.local/bin.
func installFromArchive(archivePath, assetName, installName string, logf func(string, ...any)) error {
	dir, err := binDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(dir, installName)
	var content io.Reader
	switch {
	case strings.HasSuffix(assetName, ".tar.gz") || strings.HasSuffix(assetName, ".tgz"):
		entry, err := withTar(archivePath, func(r io.Reader) (string, error) {
			return pickFromTar(r, installName)
		})
		if err != nil {
			return err
		}
		f, err := os.Open(archivePath)
		if err != nil {
			return err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		rc, err := extractFromTar(gz, entry)
		if err != nil {
			return err
		}
		content = rc
	case strings.HasSuffix(assetName, ".zip"):
		zr, err := zip.OpenReader(archivePath)
		if err != nil {
			return err
		}
		defer zr.Close()
		rc, err := findInZip(&zr.Reader, installName)
		if err != nil {
			return err
		}
		defer rc.Close()
		content = rc
	default:
		// A bare binary asset.
		f, err := os.Open(archivePath)
		if err != nil {
			return err
		}
		defer f.Close()
		content = f
	}
	if err := writeExecutable(dest, content); err != nil {
		return err
	}
	logf("installed %s", dest)
	return nil
}

// withTar opens the gzipped tar at archivePath and runs fn over it.
func withTar(archivePath string, fn func(io.Reader) (string, error)) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	return fn(gz)
}

// candidate ranks archive entries that may hold the binary we are after.
// An exact base-name match always wins (gh archives ship bin/gh next to
// man pages); otherwise a base name starting with the binary name is
// accepted (codex ships a codex-x86_64-... binary), and the largest such
// entry wins so man pages and license stubs lose to the real binary.
type candidate struct {
	name  string
	exact bool
	size  int64
}

func (c candidate) beats(other candidate) bool {
	if other.name == "" {
		return true
	}
	if c.exact != other.exact {
		return c.exact
	}
	return c.size > other.size
}

func classify(entryName, installName string, size int64) (candidate, bool) {
	if strings.Contains(entryName, "..") {
		return candidate{}, false
	}
	base := path.Base(filepath.ToSlash(entryName))
	if base == installName {
		return candidate{name: entryName, exact: true, size: size}, true
	}
	if strings.HasPrefix(base, installName+"-") || strings.HasPrefix(base, installName+"_") {
		return candidate{name: entryName, size: size}, true
	}
	return candidate{}, false
}

// pickFromTar scans the whole archive and returns the winning entry name.
func pickFromTar(r io.Reader, installName string) (string, error) {
	var best candidate
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if c, ok := classify(hdr.Name, installName, hdr.Size); ok && c.beats(best) {
			best = c
		}
	}
	if best.name == "" {
		return "", fmt.Errorf("no %q binary found in archive", installName)
	}
	return best.name, nil
}

func extractFromTar(r io.Reader, entryName string) (io.Reader, error) {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err != nil {
			return nil, err
		}
		if hdr.Name == entryName {
			return tr, nil
		}
	}
}

func findInZip(zr *zip.Reader, installName string) (io.ReadCloser, error) {
	var best candidate
	for _, f := range zr.File {
		if !f.FileInfo().Mode().IsRegular() {
			continue
		}
		if c, ok := classify(f.Name, installName, f.FileInfo().Size()); ok && c.beats(best) {
			best = c
		}
	}
	for _, f := range zr.File {
		if f.Name == best.name {
			return f.Open()
		}
	}
	return nil, fmt.Errorf("no %q binary found in archive", installName)
}

func writeExecutable(dest string, content io.Reader) error {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".hauth-bin-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}

// scriptInstall downloads the harness's official install script and runs
// it with bash. The script is fetched by hauth itself, so curl is not
// needed; bash is.
type scriptInstall struct {
	URL  string
	Args []string
}

func (s *scriptInstall) describe() string {
	return "run official install script " + s.URL
}

func (s *scriptInstall) ready() bool {
	_, err := exec.LookPath("bash")
	return err == nil
}

func (s *scriptInstall) run(ctx context.Context, logf func(string, ...any)) error {
	resp, err := httpGet(ctx, s.URL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	tmp, err := os.CreateTemp("", "hauth-install-*.sh")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	argv := append([]string{tmp.Name()}, s.Args...)
	return runStreaming(ctx, logf, "bash", argv...)
}

// npmInstall is a last resort for harnesses whose only alternative channel
// is npm.
type npmInstall struct {
	Package string
}

func (n *npmInstall) describe() string {
	return "npm install -g " + n.Package
}

func (n *npmInstall) ready() bool {
	_, err := exec.LookPath("npm")
	return err == nil
}

func (n *npmInstall) run(ctx context.Context, logf func(string, ...any)) error {
	return runStreaming(ctx, logf, "npm", "install", "-g", n.Package)
}

func runStreaming(ctx context.Context, logf func(string, ...any), name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	for line := range strings.SplitSeq(strings.TrimRight(string(out), "\n"), "\n") {
		if line != "" {
			logf("%s", line)
		}
	}
	return err
}
