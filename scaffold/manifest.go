package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Manifest is the on-disk template description (template.json). It declares
// the spec version, the variable model and the file generation rules.
type Manifest struct {
	// SpecVersion is the template format version. The engine rejects
	// templates whose spec version exceeds the supported range.
	SpecVersion string `json:"specVersion"`

	// Name is an optional human readable template name.
	Name string `json:"name,omitempty"`

	// Description is an optional human readable template description.
	Description string `json:"description,omitempty"`

	// Variables declares the variable model: types, defaults and validation.
	Variables []VariableSpec `json:"variables"`

	// Files declares the file generation rules, in declaration order.
	Files []FileSpec `json:"files"`
}

// VariableSpec describes a single template variable.
type VariableSpec struct {
	// Name is the variable identifier used in templates.
	Name string `json:"name"`

	// Type is one of: string, int, bool, enum, list.
	Type string `json:"type"`

	// Required forces the caller to supply a value (no default allowed).
	Required bool `json:"required,omitempty"`

	// Default is used when the caller does not supply a value.
	Default any `json:"default,omitempty"`

	// Description is an optional human readable description.
	Description string `json:"description,omitempty"`

	// Pattern is a regular expression the (string) value must match.
	Pattern string `json:"pattern,omitempty"`

	// Min / Max constrain int values (inclusive).
	Min *int64 `json:"min,omitempty"`
	Max *int64 `json:"max,omitempty"`

	// Values is the set of allowed values for enum variables.
	Values []string `json:"values,omitempty"`

	// ItemType is the element type for list variables (string or int).
	ItemType string `json:"itemType,omitempty"`
}

// FileSpec describes one generated file (or a repeated group of files when
// Each is set).
type FileSpec struct {
	// ID is an optional stable identifier other files can reference via
	// dependsOn / the ref template function.
	ID string `json:"id,omitempty"`

	// Source is the path to the source template file, relative to the
	// template directory.
	Source string `json:"source"`

	// Target is the output path relative to the workspace directory. It is
	// rendered as a template, so file names may vary with variables.
	Target string `json:"target"`

	// Condition is an optional template expression; the file is only
	// generated when it renders to a truthy value.
	Condition string `json:"condition,omitempty"`

	// Each names a list variable; when set, one file is generated per list
	// element (available as {{.item}} / {{.index}}).
	Each string `json:"each,omitempty"`

	// DependsOn lists file IDs (or source paths) that must be generated
	// before this file.
	DependsOn []string `json:"dependsOn,omitempty"`

	// Conflict overrides the global conflict strategy for this file:
	// skip, overwrite or merge.
	Conflict string `json:"conflict,omitempty"`
}

// SupportedSpecVersion is the newest template spec version this engine
// understands.
const SupportedSpecVersion = "1"

// manifestWire is the on-disk JSON shape. It embeds the canonical fields and
// adds deprecated aliases so they can be mapped deterministically instead of
// being silently ignored.
type manifestWire struct {
	Manifest

	// deprecated aliases
	Version   *string `json:"version,omitempty"`   // -> specVersion
	Vars      *[]VariableSpec `json:"vars,omitempty"` // -> variables
	Output    *string `json:"output,omitempty"`    // -> files[].target (handled per file? no: top-level alias only)
}

// deprecatedField records a deprecated field that was used and how it was
// handled.
type deprecatedField struct {
	Field   string
	Mapping string
}

// loadManifest reads and strictly decodes template.json from dir. Unknown
// fields are rejected; deprecated aliases are mapped with a recorded warning.
func loadManifest(dir string) (*Manifest, []deprecatedField, error) {
	path := filepath.Join(dir, "template.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("scaffold: cannot read template manifest %s: %w", path, err)
	}

	dec := jsonStrictDecoder(data)
	var wire manifestWire
	if err := dec.Decode(&wire); err != nil {
		if isUnknownFieldErr(err) {
			return nil, nil, fmt.Errorf("%w: %v", ErrUnknownField, err)
		}
		return nil, nil, fmt.Errorf("scaffold: cannot parse template manifest %s: %w", path, err)
	}

	var used []deprecatedField
	if wire.Version != nil {
		if wire.SpecVersion != "" {
			used = append(used, deprecatedField{"version", "ignored in favor of specVersion"})
		} else {
			wire.SpecVersion = *wire.Version
			used = append(used, deprecatedField{"version", "mapped to specVersion"})
		}
	}
	if wire.Vars != nil {
		if len(wire.Variables) > 0 {
			used = append(used, deprecatedField{"vars", "ignored in favor of variables"})
		} else {
			wire.Variables = *wire.Vars
			used = append(used, deprecatedField{"vars", "mapped to variables"})
		}
	}

	if err := checkSpecVersion(wire.SpecVersion); err != nil {
		return nil, nil, err
	}
	if err := validateManifestShape(&wire.Manifest); err != nil {
		return nil, nil, err
	}

	return &wire.Manifest, used, nil
}

// checkSpecVersion rejects templates whose declared spec version is newer
// than the engine supports.
func checkSpecVersion(declared string) error {
	if declared == "" {
		return fmt.Errorf("%w: manifest does not declare specVersion (engine supports %s)",
			ErrUnsupportedSpecVersion, SupportedSpecVersion)
	}
	d, err := strconv.Atoi(declared)
	if err != nil {
		return fmt.Errorf("%w: %q is not a numeric spec version (engine supports %s)",
			ErrUnsupportedSpecVersion, declared, SupportedSpecVersion)
	}
	s, _ := strconv.Atoi(SupportedSpecVersion)
	if d > s {
		return fmt.Errorf("%w: template declares specVersion %s, engine supports up to %s",
			ErrUnsupportedSpecVersion, declared, SupportedSpecVersion)
	}
	return nil
}

// validateManifestShape checks internal consistency of the manifest.
func validateManifestShape(m *Manifest) error {
	if len(m.Files) == 0 {
		return fmt.Errorf("scaffold: manifest declares no files")
	}
	seenNames := map[string]bool{}
	for _, v := range m.Variables {
		if v.Name == "" {
			return fmt.Errorf("scaffold: manifest contains a variable without a name")
		}
		if seenNames[v.Name] {
			return fmt.Errorf("scaffold: duplicate variable %q", v.Name)
		}
		seenNames[v.Name] = true
		switch v.Type {
		case "string", "int", "bool", "enum", "list":
		default:
			return fmt.Errorf("scaffold: variable %q has unsupported type %q", v.Name, v.Type)
		}
	}
	seenIDs := map[string]bool{}
	for i, f := range m.Files {
		if f.Source == "" {
			return fmt.Errorf("scaffold: file #%d has no source", i)
		}
		if f.Target == "" {
			return fmt.Errorf("scaffold: file #%d (%s) has no target", i, f.Source)
		}
		if f.ID != "" {
			if seenIDs[f.ID] {
				return fmt.Errorf("scaffold: duplicate file id %q", f.ID)
			}
			seenIDs[f.ID] = true
		}
		if f.Conflict != "" && f.Conflict != "skip" && f.Conflict != "overwrite" && f.Conflict != "merge" {
			return fmt.Errorf("scaffold: file %q has unsupported conflict strategy %q", f.Source, f.Conflict)
		}
	}
	return nil
}
