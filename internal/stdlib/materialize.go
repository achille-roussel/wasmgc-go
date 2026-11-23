package stdlib

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/achille-roussel/wasmgc-go/internal/load"
)

// Paths holds locations for the pin, manifest, overlays, and rewrite sources.
type Paths struct {
	ModuleRoot string // repository root
	PIN        string // std/PIN
	Manifest   string // std/manifest.json
	Overlay    string // std/overlay
	Rewrite    string // std/src
	CacheDir   string // typically UserCacheDir/wasmgc-go
}

// DefaultPaths resolves paths from the module root.
func DefaultPaths(moduleRoot string) (Paths, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		ModuleRoot: moduleRoot,
		PIN:        filepath.Join(moduleRoot, "std", "PIN"),
		Manifest:   filepath.Join(moduleRoot, "std", "manifest.json"),
		Overlay:    filepath.Join(moduleRoot, "std", "overlay"),
		Rewrite:    filepath.Join(moduleRoot, "std", "src"),
		CacheDir:   filepath.Join(cache, "wasmgc-go"),
	}, nil
}

// Materialized is a ready-to-use cache GOROOT for GOOS=js GOARCH=wasmgc.
type Materialized struct {
	GOROOT   string
	Pin      Pin
	Manifest *Manifest
}

// Materialize builds $(CacheDir)/goroot-<version>/ from the pin's source tree,
// applying the manifest: rewrite replaces the file list, overlays replace named
// files, deletions drop files. Vendored packages are re-filtered for wasmgc.
func Materialize(p Paths) (*Materialized, error) {
	pin, err := LoadPIN(p.PIN)
	if err != nil {
		return nil, err
	}
	man, err := LoadManifest(p.Manifest)
	if err != nil {
		return nil, err
	}
	if man.Pin != pin.Version {
		return nil, fmt.Errorf("stdlib: manifest pin %q != PIN %q", man.Pin, pin.Version)
	}

	if err := os.MkdirAll(p.CacheDir, 0o755); err != nil {
		return nil, err
	}

	srcRoot, err := ensureUpstreamSrc(p, pin)
	if err != nil {
		return nil, err
	}

	goroot := filepath.Join(p.CacheDir, "goroot-"+pin.Version)
	marker := filepath.Join(goroot, ".wasmgc-materialized")
	wantMarker := materializeMarker(pin, p)

	if data, err := os.ReadFile(marker); err == nil && string(data) == wantMarker {
		return &Materialized{GOROOT: goroot, Pin: pin, Manifest: man}, nil
	}

	// Rebuild.
	_ = os.RemoveAll(goroot)
	if err := os.MkdirAll(filepath.Join(goroot, "src"), 0o755); err != nil {
		return nil, err
	}
	// Minimal GOROOT bits go list / tools sometimes want.
	if err := writeVERSION(goroot, pin.Version); err != nil {
		return nil, err
	}

	upstreamSrc := filepath.Join(srcRoot, "src")
	filter := load.DefaultFilter()

	// Walk every package directory under upstream src (and ensure rewrites exist).
	pkgs, err := listPackageDirs(upstreamSrc)
	if err != nil {
		return nil, err
	}
	// Also include rewrite-only packages that might not exist upstream with our path.
	for path, e := range man.Packages {
		if e.Tier == TierRewrite {
			pkgs[path] = struct{}{}
		}
	}

	for importPath := range pkgs {
		entry := man.Lookup(importPath)
		dst := filepath.Join(goroot, "src", filepath.FromSlash(importPath))
		switch entry.Tier {
		case TierOut:
			// empty directory signals out; import is an error at load time
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return nil, err
			}
			continue
		case TierRewrite:
			src := filepath.Join(p.Rewrite, filepath.FromSlash(importPath))
			if err := copyGoTree(src, dst, nil); err != nil {
				return nil, fmt.Errorf("stdlib: rewrite %s: %w", importPath, err)
			}
		case TierVendor, TierOverlay:
			src := filepath.Join(upstreamSrc, filepath.FromSlash(importPath))
			if _, err := os.Stat(src); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			keep, err := filter.ListGoFiles(src)
			if err != nil {
				return nil, fmt.Errorf("stdlib: filter %s: %w", importPath, err)
			}
			deleteSet := map[string]bool{}
			for _, d := range entry.Delete {
				deleteSet[d] = true
			}
			overlaySet := map[string]bool{}
			for _, o := range entry.Overlays {
				overlaySet[o] = true
			}
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return nil, err
			}
			for _, f := range keep {
				base := filepath.Base(f)
				if deleteSet[base] || overlaySet[base] {
					continue
				}
				if err := copyFile(f, filepath.Join(dst, base)); err != nil {
					return nil, err
				}
			}
			for _, o := range entry.Overlays {
				from := filepath.Join(p.Overlay, filepath.FromSlash(importPath), o)
				if err := copyFile(from, filepath.Join(dst, o)); err != nil {
					return nil, fmt.Errorf("stdlib: overlay %s/%s: %w", importPath, o, err)
				}
				if err := checkOverlayHeader(filepath.Join(dst, o), pin.Version, importPath, o); err != nil {
					return nil, err
				}
			}
		}
	}

	if err := os.WriteFile(marker, []byte(wantMarker), 0o644); err != nil {
		return nil, err
	}
	return &Materialized{GOROOT: goroot, Pin: pin, Manifest: man}, nil
}

func materializeMarker(pin Pin, p Paths) string {
	// Invalidate when PIN, manifest, overlay, or rewrite tree change.
	h := sha256.New()
	for _, path := range []string{p.PIN, p.Manifest} {
		data, _ := os.ReadFile(path)
		h.Write(data)
	}
	_ = filepath.Walk(p.Overlay, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, _ := os.ReadFile(path)
		h.Write([]byte(path))
		h.Write(data)
		return nil
	})
	_ = filepath.Walk(p.Rewrite, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, _ := os.ReadFile(path)
		h.Write([]byte(path))
		h.Write(data)
		return nil
	})
	return pin.Version + "\n" + hex.EncodeToString(h.Sum(nil)) + "\n"
}

func writeVERSION(goroot, version string) error {
	return os.WriteFile(filepath.Join(goroot, "VERSION"), []byte(version+"\n"), 0o644)
}

// ensureUpstreamSrc returns a directory containing the extracted Go source
// (with a src/ subdirectory). Prefer a downloaded, checksum-verified tarball.
// If the running toolchain's GOROOT VERSION matches the pin, it may be used as
// the upstream tree without re-downloading (still not committed into the repo).
func ensureUpstreamSrc(p Paths, pin Pin) (string, error) {
	extracted := filepath.Join(p.CacheDir, "src-"+pin.Version)
	if _, err := os.Stat(filepath.Join(extracted, "src")); err == nil {
		if ok, _ := verifyExtractedVERSION(extracted, pin.Version); ok {
			return extracted, nil
		}
	}

	// Fast path: running toolchain matches the pin (avoids a 34MB download when
	// the developer already has go1.27.4). The tarball path remains the
	// authoritative pin for CI and mismatched toolchains.
	root := os.Getenv("GOROOT")
	if root == "" {
		if out, err := exec.Command("go", "env", "GOROOT").Output(); err == nil {
			root = strings.TrimSpace(string(out))
		}
	}
	if root != "" {
		if ok, _ := verifyExtractedVERSION(root, pin.Version); ok {
			if _, err := os.Stat(filepath.Join(root, "src", "fmt")); err == nil {
				return root, nil
			}
		}
	}

	// Download tarball.
	tarball := filepath.Join(p.CacheDir, pin.Version+".src.tar.gz")
	if err := downloadVerified(pin.URL, tarball, pin.SHA256); err != nil {
		return "", err
	}
	_ = os.RemoveAll(extracted)
	if err := os.MkdirAll(extracted, 0o755); err != nil {
		return "", err
	}
	cmd := exec.Command("tar", "-xzf", tarball, "-C", extracted, "--strip-components=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("stdlib: extract: %v\n%s", err, out)
	}
	if ok, err := verifyExtractedVERSION(extracted, pin.Version); !ok {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("stdlib: extracted VERSION is not %s", pin.Version)
	}
	return extracted, nil
}

func verifyExtractedVERSION(root, want string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return false, err
	}
	line := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	return line == want || strings.HasPrefix(line, want), nil
}

func downloadVerified(url, dest, wantSHA string) error {
	if data, err := os.ReadFile(dest); err == nil {
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) == wantSHA {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	resp, err := http.Get(url)
	if err != nil {
		f.Close()
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		f.Close()
		return fmt.Errorf("stdlib: download %s: %s", url, resp.Status)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != wantSHA {
		_ = os.Remove(tmp)
		return fmt.Errorf("stdlib: sha256 mismatch: got %s want %s", got, wantSHA)
	}
	return os.Rename(tmp, dest)
}

func checkOverlayHeader(path, version, importPath, base string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	first, _, _ := strings.Cut(string(data), "\n")
	want := fmt.Sprintf("// overlay of %s %s/%s", version, importPath, base)
	if strings.TrimSpace(first) != want {
		return fmt.Errorf("stdlib: overlay header mismatch in %s:\n  got  %q\n  want %q", path, first, want)
	}
	return nil
}

func listPackageDirs(srcRoot string) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	err := filepath.Walk(srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		base := info.Name()
		if base == "testdata" || base == "vendor" || strings.HasPrefix(base, ".") {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// Skip cmd entirely.
		if rel == "cmd" || strings.HasPrefix(rel, "cmd"+string(os.PathSeparator)) {
			return filepath.SkipDir
		}
		// A package dir has at least one .go file.
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
				out[filepath.ToSlash(rel)] = struct{}{}
				break
			}
		}
		return nil
	})
	return out, err
}

func copyGoTree(src, dst string, filter *load.FilterConfig) error {
	if _, err := os.Stat(src); err != nil {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
