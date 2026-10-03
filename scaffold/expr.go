package scaffold

import (
	"fmt"
	"strings"
)

// evalWhen 计算 when 条件表达式，变量来自 vars。
// 支持: 标识符(含点路径)、!x、x == "lit"、x != "lit"、&&、||、括号。
func evalWhen(expr string, vars map[string]any) (bool, error) {
	toks, err := lexExpr(expr)
	if err != nil {
		return false, err
	}
	p := &exprParser{toks: toks, vars: vars}
	v, err := p.parseOr()
	if err != nil {
		return false, err
	}
	if p.pos != len(p.toks) {
		return false, fmt.Errorf("unexpected token %q in when expression %q", p.toks[p.pos].text, expr)
	}
	return v, nil
}

type tokenKind int

const (
	tokIdent tokenKind = iota
	tokString
	tokNot
	tokEq
	tokNeq
	tokAnd
	tokOr
	tokLParen
	tokRParen
)

type token struct {
	kind tokenKind
	text string
}

func lexExpr(s string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			i++
		case c == '(':
			toks = append(toks, token{tokLParen, "("})
			i++
		case c == ')':
			toks = append(toks, token{tokRParen, ")"})
			i++
		case c == '!':
			if i+1 < len(s) && s[i+1] == '=' {
				toks = append(toks, token{tokNeq, "!="})
				i += 2
			} else {
				toks = append(toks, token{tokNot, "!"})
				i++
			}
		case c == '=' && i+1 < len(s) && s[i+1] == '=':
			toks = append(toks, token{tokEq, "=="})
			i += 2
		case c == '&' && i+1 < len(s) && s[i+1] == '&':
			toks = append(toks, token{tokAnd, "&&"})
			i += 2
		case c == '|' && i+1 < len(s) && s[i+1] == '|':
			toks = append(toks, token{tokOr, "||"})
			i += 2
		case c == '"':
			j := i + 1
			var sb strings.Builder
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' && j+1 < len(s) {
					j++
				}
				sb.WriteByte(s[j])
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated string literal in when expression %q", s)
			}
			toks = append(toks, token{tokString, sb.String()})
			i = j + 1
		case c == '_' || c == '.' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'):
			j := i
			for j < len(s) && (s[j] == '_' || s[j] == '.' ||
				(s[j] >= 'a' && s[j] <= 'z') || (s[j] >= 'A' && s[j] <= 'Z') ||
				(s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			toks = append(toks, token{tokIdent, s[i:j]})
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q in when expression %q", string(c), s)
		}
	}
	return toks, nil
}

type exprParser struct {
	toks []token
	pos  int
	vars map[string]any
}

func (p *exprParser) peek() (token, bool) {
	if p.pos < len(p.toks) {
		return p.toks[p.pos], true
	}
	return token{}, false
}

func (p *exprParser) parseOr() (bool, error) {
	left, err := p.parseAnd()
	if err != nil {
		return false, err
	}
	for {
		if t, ok := p.peek(); ok && t.kind == tokOr {
			p.pos++
			right, err := p.parseAnd()
			if err != nil {
				return false, err
			}
			left = left || right
		} else {
			return left, nil
		}
	}
}

func (p *exprParser) parseAnd() (bool, error) {
	left, err := p.parseUnary()
	if err != nil {
		return false, err
	}
	for {
		if t, ok := p.peek(); ok && t.kind == tokAnd {
			p.pos++
			right, err := p.parseUnary()
			if err != nil {
				return false, err
			}
			left = left && right
		} else {
			return left, nil
		}
	}
}

func (p *exprParser) parseUnary() (bool, error) {
	if t, ok := p.peek(); ok && t.kind == tokNot {
		p.pos++
		v, err := p.parseUnary()
		return !v, err
	}
	return p.parseComparison()
}

func (p *exprParser) parseComparison() (bool, error) {
	t, ok := p.peek()
	if !ok {
		return false, fmt.Errorf("unexpected end of when expression")
	}
	if t.kind == tokLParen {
		p.pos++
		v, err := p.parseOr()
		if err != nil {
			return false, err
		}
		closing, ok := p.peek()
		if !ok || closing.kind != tokRParen {
			return false, fmt.Errorf("missing closing parenthesis in when expression")
		}
		p.pos++
		return v, nil
	}
	if t.kind != tokIdent {
		return false, fmt.Errorf("unexpected token %q in when expression", t.text)
	}
	p.pos++
	val := lookupPath(p.vars, t.text)

	if op, ok := p.peek(); ok && (op.kind == tokEq || op.kind == tokNeq) {
		p.pos++
		lit, ok := p.peek()
		if !ok || lit.kind != tokString {
			return false, fmt.Errorf("expected string literal after %q in when expression", op.text)
		}
		p.pos++
		eq := fmt.Sprint(val) == lit.text
		if op.kind == tokNeq {
			return !eq, nil
		}
		return eq, nil
	}
	return truthy(val), nil
}

func lookupPath(vars map[string]any, path string) any {
	var cur any = vars
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[seg]
	}
	return cur
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int64:
		return x != 0
	case []any:
		return len(x) > 0
	default:
		return true
	}
}
