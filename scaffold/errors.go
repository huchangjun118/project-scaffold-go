package scaffold

import (
	"fmt"
	"strings"
)

// VarError 描述单个变量的校验错误。
type VarError struct {
	Name    string
	Message string
}

func (e *VarError) Error() string {
	return fmt.Sprintf("variable %q: %s", e.Name, e.Message)
}

// ValidationError 聚合渲染前发现的所有变量错误。
type ValidationError struct {
	Errors []*VarError
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Errors))
	for _, ve := range e.Errors {
		parts = append(parts, ve.Error())
	}
	return fmt.Sprintf("template validation failed with %d error(s): %s",
		len(e.Errors), strings.Join(parts, "; "))
}

// CycleError 描述文件依赖图中的循环依赖。
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("cyclic dependency detected among template files: %s",
		strings.Join(e.Path, " -> "))
}

// SpecVersionError 表示模板声明的规范版本不被引擎支持。
type SpecVersionError struct {
	Template  string
	Supported string
}

func (e *SpecVersionError) Error() string {
	return fmt.Sprintf("template spec version %q is not supported by this engine (supported: %s)",
		e.Template, e.Supported)
}

// DeprecatedFieldError 表示模板使用了已废弃字段。
type DeprecatedFieldError struct {
	Field       string
	Replacement string
}

func (e *DeprecatedFieldError) Error() string {
	return fmt.Sprintf("template field %q is deprecated; use %q instead", e.Field, e.Replacement)
}

// WorkspaceError 描述工作区权限或资源异常。
type WorkspaceError struct {
	Op   string
	Path string
	Err  error
}

func (e *WorkspaceError) Error() string {
	return fmt.Sprintf("workspace %s failed for %q: %v", e.Op, e.Path, e.Err)
}

func (e *WorkspaceError) Unwrap() error { return e.Err }
