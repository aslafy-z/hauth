package installer

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnownAndBinaryFor(t *testing.T) {
	for harness, binary := range map[string]string{
		"gh": "gh", "claude": "claude", "codex": "codex", "opencode": "opencode", "agy": "agy",
	} {
		if !Known(harness) {
			t.Errorf("Known(%q) = false", harness)
		}
		got, ok := BinaryFor(harness)
		if !ok || got != binary {
			t.Errorf("BinaryFor(%q) = %q, %v", harness, got, ok)
		}
	}
	if Known("nope") {
		t.Error("Known(nope) = true")
	}
	if !Available() {
		t.Error("Available() = false")
	}
}

// Real asset names recorded from each project's latest release listing at
// the time the patterns were written. The test pins pattern-to-asset
// matching for every supported platform.
func TestAssetPatterns(t *testing.T) {
	releases := map[string][]string{
		"cli/cli": {
			"gh_2.98.0_linux_amd64.tar.gz",
			"gh_2.98.0_linux_arm64.tar.gz",
			"gh_2.98.0_macOS_amd64.zip",
			"gh_2.98.0_macOS_arm64.zip",
			"gh_2.98.0_windows_amd64.zip",
			"gh_2.98.0_checksums.txt",
		},
		"openai/codex": {
			"codex-x86_64-unknown-linux-musl.tar.gz",
			"codex-aarch64-unknown-linux-musl.tar.gz",
			"codex-x86_64-apple-darwin.tar.gz",
			"codex-aarch64-apple-darwin.tar.gz",
			"codex-app-server-x86_64-unknown-linux-musl.tar.gz",
		},
		"anomalyco/opencode": {
			"opencode-linux-x64.tar.gz",
			"opencode-linux-x64-musl.tar.gz",
			"opencode-linux-arm64.tar.gz",
			"opencode-darwin-x64.zip",
			"opencode-darwin-arm64.zip",
		},
	}
	for harness, s := range specs {
		for _, st := range s.Strategies {
			r, ok := st.(*releaseInstall)
			if !ok {
				continue
			}
			assets := releases[r.Repo]
			if assets == nil {
				t.Fatalf("no recorded assets for %s", r.Repo)
			}
			for platform, pattern := range r.Assets {
				var matched []string
				for _, a := range assets {
					if ok, err := filepathMatch(pattern, a); err != nil {
						t.Fatalf("bad pattern %q: %v", pattern, err)
					} else if ok {
						matched = append(matched, a)
					}
				}
				if len(matched) != 1 {
					t.Errorf("%s %s: pattern %q matched %v, want exactly one", harness, platform, pattern, matched)
				}
			}
		}
	}
}

func filepathMatch(pattern, name string) (bool, error) {
	return filepath.Match(pattern, name)
}

func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func withBinDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := binDir
	binDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { binDir = orig })
	return dir
}

func TestInstallFromTarGz(t *testing.T) {
	dir := withBinDir(t)
	data := makeTarGz(t, map[string]string{
		"gh_2.98.0_linux_amd64/LICENSE":    "license",
		"gh_2.98.0_linux_amd64/bin/gh":     "#!/bin/sh\necho gh\n",
		"gh_2.98.0_linux_amd64/share/data": "data",
	})
	tmp := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installFromArchive(tmp, "gh_2.98.0_linux_amd64.tar.gz", "gh", t.Logf); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "gh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "echo gh") {
		t.Errorf("wrong file installed: %q", got)
	}
	info, _ := os.Stat(filepath.Join(dir, "gh"))
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", info.Mode().Perm())
	}
}

// Regression: the gh tarball ships man pages (gh-agent-task-create.1, ...)
// whose names share the binary prefix; bin/gh must still win.
func TestInstallFromTarGzPrefersExactMatchOverManPages(t *testing.T) {
	dir := withBinDir(t)
	data := makeTarGz(t, map[string]string{
		"gh_2.98.0_linux_amd64/share/man/man1/gh-agent-task-create.1": ".TH man page",
		"gh_2.98.0_linux_amd64/share/man/man1/gh-auth.1":              ".TH man page",
		"gh_2.98.0_linux_amd64/bin/gh":                                "real-binary",
	})
	tmp := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installFromArchive(tmp, "gh.tar.gz", "gh", t.Logf); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "gh"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "real-binary" {
		t.Errorf("installed %q, want the bin/gh entry", got)
	}
}

// Among prefix-only matches the largest entry wins (real binaries dwarf
// stray text files).
func TestInstallPrefixMatchPrefersLargest(t *testing.T) {
	dir := withBinDir(t)
	data := makeTarGz(t, map[string]string{
		"codex-notes":                     "tiny",
		"codex-x86_64-unknown-linux-musl": "the-much-larger-real-binary-content",
	})
	tmp := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installFromArchive(tmp, "codex.tar.gz", "codex", t.Logf); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "codex"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "larger-real-binary") {
		t.Errorf("installed %q, want the larger entry", got)
	}
}

func TestInstallFromTarGzPrefixedBinary(t *testing.T) {
	dir := withBinDir(t)
	data := makeTarGz(t, map[string]string{
		"codex-x86_64-unknown-linux-musl": "binary",
	})
	tmp := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installFromArchive(tmp, "codex-x86_64-unknown-linux-musl.tar.gz", "codex", t.Logf); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "codex")); err != nil {
		t.Fatal(err)
	}
}

func TestInstallFromZip(t *testing.T) {
	dir := withBinDir(t)
	data := makeZip(t, map[string]string{
		"opencode": "binary",
		"README":   "readme",
	})
	tmp := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installFromArchive(tmp, "opencode-darwin-arm64.zip", "opencode", t.Logf); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "opencode")); err != nil {
		t.Fatal(err)
	}
}

func TestInstallMissingBinaryInArchive(t *testing.T) {
	withBinDir(t)
	data := makeTarGz(t, map[string]string{"README": "no binary here"})
	tmp := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installFromArchive(tmp, "x.tar.gz", "gh", t.Logf); err == nil {
		t.Fatal("want error for archive without the binary")
	}
}

func TestReleaseInstallEndToEnd(t *testing.T) {
	dir := withBinDir(t)
	asset := makeTarGz(t, map[string]string{"gh_9.9.9_linux_amd64/bin/gh": "gh-binary"})

	mux := http.NewServeMux()
	var assetURL string
	mux.HandleFunc("/repos/cli/cli/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v9.9.9",
			"assets": []map[string]string{
				{"name": "gh_9.9.9_checksums.txt", "browser_download_url": assetURL + "/sums"},
				{"name": "gh_9.9.9_linux_amd64.tar.gz", "browser_download_url": assetURL + "/gh.tar.gz"},
			},
		})
	})
	mux.HandleFunc("/dl/gh.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(asset)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	assetURL = srv.URL + "/dl"

	origAPI, origArch := apiBase, osArch
	apiBase = srv.URL
	osArch = "linux/amd64"
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Cleanup(func() { apiBase, osArch = origAPI, origArch })

	r := &releaseInstall{
		Repo:        "cli/cli",
		InstallName: "gh",
		Assets:      map[string]string{"linux/amd64": "gh_*_linux_amd64.tar.gz"},
	}
	if !r.ready() {
		t.Fatal("ready() = false")
	}
	if err := r.run(context.Background(), t.Logf); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "gh"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "gh-binary" {
		t.Errorf("installed content = %q", got)
	}
}

func TestInstalledWithStubOnPath(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "codex")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if !Installed("codex") {
		t.Error("Installed(codex) = false with stub on PATH")
	}
	if Installed("gh") {
		t.Error("Installed(gh) = true without stub")
	}
	if Installed("nope") {
		t.Error("Installed(nope) = true")
	}
}

func TestInstalledSeesFreshBinDir(t *testing.T) {
	dir := withBinDir(t)
	t.Setenv("PATH", t.TempDir())
	if Installed("gh") {
		t.Error("Installed(gh) = true before install")
	}
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !Installed("gh") {
		t.Error("Installed(gh) = false with binary in install dir")
	}
}

func TestStrategySelectionSkipsUnready(t *testing.T) {
	withBinDir(t)
	// No bash, no npm on PATH: script and npm strategies must be skipped
	// and Install must fail with a clear error instead of hanging.
	t.Setenv("PATH", t.TempDir())
	err := Install(context.Background(), "agy", t.Logf)
	if err == nil {
		t.Fatal("want error when no strategy is usable")
	}
	if !strings.Contains(err.Error(), "no usable install method") {
		t.Errorf("error = %v", err)
	}
}

func TestInstallUnknownHarness(t *testing.T) {
	if err := Install(context.Background(), "nope", nil); err == nil {
		t.Fatal("want error for unknown harness")
	}
}

func TestInstallAllJoinsErrors(t *testing.T) {
	withBinDir(t)
	t.Setenv("PATH", t.TempDir())
	var lines []string
	logf := func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	err := InstallAll(context.Background(), []string{"nope", "agy"}, logf)
	if err == nil {
		t.Fatal("want joined error")
	}
	for _, want := range []string{"nope", "agy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %v does not mention %s", err, want)
		}
	}
}
