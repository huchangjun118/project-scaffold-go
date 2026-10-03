package scaffold

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

// edge 表示依赖边：from 必须先于 to 解析。
type edge struct{ from, to string }

// renderedFile 是渲染后待落地的单个文件。
type renderedFile struct {
	path    string // 相对工作区的路径
	content []byte
	merge   string
}

// renderFiles 依据变量展开全部文件规则（路径模板、条件、重复），
// 并按 dependsOn 拓扑排序；发现循环依赖返回 *CycleError。
func renderFiles(t *Template, vars map[string]any) ([]*renderedFile, error) {
	var files []*renderedFile
	var edges []edge

	for i := range t.Files {
		rule := &t.Files[i]
		contexts, err := expandContexts(rule, vars)
		if err != nil {
			return nil, fmt.Errorf("file rule %q: %w", rule.Path, err)
		}
		for _, ctx := range contexts {
			pathBytes, err := renderString("path", rule.Path, ctx)
			if err != nil {
				return nil, fmt.Errorf("file rule %q: %w", rule.Path, err)
			}
			path, err := cleanRelPath(string(pathBytes))
			if err != nil {
				return nil, fmt.Errorf("file rule %q: %w", rule.Path, err)
			}
			content, err := renderContent(t, rule, ctx)
			if err != nil {
				return nil, fmt.Errorf("file rule %q: %w", rule.Path, err)
			}
			files = append(files, &renderedFile{path: path, content: content, merge: rule.Merge})
			for _, dep := range rule.DependsOn {
				depBytes, err := renderString("dependsOn", dep, ctx)
				if err != nil {
					return nil, fmt.Errorf("file rule %q: %w", rule.Path, err)
				}
				depPath, err := cleanRelPath(string(depBytes))
				if err != nil {
					return nil, fmt.Errorf("file rule %q: %w", rule.Path, err)
				}
				edges = append(edges, edge{from: depPath, to: path})
			}
		}
	}

	// 同一目标路径被多条规则渲染出来属于模板错误。
	seen := map[string]bool{}
	for _, f := range files {
		if seen[f.path] {
			return nil, fmt.Errorf("template renders path %q more than once", f.path)
		}
		seen[f.path] = true
	}

	return topoSort(files, edges)
}

// expandContexts 计算一条文件规则生效的变量上下文列表：
// when 为假时为空；有 repeat 时按列表变量逐元素展开。
func expandContexts(rule *FileRule, vars map[string]any) ([]map[string]any, error) {
	if rule.When != "" {
		ok, err := evalWhen(rule.When, vars)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
	}
	if rule.Repeat == nil {
		return []map[string]any{vars}, nil
	}
	rep := rule.Repeat
	if rep.Var == "" || rep.As == "" {
		return nil, fmt.Errorf("repeat requires both \"var\" and \"as\"")
	}
	list, ok := vars[rep.Var].([]any)
	if !ok {
		return nil, fmt.Errorf("repeat variable %q is not a list", rep.Var)
	}
	contexts := make([]map[string]any, 0, len(list))
	for i, item := range list {
		ctx := make(map[string]any, len(vars)+2)
		for k, v := range vars {
			ctx[k] = v
		}
		ctx[rep.As] = item
		ctx["__index"] = i
		contexts = append(contexts, ctx)
	}
	return contexts, nil
}

// renderContent 取得并渲染文件内容：内联 content 或模板目录下的 source 文件。
func renderContent(t *Template, rule *FileRule, vars map[string]any) ([]byte, error) {
	var raw []byte
	switch {
	case rule.Source != "":
		srcPath := filepath.Join(t.Dir, filepath.FromSlash(rule.Source))
		if !strings.HasPrefix(srcPath, filepath.Clean(t.Dir)+string(os.PathSeparator)) {
			return nil, fmt.Errorf("source %q escapes the template directory", rule.Source)
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return nil, &WorkspaceError{Op: "read template source", Path: srcPath, Err: err}
		}
		raw = data
	case rule.Content != "":
		raw = []byte(rule.Content)
	default:
		raw = nil
	}
	return renderString("content", string(raw), vars)
}

// renderString 用 text/template 渲染一段文本。
func renderString(kind, text string, vars map[string]any) ([]byte, error) {
	if !strings.Contains(text, "{{") {
		return []byte(text), nil
	}
	tmpl, err := template.New(kind).Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, fmt.Errorf("invalid %s template: %w", kind, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return nil, fmt.Errorf("failed to render %s template: %w", kind, err)
	}
	return buf.Bytes(), nil
}

// cleanRelPath 规整渲染出的路径并确保其不逃逸工作区。
func cleanRelPath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("rendered path is empty")
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("rendered path %q must be relative to the workspace", p)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("rendered path %q escapes the workspace", p)
	}
	return clean, nil
}

// topoSort 按依赖边对渲染结果排序，发现环时返回 *CycleError。
func topoSort(files []*renderedFile, edges []edge) ([]*renderedFile, error) {
	byPath := make(map[string]*renderedFile, len(files))
	for _, f := range files {
		byPath[f.path] = f
	}
	deps := make(map[string][]string, len(files))
	for _, e := range edges {
		if e.from == e.to {
			return nil, &CycleError{Path: []string{e.from, e.to}}
		}
		if _, ok := byPath[e.from]; !ok {
			return nil, fmt.Errorf("file %q depends on %q which is not produced by this template",
				e.to, e.from)
		}
		deps[e.to] = append(deps[e.to], e.from)
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(files))
	var order []*renderedFile
	var stack []string

	var visit func(p string) error
	visit = func(p string) error {
		switch color[p] {
		case black:
			return nil
		case gray:
			// 找到环：截取栈中从 p 开始的部分。
			idx := 0
			for i, s := range stack {
				if s == p {
					idx = i
					break
				}
			}
			cycle := append(append([]string{}, stack[idx:]...), p)
			return &CycleError{Path: cycle}
		}
		color[p] = gray
		stack = append(stack, p)
		ds := append([]string{}, deps[p]...)
		sort.Strings(ds)
		for _, d := range ds {
			if err := visit(d); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		color[p] = black
		order = append(order, byPath[p])
		return nil
	}

	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.path)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if err := visit(p); err != nil {
			return nil, err
		}
	}
	return order, nil
}
