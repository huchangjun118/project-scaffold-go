package scaffold

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SupportedSpecMajor 是引擎支持的模板规范主版本号。
const SupportedSpecMajor = 1

// ManifestName 是模板清单文件名。
const ManifestName = "template.json"

// VarType 变量类型。
type VarType string

const (
	VarString VarType = "string"
	VarInt    VarType = "int"
	VarBool   VarType = "bool"
	VarList   VarType = "list"
)

// Variable 描述模板声明的单个变量。
type Variable struct {
	Type     VarType `json:"type"`
	Required bool    `json:"required,omitempty"`
	Default  any     `json:"default,omitempty"`
	Pattern  string  `json:"pattern,omitempty"`
	Enum     []any   `json:"enum,omitempty"`
	Min      *int64  `json:"min,omitempty"`
	Max      *int64  `json:"max,omitempty"`
}

// Repeat 描述文件的重复展开规则。
type Repeat struct {
	Var string `json:"var"`
	As  string `json:"as"`
}

// FileRule 描述模板中一个输出文件的生成规则。
type FileRule struct {
	Path      string   `json:"path"`
	Source    string   `json:"source,omitempty"`
	Content   string   `json:"content,omitempty"`
	When      string   `json:"when,omitempty"`
	Repeat    *Repeat  `json:"repeat,omitempty"`
	DependsOn []string `json:"dependsOn,omitempty"`
	Merge     string   `json:"merge,omitempty"`
}

// Template 是磁盘上模板目录的清单（template.json）。
type Template struct {
	SpecVersion string              `json:"specVersion"`
	Name        string              `json:"name"`
	Variables   map[string]Variable `json:"variables,omitempty"`
	Files       []FileRule          `json:"files"`

	// Dir 是模板在磁盘上的根目录。
	Dir string `json:"-"`
}

// rawManifest 用于在严格解析前探测废弃字段。
type rawManifest struct {
	SpecVersion string `json:"specVersion"`
	// 废弃字段：v0 时代的 "params"（变量表）与文件规则里的 "copy"。
	Params map[string]json.RawMessage `json:"params,omitempty"`
}

// LoadTemplate 从目录加载模板清单并做规范版本与废弃字段检查。
func LoadTemplate(dir string) (*Template, error) {
	manifestPath := filepath.Join(dir, ManifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, &WorkspaceError{Op: "read template manifest", Path: manifestPath, Err: err}
	}

	var raw rawManifest
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("template manifest %s is not valid JSON: %w", manifestPath, err)
	}

	// 废弃字段：明确拒绝并给出迁移指引，而不是静默忽略。
	if raw.Params != nil {
		return nil, &DeprecatedFieldError{Field: "params", Replacement: "variables"}
	}

	if err := checkSpecVersion(raw.SpecVersion); err != nil {
		return nil, err
	}

	// 文件规则内的废弃字段探测（v0 的 "copy" 布尔字段）。
	var probe struct {
		Files []map[string]json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal(data, &probe); err == nil {
		for i, f := range probe.Files {
			if _, ok := f["copy"]; ok {
				return nil, &DeprecatedFieldError{
					Field:       fmt.Sprintf("files[%d].copy", i),
					Replacement: "source",
				}
			}
		}
	}

	var t Template
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, fmt.Errorf("template manifest %s: %w", manifestPath, err)
	}
	t.Dir = dir

	if t.Name == "" {
		return nil, fmt.Errorf("template manifest %s: missing required field \"name\"", manifestPath)
	}
	if len(t.Files) == 0 {
		return nil, fmt.Errorf("template manifest %s: template must declare at least one file", manifestPath)
	}
	for i, f := range t.Files {
		if f.Path == "" {
			return nil, fmt.Errorf("template manifest %s: files[%d] missing \"path\"", manifestPath, i)
		}
		if f.Source != "" && f.Content != "" {
			return nil, fmt.Errorf("template manifest %s: files[%d] (%s) cannot set both \"source\" and \"content\"",
				manifestPath, i, f.Path)
		}
		switch f.Merge {
		case "", "none", "json", "lines":
		default:
			return nil, fmt.Errorf("template manifest %s: files[%d] (%s) has unknown merge strategy %q",
				manifestPath, i, f.Path, f.Merge)
		}
	}
	return &t, nil
}

// checkSpecVersion 校验模板声明的规范版本是否在引擎支持范围内。
func checkSpecVersion(v string) error {
	if v == "" {
		return fmt.Errorf("template manifest missing \"specVersion\"")
	}
	majorStr, _, _ := strings.Cut(v, ".")
	major, err := strconv.Atoi(majorStr)
	if err != nil {
		return fmt.Errorf("template specVersion %q is not a valid version string", v)
	}
	if major != SupportedSpecMajor {
		return &SpecVersionError{
			Template:  v,
			Supported: fmt.Sprintf("%d.x", SupportedSpecMajor),
		}
	}
	return nil
}
