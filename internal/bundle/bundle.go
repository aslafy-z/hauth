// Package bundle packs a harness's credential files into a versioned
// tarball plus a manifest of per-file hashes. The per-file granularity is
// what source detection is built on: only files marked Identity participate
// in deciding whether local state matches what the store wrote.
package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FileEntry describes one file inside a bundle. Path is slash-separated and
// relative to $HOME.
type FileEntry struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Mode     uint32 `json:"mode"`
	Size     int64  `json:"size"`
	Identity bool   `json:"identity"`
}

// Manifest describes a bundle.
type Manifest struct {
	Harness   string      `json:"harness"`
	Instance  string      `json:"instance"` // instance that created the bundle
	CreatedAt time.Time   `json:"created_at"`
	Files     []FileEntry `json:"files"`
}

// Bundle is a manifest plus the tar.gz of the files it describes.
type Bundle struct {
	Manifest Manifest
	Data     []byte
}

// Filter optionally rewrites or excludes a file before it enters a bundle.
// Returning ok=false excludes the file.
type Filter func(rel string, content []byte) (out []byte, ok bool)

// ExpandGlobs resolves credential path globs (relative to home) to existing
// regular files, returned as sorted slash-separated relative paths.
// Supported forms: literal paths, standard filepath.Glob patterns, and a
// trailing "/**" for a recursive directory match.
func ExpandGlobs(home string, patterns []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(abs string) {
		info, err := os.Lstat(abs)
		if err != nil || !info.Mode().IsRegular() {
			return
		}
		rel, err := filepath.Rel(home, abs)
		if err != nil {
			return
		}
		rel = filepath.ToSlash(rel)
		if !seen[rel] {
			seen[rel] = true
			out = append(out, rel)
		}
	}
	for _, p := range patterns {
		p = filepath.ToSlash(p)
		if prefix, ok := strings.CutSuffix(p, "/**"); ok {
			root := filepath.Join(home, filepath.FromSlash(prefix))
			_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil // unreadable subtree: skip, not fatal
				}
				if !d.IsDir() {
					add(path)
				}
				return nil
			})
			continue
		}
		matches, err := filepath.Glob(filepath.Join(home, filepath.FromSlash(p)))
		if err != nil {
			return nil, fmt.Errorf("bad pattern %q: %w", p, err)
		}
		for _, m := range matches {
			add(m)
		}
	}
	sort.Strings(out)
	return out, nil
}

// MatchesPattern reports whether rel (slash-separated) matches one of the
// patterns, using the same semantics as ExpandGlobs.
func MatchesPattern(rel string, patterns []string) bool {
	for _, p := range patterns {
		p = filepath.ToSlash(p)
		if prefix, ok := strings.CutSuffix(p, "/**"); ok {
			if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
				return true
			}
			continue
		}
		if ok, _ := path.Match(p, rel); ok {
			return true
		}
		if p == rel {
			return true
		}
	}
	return false
}

// Pack builds a bundle from the files matching paths under home. identity
// patterns mark which files participate in source detection. filter may be
// nil.
func Pack(home, harness, instance string, paths, identity []string, filter Filter) (Bundle, error) {
	rels, err := ExpandGlobs(home, paths)
	if err != nil {
		return Bundle{}, err
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	man := Manifest{Harness: harness, Instance: instance, CreatedAt: time.Now().UTC()}
	for _, rel := range rels {
		abs := filepath.Join(home, filepath.FromSlash(rel))
		content, err := os.ReadFile(abs)
		if err != nil {
			return Bundle{}, fmt.Errorf("read %s: %w", rel, err)
		}
		if filter != nil {
			var ok bool
			content, ok = filter(rel, content)
			if !ok {
				continue
			}
		}
		sum := sha256.Sum256(content)
		entry := FileEntry{
			Path:     rel,
			SHA256:   hex.EncodeToString(sum[:]),
			Mode:     0o600, // credential material: owner-only, always
			Size:     int64(len(content)),
			Identity: MatchesPattern(rel, identity),
		}
		man.Files = append(man.Files, entry)
		hdr := &tar.Header{
			Name:    rel,
			Mode:    0o600,
			Size:    int64(len(content)),
			ModTime: man.CreatedAt,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return Bundle{}, err
		}
		if _, err := tw.Write(content); err != nil {
			return Bundle{}, err
		}
	}
	if err := tw.Close(); err != nil {
		return Bundle{}, err
	}
	if err := gz.Close(); err != nil {
		return Bundle{}, err
	}
	return Bundle{Manifest: man, Data: buf.Bytes()}, nil
}

// Unpack restores a bundle under home. Every tar entry must be listed in the
// manifest; paths must be clean, relative, and free of "..". Files are
// written 0600 with 0700 parent directories.
func Unpack(b Bundle, home string) error {
	allowed := map[string]FileEntry{}
	for _, f := range b.Manifest.Files {
		allowed[f.Path] = f
	}
	gz, err := gzip.NewReader(bytes.NewReader(b.Data))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := path.Clean(hdr.Name)
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("bundle entry escapes home: %q", hdr.Name)
		}
		entry, ok := allowed[name]
		if !ok {
			return fmt.Errorf("bundle entry not in manifest: %q", name)
		}
		if hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("bundle entry %q is not a regular file", name)
		}
		content, err := io.ReadAll(io.LimitReader(tr, entry.Size+1))
		if err != nil {
			return err
		}
		if int64(len(content)) != entry.Size {
			return fmt.Errorf("bundle entry %q size mismatch", name)
		}
		sum := sha256.Sum256(content)
		if hex.EncodeToString(sum[:]) != entry.SHA256 {
			return fmt.Errorf("bundle entry %q hash mismatch", name)
		}
		abs := filepath.Join(home, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
			return err
		}
		if err := writeFileAtomic(abs, content, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func writeFileAtomic(abs string, content []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".hauth-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, abs)
}

// HashFile returns the hex sha256 of a file on disk, or "" if unreadable.
func HashFile(abs string) string {
	f, err := os.Open(abs)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// IdentityHash returns a stable hash over the identity files of a manifest,
// used as a quick equality check between two bundles.
func (m Manifest) IdentityHash() string {
	h := sha256.New()
	for _, f := range m.Files {
		if f.Identity {
			fmt.Fprintf(h, "%s\x00%s\x00", f.Path, f.SHA256)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// IdentityMatchesLocal reports whether every identity file in the manifest
// exists under home with the same hash, and no identity-classified file
// present on disk contradicts it. identityPatterns are the adapter's
// IdentityPaths, used to notice identity files that exist locally but are
// absent from the manifest.
func (m Manifest) IdentityMatchesLocal(home string, identityPatterns []string) bool {
	inManifest := map[string]string{}
	for _, f := range m.Files {
		if f.Identity {
			inManifest[f.Path] = f.SHA256
		}
	}
	for rel, want := range inManifest {
		abs := filepath.Join(home, filepath.FromSlash(rel))
		if HashFile(abs) != want {
			return false
		}
	}
	local, err := ExpandGlobs(home, identityPatterns)
	if err != nil {
		return false
	}
	for _, rel := range local {
		if _, ok := inManifest[rel]; !ok {
			return false
		}
	}
	return true
}
