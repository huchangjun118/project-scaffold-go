package scaffold

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
)

// ResolveVariables 合并默认值与用户输入，校验所有变量，
// 一次性返回全部缺失或非法的变量错误。
func ResolveVariables(t *Template, input map[string]any) (map[string]any, error) {
	resolved := make(map[string]any, len(t.Variables))
	var errs []*VarError

	// 未在模板中声明的输入变量也要报出，避免拼写错误被静默吞掉。
	for name := range input {
		if _, ok := t.Variables[name]; !ok {
			errs = append(errs, &VarError{Name: name, Message: "is not declared by the template"})
		}
	}

	// 按变量名排序保证错误顺序稳定。
	names := make([]string, 0, len(t.Variables))
	for name := range t.Variables {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		decl := t.Variables[name]
		val, provided := input[name]
		if !provided {
			if decl.Default != nil {
				val = decl.Default
			} else if decl.Required {
				errs = append(errs, &VarError{Name: name, Message: "is required but no value was provided"})
				continue
			} else {
				resolved[name] = zeroValue(decl.Type)
				continue
			}
		}
		if err := validateValue(name, decl, val); err != nil {
			errs = append(errs, err)
			continue
		}
		resolved[name] = val
	}

	if len(errs) > 0 {
		return nil, &ValidationError{Errors: errs}
	}
	return resolved, nil
}

func zeroValue(t VarType) any {
	switch t {
	case VarInt:
		return int64(0)
	case VarBool:
		return false
	case VarList:
		return []any{}
	default:
		return ""
	}
}

func validateValue(name string, decl Variable, val any) *VarError {
	bad := func(format string, args ...any) *VarError {
		return &VarError{Name: name, Message: fmt.Sprintf(format, args...)}
	}
	switch decl.Type {
	case VarString:
		s, ok := val.(string)
		if !ok {
			return bad("expected string, got %s", typeName(val))
		}
		if decl.Pattern != "" {
			re, err := regexp.Compile("^(?:" + decl.Pattern + ")$")
			if err != nil {
				return bad("template declares invalid pattern %q: %v", decl.Pattern, err)
			}
			if !re.MatchString(s) {
				return bad("value %q does not match pattern %q", s, decl.Pattern)
			}
		}
	case VarInt:
		n, ok := toInt64(val)
		if !ok {
			return bad("expected int, got %s", typeName(val))
		}
		if decl.Min != nil && n < *decl.Min {
			return bad("value %d is below minimum %d", n, *decl.Min)
		}
		if decl.Max != nil && n > *decl.Max {
			return bad("value %d exceeds maximum %d", n, *decl.Max)
		}
	case VarBool:
		if _, ok := val.(bool); !ok {
			return bad("expected bool, got %s", typeName(val))
		}
	case VarList:
		if _, ok := val.([]any); !ok {
			return bad("expected list, got %s", typeName(val))
		}
	default:
		return bad("template declares unknown type %q", string(decl.Type))
	}
	if len(decl.Enum) > 0 {
		found := false
		for _, e := range decl.Enum {
			if reflect.DeepEqual(e, val) {
				found = true
				break
			}
		}
		if !found {
			return bad("value %v is not one of the allowed enum values %v", val, decl.Enum)
		}
	}
	return nil
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		if n == float64(int64(n)) {
			return int64(n), true
		}
	}
	return 0, false
}

func typeName(v any) string {
	if v == nil {
		return "null"
	}
	return fmt.Sprintf("%T", v)
}
