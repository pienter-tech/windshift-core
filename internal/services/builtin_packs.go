package services

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
)

// Built-in framework packs ship inside the binary the same way schema SQL
// does. Each pack is a directory under packs/ holding the same document set a
// pack archive contains — manifest.json plus the files the manifest references
// — so the manifest, schema, and content stay reviewable as JSON instead of a
// committed tar. A pack assembled here is byte-for-byte the document set an
// upload would carry, and validatePackManifest is the shared contract for
// both paths.

//go:embed packs
var builtinPackFS embed.FS

const builtinPacksRoot = "packs"

// ErrBuiltinPackNotFound reports an unknown built-in pack name.
var ErrBuiltinPackNotFound = errors.New("built-in pack not found")

// BuiltinPackSummary is the list projection of one embedded pack.
type BuiltinPackSummary struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	HasContent  bool     `json:"has_content"`
	Plugins     []string `json:"plugins,omitempty"`
}

// builtinPack pairs a parsed archive with its list projection.
type builtinPack struct {
	archive *PackArchive
	summary BuiltinPackSummary
}

// The embedded packs are parsed once. A malformed built-in pack is a build
// defect, so the error is sticky: every accessor reports it until fixed.
var builtinPacksIndex struct {
	sync.Once
	packs map[string]*builtinPack
	order []string
	err   error
}

// loadBuiltinPacks walks the embedded packs tree and parses every pack.
func loadBuiltinPacks() (packs map[string]*builtinPack, order []string, err error) {
	entries, err := fs.ReadDir(builtinPackFS, builtinPacksRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("built-in packs: read %q: %w", builtinPacksRoot, err)
	}
	loadedPacks := make(map[string]*builtinPack, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		dir := path.Join(builtinPacksRoot, name)
		files := map[string][]byte{}
		walkErr := fs.WalkDir(builtinPackFS, dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel := strings.TrimPrefix(p, dir+"/")
			data, err := builtinPackFS.ReadFile(p)
			if err != nil {
				return err
			}
			files[rel] = data
			return nil
		})
		if walkErr != nil {
			return nil, nil, fmt.Errorf("built-in pack %q: %w", name, walkErr)
		}
		manifestRaw, ok := files["manifest.json"]
		if !ok {
			return nil, nil, fmt.Errorf("built-in pack %q: manifest.json is required", name)
		}
		manifest := &PackManifest{}
		if err := json.Unmarshal(manifestRaw, manifest); err != nil {
			return nil, nil, fmt.Errorf("built-in pack %q: manifest is not valid JSON: %w", name, err)
		}
		if err := validatePackManifest(manifest, files); err != nil {
			return nil, nil, fmt.Errorf("built-in pack %q: %w", name, err)
		}
		if manifest.Name != name {
			return nil, nil, fmt.Errorf("built-in pack %q: manifest name %q must match its directory", name, manifest.Name)
		}
		summary := BuiltinPackSummary{
			Name:        manifest.Name,
			Version:     manifest.Version,
			Description: manifest.Description,
			HasContent:  manifest.Content != nil && manifest.Content.WorkspaceBundle != "",
		}
		for _, ref := range manifest.Plugins {
			summary.Plugins = append(summary.Plugins, ref.Name)
		}
		loadedPacks[manifest.Name] = &builtinPack{
			archive: &PackArchive{Manifest: manifest, Files: files},
			summary: summary,
		}
		order = append(order, manifest.Name)
	}
	sort.Strings(order)
	return loadedPacks, order, nil
}

// builtinPacks lookup parses the embedded tree once and caches the result.
func builtinPacks() (packs map[string]*builtinPack, order []string, err error) {
	builtinPacksIndex.Do(func() {
		builtinPacksIndex.packs, builtinPacksIndex.order, builtinPacksIndex.err = loadBuiltinPacks()
	})
	return builtinPacksIndex.packs, builtinPacksIndex.order, builtinPacksIndex.err
}

// BuiltinPacks lists the embedded packs, sorted by name.
func BuiltinPacks() ([]BuiltinPackSummary, error) {
	packs, order, err := builtinPacks()
	if err != nil {
		return nil, err
	}
	out := make([]BuiltinPackSummary, 0, len(order))
	for _, name := range order {
		out = append(out, packs[name].summary)
	}
	return out, nil
}

// BuiltinPackArchive returns the parsed archive for a built-in pack.
func BuiltinPackArchive(name string) (*PackArchive, error) {
	packs, _, err := builtinPacks()
	if err != nil {
		return nil, err
	}
	p, ok := packs[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrBuiltinPackNotFound, name)
	}
	return p.archive, nil
}

// ValidateBuiltinPacks parses every embedded pack and returns the first
// problem. Server startup calls this so a malformed shipped pack fails fast
// instead of surfacing at first apply.
func ValidateBuiltinPacks() error {
	_, _, err := builtinPacks()
	return err
}
