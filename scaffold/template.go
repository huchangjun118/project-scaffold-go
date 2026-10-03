package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
)

// Template is a loaded, ready-to-render template.
type Template struct {
	// Dir is the absolute template directory.
	Dir string
	// Manifest is the decoded template description.
	Manifest *Manifest
	// Warnings lists non-fatal compatibility notices (e.g. deprecated
	// fields that were mapped deterministically).
	Warnings []string
}

// LoadTemplate loads a template from dir: it decodes the manifest, checks
// spec version compatibility, maps deprecated fields with warnings and
// verifies that every referenced source file exists.
func LoadTemplate(dir string) (*Template, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("scaffold: cannot resolve template directory %s: %w", dir, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("scaffold: cannot access template directory %s: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("scaffold: template path %s is not a directory", abs)
	}

	manifest, deprecated, err := loadManifest(abs)
	if err != nil {
		return nil, err
	}

	var warnings []string
	for _, d := range deprecated {
		warnings = append(warnings,
			fmt.Sprintf("deprecated field %q used: %s", d.Field, d.Mapping))
	}

	for _, f := range manifest.Files {
		src := filepath.Join(abs, filepath.FromSlash(f.Source))
		si, err := os.Stat(src)
		if err != nil {
			return nil, fmt.Errorf("scaffold: template %s references missing source file %q: %w",
				abs, f.Source, err)
		}
		if si.IsDir() {
			return nil, fmt.Errorf("scaffold: template source %q is a directory, not a file", f.Source)
		}
	}

	return &Template{Dir: abs, Manifest: manifest, Warnings: warnings}, nil
}
