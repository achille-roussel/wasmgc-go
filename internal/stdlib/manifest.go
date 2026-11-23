package stdlib

import (
	"encoding/json"
	"fmt"
	"os"
)

// Tier is the materialization strategy for a package.
type Tier string

const (
	TierRewrite Tier = "rewrite"
	TierVendor  Tier = "vendor"
	TierOverlay Tier = "overlay"
	TierOut     Tier = "out"
)

// PackageEntry describes one stdlib package in the manifest.
type PackageEntry struct {
	Tier     Tier     `json:"tier"`
	Overlays []string `json:"overlays,omitempty"` // basenames replaced from std/overlay
	Delete   []string `json:"delete,omitempty"`   // basenames dropped from the vendor set
	Notes    string   `json:"notes,omitempty"`
}

// Manifest is std/manifest.json.
type Manifest struct {
	Pin      string                  `json:"pin"`
	Packages map[string]PackageEntry `json:"packages"`
}

// LoadManifest reads and validates the manifest.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("stdlib: manifest: %w", err)
	}
	if m.Pin == "" {
		return nil, fmt.Errorf("stdlib: manifest missing pin")
	}
	if m.Packages == nil {
		m.Packages = map[string]PackageEntry{}
	}
	for path, e := range m.Packages {
		switch e.Tier {
		case TierRewrite, TierVendor, TierOverlay, TierOut:
		default:
			return nil, fmt.Errorf("stdlib: package %s: unknown tier %q", path, e.Tier)
		}
		if e.Tier == TierOverlay && len(e.Overlays) == 0 && len(e.Delete) == 0 {
			return nil, fmt.Errorf("stdlib: package %s: overlay tier needs overlays or delete", path)
		}
	}
	return &m, nil
}

// Lookup returns the entry for importPath. Unlisted packages default to vendor.
func (m *Manifest) Lookup(importPath string) PackageEntry {
	if e, ok := m.Packages[importPath]; ok {
		return e
	}
	return PackageEntry{Tier: TierVendor}
}

// Supported returns import paths with tier rewrite, vendor, or overlay.
func (m *Manifest) Supported() []string {
	var out []string
	for path, e := range m.Packages {
		if e.Tier == TierOut {
			continue
		}
		out = append(out, path)
	}
	return out
}
