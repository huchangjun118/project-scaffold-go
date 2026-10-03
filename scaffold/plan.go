package scaffold

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/template"
)

// Plan describes the full set of changes a generation would make, without
// touching the workspace.
type Plan struct {
	// Workspace is the absolute target directory.
	Workspace string
	// Entries is the ordered list of planned file operations.
	Entries []PlanEntry
	// Variables is the validated, coerced variable set used for rendering.
	Variables map[string]any
	// TemplateWarnings carries non-fatal template compatibility notices.
	TemplateWarnings []string
}

// PlanEntry is one planned file operation.
type PlanEntry struct {
	// Op is one of: create, overwrite, skip, merge.
	Op string
	// Target is the rendered output path relative to the workspace.
	Target string
	// Source is the template source path (informational).
	Source string
	// Bytes is the generated (or merged) content to write. Nil for skip.
	Bytes []byte
	// Reason explains why the op was chosen (e.g. conflict strategy).
	Reason string
}

// TotalBytes returns the number of bytes the plan will write.
func (p *Plan) TotalBytes() int64 {
	var n int64
	for _, e := range p.Entries {
		if e.Op != "skip" {
			n += int64(len(e.Bytes))
		}
	}
	return n
}

// Counts returns the number of entries per operation.
func (p *Plan) Counts() map[string]int {
	c := map[string]int{}
	for _, e := range p.Entries {
		c[e.Op]++
	}
	return c
}

// planGeneration validates variables, renders file names/content, resolves
// dependencies and computes conflict operations. It performs reads only.
func planGeneration(workspace string, tmpl *Template, values map[string]any, strategy string) (*Plan, error) {
	if strategy != "skip" && strategy != "overwrite" && strategy != "merge" {
		return nil, fmt.Errorf("scaffold: unknown conflict strategy %q (want skip, overwrite or merge)", strategy)
	}

	vars, err := validateVariables(tmpl.Manifest.Variables, values)
	if err != nil {
		return nil, err
	}

	ordered, err := resolveDependencies(tmpl.Manifest.Files, func(source string) ([]byte, error) {
		return os.ReadFile(filepath.Join(tmpl.Dir, filepath.FromSlash(source)))
	})
	if err != nil {
		return nil, err
	}

	r := &renderer{
		tmpl:    tmpl,
		vars:    vars,
		targets: map[string]string{},
	}

	plan := &Plan{Workspace: workspace, Variables: vars, TemplateWarnings: tmpl.Warnings}

	for _, spec := range ordered {
		items, err := r.expandEach(spec, vars)
		if err != nil {
			return nil, err
		}
		for _, ctx := range items {
			target, err := r.renderTarget(spec, ctx)
			if err != nil {
				return nil, err
			}
			content, err := r.renderContent(spec, ctx)
			if err != nil {
				return nil, err
			}
			entry, err := r.planEntry(workspace, spec, target, content, strategy)
			if err != nil {
				return nil, err
			}
			plan.Entries = append(plan.Entries, entry)
		}
	}
	return plan, nil
}

// renderer holds render state for one planning pass.
type renderer struct {
	tmpl    *Template
	vars    map[string]any
	targets map[string]string // file id -> rendered target path
}

// expandEach returns the render contexts for a file spec: one context when
// Each is unset, or one per list element when Each names a list variable.
func (r *renderer) expandEach(spec FileSpec, vars map[string]any) ([]map[string]any, error) {
	if spec.Each == "" {
		return []map[string]any{vars}, nil
	}
	raw, ok := vars[spec.Each]
	if !ok {
		return nil, fmt.Errorf("scaffold: file %q each references undeclared variable %q", spec.Source, spec.Each)
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("scaffold: file %q each variable %q is not a list", spec.Source, spec.Each)
	}
	out := make([]map[string]any, 0, len(list))
	for i, item := range list {
		ctx := make(map[string]any, len(vars)+2)
		for k, v := range vars {
			ctx[k] = v
		}
		ctx["item"] = item
		ctx["index"] = i
		out = append(out, ctx)
	}
	return out, nil
}

// funcs returns the template function map. ref resolves a file id to its
// rendered target path (dependencies are rendered first).
func (r *renderer) funcs() template.FuncMap {
	return template.FuncMap{
		"ref": func(id string) (string, error) {
			t, ok := r.targets[id]
			if !ok {
				return "", fmt.Errorf("scaffold: template references file id %q which is not a dependency", id)
			}
			return t, nil
		},
	}
}

// renderTemplate parses and executes a template string.
func (r *renderer) renderTemplate(name, text string, ctx map[string]any) (string, error) {
	t, err := template.New(name).Funcs(r.funcs()).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", fmt.Errorf("scaffold: cannot parse template %s: %w", name, err)
	}
	var b strings.Builder
	if err := t.Execute(&b, ctx); err != nil {
		return "", fmt.Errorf("scaffold: cannot render template %s: %w", name, err)
	}
	return b.String(), nil
}

// renderTarget renders and validates the output path.
func (r *renderer) renderTarget(spec FileSpec, ctx map[string]any) (string, error) {
	rendered, err := r.renderTemplate("target:"+spec.Source, spec.Target, ctx)
	if err != nil {
		return "", err
	}
	target := strings.TrimSpace(rendered)
	if target == "" {
		return "", fmt.Errorf("scaffold: file %q rendered to an empty target path", spec.Source)
	}
	target = filepath.ToSlash(target)
	clean := path.Clean(target)
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("scaffold: file %q rendered to target %q which escapes the workspace", spec.Source, target)
	}
	if spec.ID != "" {
		r.targets[spec.ID] = clean
	}
	return clean, nil
}

// renderContent renders the source file content, honoring Condition.
func (r *renderer) renderContent(spec FileSpec, ctx map[string]any) ([]byte, error) {
	if spec.Condition != "" {
		out, err := r.renderTemplate("condition:"+spec.Source, spec.Condition, ctx)
		if err != nil {
			return nil, err
		}
		if !truthy(out) {
			return nil, nil
		}
	}
	src := filepath.Join(r.tmpl.Dir, filepath.FromSlash(spec.Source))
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, fmt.Errorf("scaffold: cannot read source %q: %w", spec.Source, err)
	}
	rendered, err := r.renderTemplate("content:"+spec.Source, string(data), ctx)
	if err != nil {
		return nil, err
	}
	return []byte(rendered), nil
}

// planEntry decides the operation for one rendered file against the
// workspace. It performs reads only.
func (r *renderer) planEntry(workspace string, spec FileSpec, target string, content []byte, strategy string) (PlanEntry, error) {
	entry := PlanEntry{Target: target, Source: spec.Source}
	if content == nil {
		entry.Op = "skip"
		entry.Reason = "condition evaluated false"
		return entry, nil
	}
	entry.Bytes = content

	existing, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(target)))
	if err != nil {
		if os.IsNotExist(err) {
			entry.Op = "create"
			entry.Reason = "file does not exist"
			return entry, nil
		}
		return PlanEntry{}, fmt.Errorf("scaffold: cannot read existing file %s: %w", target, err)
	}

	use := strategy
	if spec.Conflict != "" {
		use = spec.Conflict
	}

	switch use {
	case "skip":
		entry.Op = "skip"
		entry.Bytes = nil
		entry.Reason = "file exists and strategy is skip"
	case "overwrite":
		if bytes.Equal(existing, content) {
			entry.Op = "skip"
			entry.Bytes = nil
			entry.Reason = "file exists with identical content"
		} else {
			entry.Op = "overwrite"
			entry.Reason = "file exists and strategy is overwrite"
		}
	case "merge":
		merged, err := mergeContent(existing, content)
		if err != nil {
			return PlanEntry{}, fmt.Errorf("scaffold: merge for %s failed: %w", target, err)
		}
		if bytes.Equal(merged, existing) {
			entry.Op = "skip"
			entry.Bytes = nil
			entry.Reason = "file already satisfies template (merge no-op)"
		} else {
			entry.Op = "merge"
			entry.Bytes = merged
			entry.Reason = "file exists and strategy is merge"
		}
	default:
		return PlanEntry{}, fmt.Errorf("scaffold: unknown conflict strategy %q", use)
	}
	return entry, nil
}

// truthy reports whether a rendered condition is true.
func truthy(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	switch strings.ToLower(s) {
	case "false", "0", "no", "off":
		return false
	}
	return true
}
