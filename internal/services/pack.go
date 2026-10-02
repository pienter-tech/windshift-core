package services

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
)

// A framework pack is the installable unit binding the three layers:
// schema (a configuration-set template file), content (a workspace bundle),
// and functionality (plugin references). The framework itself is pack
// content, not product code.

const (
	PackSchemaVersion = 1
	PackKind          = "windshift.pack"

	// Pack archives are configuration data; the HTTP layer already caps the
	// compressed upload. These bound what that upload can expand to while every
	// entry is read into memory, so a gzip bomb cannot exhaust the process.
	packMaxTotalBytes = 128 << 20 // 128 MiB decompressed across the whole archive
	packMaxEntryBytes = 64 << 20  // 64 MiB for any single archive entry
)

type PackManifest struct {
	SchemaVersion int                     `json:"schema_version"`
	Kind          string                  `json:"kind"`
	Name          string                  `json:"name"`
	Version       string                  `json:"version"`
	Description   string                  `json:"description,omitempty"`
	Schema        PackSchemaSection       `json:"schema"`
	Content       *PackContentSection     `json:"content,omitempty"`
	Plugins       []PackPluginRef         `json:"plugins,omitempty"`
	Conformance   *PackConformanceSection `json:"conformance,omitempty"`
}

// PackSchemaSection declares the schema layer: the configuration-set
// template file inside the archive.
type PackSchemaSection struct {
	ConfigurationSet string `json:"configuration_set"`
}

// PackContentSection declares the content layer: the workspace bundle file
// inside the archive.
type PackContentSection struct {
	WorkspaceBundle string `json:"workspace_bundle"`
}

// PackPluginRef references a plugin by name with a minimum version. Plugin
// binaries are never distributed inside the pack.
type PackPluginRef struct {
	Name       string `json:"name"`
	MinVersion string `json:"min_version"`
}

// PackConformanceSection declares the apply-time verification: which
// template the conformance check runs against.
type PackConformanceSection struct {
	ConfigurationSet string `json:"configuration_set,omitempty"`
	// RequiredStatuses names statuses that must exist after the schema stage,
	// by exact name (case-insensitive). The pack's content depends on them —
	// e.g. queue views keyed on "Open"/"Closed" — so a missing status fails
	// the apply. Shared-registry attributes are never gated: existing statuses
	// are adopted as the instance has them.
	RequiredStatuses []string `json:"required_statuses,omitempty"`
}

// PackArchive is a parsed pack: the manifest plus every file it may
// reference, keyed by archive-relative path.
type PackArchive struct {
	Manifest *PackManifest
	Files    map[string][]byte
}

// ConfigurationSetTemplate returns the schema-layer template.
func (p *PackArchive) ConfigurationSetTemplate() (*ConfigSetTemplate, error) {
	raw, ok := p.Files[p.Manifest.Schema.ConfigurationSet]
	if !ok {
		return nil, fmt.Errorf("pack: configuration-set file %q is missing from the archive", p.Manifest.Schema.ConfigurationSet)
	}
	tpl := &ConfigSetTemplate{}
	if err := json.Unmarshal(raw, tpl); err != nil {
		return nil, fmt.Errorf("pack: configuration-set file %q is not a valid template: %w", p.Manifest.Schema.ConfigurationSet, err)
	}
	return tpl, nil
}

// WorkspaceBundleFile returns the content-layer bundle, if declared.
func (p *PackArchive) WorkspaceBundleFile() (bundle map[string]any, declared bool, err error) {
	if p.Manifest.Content == nil || p.Manifest.Content.WorkspaceBundle == "" {
		return nil, false, nil
	}
	raw, ok := p.Files[p.Manifest.Content.WorkspaceBundle]
	if !ok {
		return nil, false, fmt.Errorf("pack: workspace-bundle file %q is missing from the archive", p.Manifest.Content.WorkspaceBundle)
	}
	bundle = map[string]any{}
	if err = json.Unmarshal(raw, &bundle); err != nil {
		return nil, false, fmt.Errorf("pack: workspace-bundle file %q is not valid JSON: %w", p.Manifest.Content.WorkspaceBundle, err)
	}
	return bundle, true, nil
}

var packSemverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+`)

// ParsePackArchive validates a gzipped-tar pack archive and its manifest.
// Schema violations (unknown kind, missing version, dangling file
// references) are rejected here, before anything is applied.
func ParsePackArchive(data []byte) (*PackArchive, error) {
	return parsePackArchive(data, packMaxTotalBytes, packMaxEntryBytes)
}

// parsePackArchive is ParsePackArchive with explicit decompressed-size
// budgets, so tests can exercise the limits without materializing an archive
// the size of the production caps.
func parsePackArchive(data []byte, maxTotalBytes, maxEntryBytes int64) (*PackArchive, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("pack: archive must be a gzipped tar archive: %w", err)
	}
	defer func() { _ = gz.Close() }()

	files := map[string][]byte{}
	var manifestRaw []byte
	var totalRead int64
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("pack: reading archive: %w", err)
		}
		name := path.Clean(header.Name)
		if strings.HasPrefix(name, "..") || path.IsAbs(name) {
			return nil, fmt.Errorf("pack: refusing unsafe archive entry %q", header.Name)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		// Cap each entry by the per-entry limit and the archive-wide budget
		// still available. Reading one byte past the cap distinguishes an
		// overrun from an entry that fits exactly.
		remaining := maxTotalBytes - totalRead
		readLimit := maxEntryBytes
		if remaining < readLimit {
			readLimit = remaining
		}
		content, err := io.ReadAll(io.LimitReader(tr, readLimit+1))
		if err != nil {
			return nil, fmt.Errorf("pack: reading %q: %w", name, err)
		}
		if int64(len(content)) > readLimit {
			if remaining < maxEntryBytes {
				return nil, fmt.Errorf("pack: decompressed archive exceeds the %d byte limit", maxTotalBytes)
			}
			return nil, fmt.Errorf("pack: entry %q exceeds the %d byte limit", name, maxEntryBytes)
		}
		totalRead += int64(len(content))
		files[name] = content
		lower := strings.ToLower(name)
		if lower == "manifest.json" || lower == "pack.json" {
			if manifestRaw == nil {
				manifestRaw = content
			}
		}
	}
	if manifestRaw == nil {
		return nil, errors.New(`pack: archive has no manifest.json at its root`)
	}

	manifest := &PackManifest{}
	if err := json.Unmarshal(manifestRaw, manifest); err != nil {
		return nil, fmt.Errorf("pack: manifest is not valid JSON: %w", err)
	}
	if err := validatePackManifest(manifest, files); err != nil {
		return nil, err
	}

	return &PackArchive{Manifest: manifest, Files: files}, nil
}

// validatePackManifest applies the manifest contract shared by uploaded tar
// archives and packs assembled from embedded assets: kind, schema version,
// name, semver, and every declared file reference must resolve. The caller
// supplies the archive-relative file map; the check never reads the archive
// itself.
func validatePackManifest(manifest *PackManifest, files map[string][]byte) error {
	if manifest == nil {
		return errors.New("pack: manifest is required")
	}
	if manifest.Kind != PackKind {
		return fmt.Errorf("pack: unsupported kind %q (want %q)", manifest.Kind, PackKind)
	}
	if manifest.SchemaVersion != PackSchemaVersion {
		return fmt.Errorf("pack: unsupported schema_version %d (want %d)", manifest.SchemaVersion, PackSchemaVersion)
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return errors.New("pack: manifest name is required")
	}
	if !packSemverPattern.MatchString(manifest.Version) {
		return fmt.Errorf("pack: version %q is not a semver (want major.minor.patch)", manifest.Version)
	}
	if manifest.Schema.ConfigurationSet == "" {
		return errors.New("pack: schema.configuration_set is required")
	}
	for _, ref := range manifest.Plugins {
		if strings.TrimSpace(ref.Name) == "" {
			return errors.New("pack: plugin reference name is required")
		}
		if !packSemverPattern.MatchString(ref.MinVersion) {
			return fmt.Errorf("pack: plugin %q min_version %q is not a semver", ref.Name, ref.MinVersion)
		}
	}
	// Dangling file references.
	if _, ok := files[manifest.Schema.ConfigurationSet]; !ok {
		return fmt.Errorf("pack: schema file %q is not in the archive", manifest.Schema.ConfigurationSet)
	}
	if manifest.Content != nil && manifest.Content.WorkspaceBundle != "" {
		if _, ok := files[manifest.Content.WorkspaceBundle]; !ok {
			return fmt.Errorf("pack: content file %q is not in the archive", manifest.Content.WorkspaceBundle)
		}
	}
	if manifest.Conformance != nil && manifest.Conformance.ConfigurationSet != "" {
		if _, ok := files[manifest.Conformance.ConfigurationSet]; !ok {
			return fmt.Errorf("pack: conformance file %q is not in the archive", manifest.Conformance.ConfigurationSet)
		}
	}
	return nil
}

// compareSemver compares dotted numeric versions. Missing components count
// as zero ("1.2" equals "1.2.0"). Non-numeric components fall back to
// numeric prefixes ("1.2.3-beta" compares as 1.2.3).
func compareSemver(found, required string) (int, error) {
	a, err := semverComponents(found)
	if err != nil {
		return 0, err
	}
	b, err := semverComponents(required)
	if err != nil {
		return 0, err
	}
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1, nil
			}
			return 1, nil
		}
	}
	return 0, nil
}

func semverComponents(version string) ([3]int, error) {
	var out [3]int
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	if version == "" {
		return out, errors.New("empty version")
	}
	if idx := strings.IndexAny(version, "-+"); idx >= 0 {
		version = version[:idx]
	}
	parts := strings.Split(version, ".")
	if len(parts) > 3 {
		return out, fmt.Errorf("version %q is not a semver", version)
	}
	for i := 0; i < 3; i++ {
		if i >= len(parts) {
			break
		}
		num := 0
		digits := 0
		for _, c := range parts[i] {
			if c < '0' || c > '9' {
				break
			}
			num = num*10 + int(c-'0')
			digits++
		}
		if digits == 0 {
			return out, fmt.Errorf("version %q is not a semver", version)
		}
		out[i] = num
	}
	return out, nil
}
