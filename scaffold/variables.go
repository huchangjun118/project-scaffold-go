package scaffold

import (
	"fmt"
	"regexp"
	"strconv"
)

// validateVariables type-checks, coerces and validates the supplied variable
// values against the manifest. It returns every problem at once so callers
// can fix all variables in a single pass.
func validateVariables(specs []VariableSpec, values map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(specs))
	var problems []VariableProblem

	supplied := map[string]any{}
	for k, v := range values {
		supplied[k] = v
	}

	for _, spec := range specs {
		raw, ok := supplied[spec.Name]
		if !ok {
			if spec.Required {
				problems = append(problems, VariableProblem{spec.Name, "is required but was not provided"})
				continue
			}
			if spec.Default != nil {
				raw = spec.Default
			} else {
				raw = zeroFor(spec.Type)
			}
		}
		delete(supplied, spec.Name)

		val, probs := coerceAndValidate(spec, raw)
		problems = append(problems, probs...)
		if len(probs) == 0 || val != nil {
			out[spec.Name] = val
		}
	}

	for name := range supplied {
		problems = append(problems, VariableProblem{name, "is not declared by the template"})
	}

	if len(problems) > 0 {
		return nil, &ValidationError{problems}
	}
	return out, nil
}

// zeroFor returns the zero value used when an optional variable has no
// default.
func zeroFor(typ string) any {
	switch typ {
	case "int":
		return int64(0)
	case "bool":
		return false
	case "list":
		return []any{}
	default: // string, enum
		return ""
	}
}

// coerceAndValidate converts raw into the spec's type and runs every
// validation rule, collecting all problems for the variable.
func coerceAndValidate(spec VariableSpec, raw any) (any, []VariableProblem) {
	var probs []VariableProblem
	add := func(reason string) { probs = append(probs, VariableProblem{spec.Name, reason}) }

	switch spec.Type {
	case "string":
		s, ok := toString(raw)
		if !ok {
			add(fmt.Sprintf("expected a string, got %T", raw))
			return "", probs
		}
		if spec.Pattern != "" {
			re, err := regexp.Compile(spec.Pattern)
			if err != nil {
				add(fmt.Sprintf("template has invalid pattern %q: %v", spec.Pattern, err))
				return s, probs
			}
			if !re.MatchString(s) {
				add(fmt.Sprintf("value %q does not match pattern %q", s, spec.Pattern))
			}
		}
		return s, probs

	case "enum":
		s, ok := toString(raw)
		if !ok {
			add(fmt.Sprintf("expected an enum value (string), got %T", raw))
			return "", probs
		}
		allowed := false
		for _, v := range spec.Values {
			if v == s {
				allowed = true
				break
			}
		}
		if !allowed {
			add(fmt.Sprintf("value %q is not one of the allowed values %v", s, spec.Values))
		}
		return s, probs

	case "int":
		n, ok := toInt64(raw)
		if !ok {
			add(fmt.Sprintf("expected an integer, got %T", raw))
			return int64(0), probs
		}
		if spec.Min != nil && n < *spec.Min {
			add(fmt.Sprintf("value %d is less than min %d", n, *spec.Min))
		}
		if spec.Max != nil && n > *spec.Max {
			add(fmt.Sprintf("value %d is greater than max %d", n, *spec.Max))
		}
		return n, probs

	case "bool":
		b, ok := toBool(raw)
		if !ok {
			add(fmt.Sprintf("expected a boolean, got %T", raw))
			return false, probs
		}
		return b, probs

	case "list":
		items, ok := toSlice(raw)
		if !ok {
			add(fmt.Sprintf("expected a list, got %T", raw))
			return []any{}, probs
		}
		itemType := spec.ItemType
		if itemType == "" {
			itemType = "string"
		}
		out := make([]any, 0, len(items))
		for i, item := range items {
			switch itemType {
			case "int":
				n, ok := toInt64(item)
				if !ok {
					add(fmt.Sprintf("item #%d: expected an integer, got %T", i, item))
					continue
				}
				out = append(out, n)
			case "string":
				s, ok := toString(item)
				if !ok {
					add(fmt.Sprintf("item #%d: expected a string, got %T", i, item))
					continue
				}
				out = append(out, s)
			default:
				add(fmt.Sprintf("item #%d: unsupported itemType %q", i, itemType))
			}
		}
		return out, probs
	}

	return raw, nil
}

// toString coerces a JSON-ish value to string.
func toString(raw any) (string, bool) {
	switch v := raw.(type) {
	case string:
		return v, true
	case fmt.Stringer:
		return v.String(), true
	case nil:
		return "", false
	default:
		return "", false
	}
}

// toInt64 coerces integral values (including JSON float64 numbers) to int64.
func toInt64(raw any) (int64, bool) {
	switch v := raw.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		if v == float64(int64(v)) {
			return int64(v), true
		}
		return 0, false
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			return n, true
		}
		return 0, false
	default:
		return 0, false
	}
}

// toBool coerces a bool value.
func toBool(raw any) (bool, bool) {
	b, ok := raw.(bool)
	return b, ok
}

// toSlice coerces list values.
func toSlice(raw any) ([]any, bool) {
	switch v := raw.(type) {
	case []any:
		return v, true
	case []string:
		out := make([]any, len(v))
		for i, s := range v {
			out[i] = s
		}
		return out, true
	case []int:
		out := make([]any, len(v))
		for i, n := range v {
			out[i] = n
		}
		return out, true
	default:
		return nil, false
	}
}
