package vm

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
)

type capturePatcher struct {
	env map[string]any
}

func isNumeric(val any) bool {
	switch val.(type) {
	case int, int64, float64:
		return true
	}
	return false
}

func numericCaptureNode(val any) (ast.Node, bool) {
	s, ok := val.(string)
	if !ok {
		return nil, false
	}
	if s == "" {
		return &ast.IntegerNode{Value: 0}, true
	}
	if i, err := strconv.Atoi(s); err == nil {
		return &ast.IntegerNode{Value: i}, true
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return &ast.FloatNode{Value: f}, true
	}
	return nil, false
}

func (p *capturePatcher) numericCaptureReplacement(id string, op string, other ast.Node) (ast.Node, bool) {
	if !strings.HasPrefix(id, "__cap_") || !p.isNumericContext(op, other) {
		return nil, false
	}
	val, exists := p.env[id]
	if !exists {
		return nil, false
	}
	return numericCaptureNode(val)
}

func (p *capturePatcher) isNumericContext(op string, other ast.Node) bool {
	switch op {
	case ">", "<", ">=", "<=", "+", "-", "*", "/", "%":
		return true
	case "==", "!=":
		if _, ok := other.(*ast.IntegerNode); ok {
			return true
		}
		if _, ok := other.(*ast.FloatNode); ok {
			return true
		}
		if id, ok := other.(*ast.IdentifierNode); ok {
			val, exists := p.env[id.Value]
			return exists && isNumeric(val)
		}
	}
	return false
}

func (p *capturePatcher) Visit(node *ast.Node) {
	if bin, ok := (*node).(*ast.BinaryNode); ok {
		if id, isId := bin.Left.(*ast.IdentifierNode); isId {
			if replacement, ok := p.numericCaptureReplacement(id.Value, bin.Operator, bin.Right); ok {
				bin.Left = replacement
			}
		}
		if id, isId := bin.Right.(*ast.IdentifierNode); isId {
			if replacement, ok := p.numericCaptureReplacement(id.Value, bin.Operator, bin.Left); ok {
				bin.Right = replacement
			}
		}
	}
	if un, ok := (*node).(*ast.UnaryNode); ok {
		if id, isId := un.Node.(*ast.IdentifierNode); isId {
			if un.Operator == "-" || un.Operator == "+" {
				if replacement, ok := p.numericCaptureReplacement(id.Value, un.Operator, nil); ok {
					un.Node = replacement
				}
			}
		}
	}
}

// EvalExpression evaluates a RubyMUD expression using expr-lang/expr.
func EvalExpression(expression string, variables map[string]string, captures []string) (any, error) {
	processedExpr, varMap, capMap, err := preprocessAndValidate(expression)
	if err != nil {
		return nil, err
	}

	env := make(map[string]any)
	for originalName, safeID := range varMap {
		val, ok := variables[originalName]
		if !ok {
			val, ok = builtinVar(originalName)
		}

		if ok {
			if iVal, err := strconv.ParseInt(val, 10, 64); err == nil {
				env[safeID] = iVal
			} else if f, err := strconv.ParseFloat(val, 64); err == nil {
				env[safeID] = f
			} else {
				env[safeID] = val
			}
		} else {
			env[safeID] = ""
		}
	}

	for capIdx, safeID := range capMap {
		if capIdx < len(captures) {
			env[safeID] = captures[capIdx]
		} else {
			env[safeID] = ""
		}
	}

	program, err := expr.Compile(processedExpr, expr.Env(env), expr.Patch(&capturePatcher{env: env}))
	if err != nil {
		return nil, fmt.Errorf("compile error: %w", err)
	}

	output, err := expr.Run(program, env)
	if err != nil {
		return nil, fmt.Errorf("eval error: %w", err)
	}

	return output, nil
}

func preprocessAndValidate(input string) (string, map[string]string, map[int]string, error) {
	var result strings.Builder
	varMap := make(map[string]string)
	capMap := make(map[int]string)
	nextVarIdx := 0
	inQuote := false
	var quoteChar rune
	escaped := false

	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		r := runes[i]

		if inQuote {
			result.WriteRune(r)
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quoteChar {
				inQuote = false
			}
			continue
		}

		if r == '\'' || r == '"' {
			inQuote = true
			quoteChar = r
			result.WriteRune(r)
			continue
		}

		if r == '$' {
			j := i + 1
			for j < len(runes) && (unicode.IsLetter(runes[j]) || unicode.IsDigit(runes[j]) || runes[j] == '_') {
				j++
			}
			if j > i+1 {
				varName := string(runes[i+1 : j])
				safeID, ok := varMap[varName]
				if !ok {
					safeID = fmt.Sprintf("__var_%d", nextVarIdx)
					varMap[varName] = safeID
					nextVarIdx++
				}
				result.WriteString(safeID)
				i = j - 1
				continue
			}
		}

		if r == '%' {
			isModulo := false
			back := i - 1
			for back >= 0 && runes[back] == ' ' {
				back--
			}
			if back >= 0 {
				prev := runes[back]
				if unicode.IsLetter(prev) || unicode.IsDigit(prev) || prev == '_' || prev == '$' || prev == ']' || prev == ')' {
					isModulo = true
				}
			}
			if !isModulo {
				j := i + 1
				for j < len(runes) && unicode.IsDigit(runes[j]) {
					j++
				}
				if j > i+1 {
					idx, _ := strconv.Atoi(string(runes[i+1 : j]))
					safeID, ok := capMap[idx]
					if !ok {
						safeID = fmt.Sprintf("__cap_%d", nextVarIdx)
						capMap[idx] = safeID
						nextVarIdx++
					}
					result.WriteString(safeID)
					i = j - 1
					continue
				}
			}
		}

		result.WriteRune(r)
	}

	processed := result.String()

	// Strict validation of identifiers and function calls
	if err := validateIdentifiers(processed); err != nil {
		return "", nil, nil, err
	}

	return processed, varMap, capMap, nil
}

func validateIdentifiers(input string) error {
	runes := []rune(input)
	inQuote := false
	var quoteChar rune
	escaped := false

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if inQuote {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quoteChar {
				inQuote = false
			}
			continue
		}
		if r == '\'' || r == '"' {
			inQuote = true
			quoteChar = r
			continue
		}

		if r == '?' || r == ':' {
			return fmt.Errorf("ternary operators are not supported")
		}

		if r == '(' {
			// Check preceding characters for non-whitespace
			j := i - 1
			for j >= 0 && unicode.IsSpace(runes[j]) {
				j--
			}
			if j >= 0 && (unicode.IsLetter(runes[j]) || unicode.IsDigit(runes[j]) || runes[j] == '_') {
				return fmt.Errorf("function calls are not supported")
			}
		}

		if r == '[' || r == ']' {
			return fmt.Errorf("arrays and indexing are not supported")
		}

		// If we find the start of an identifier, validate it
		if unicode.IsLetter(r) || r == '_' {
			j := i + 1
			for j < len(runes) && (unicode.IsLetter(runes[j]) || unicode.IsDigit(runes[j]) || runes[j] == '_') {
				j++
			}
			id := string(runes[i:j])
			if !isAllowedIdentifier(id) {
				return fmt.Errorf("unsupported word operator or identifier %q", id)
			}
			i = j - 1
		} else if r == '.' {
			// Ensure '.' is part of a number (decimal literal), not member access
			// It must be preceded or followed by a digit
			hasPrecedingDigit := i > 0 && unicode.IsDigit(runes[i-1])
			hasFollowingDigit := i+1 < len(runes) && unicode.IsDigit(runes[i+1])
			if !hasPrecedingDigit && !hasFollowingDigit {
				return fmt.Errorf("operator or character %q is not supported in this version", r)
			}
		}
	}
	return nil
}

func isAllowedIdentifier(id string) bool {
	if strings.HasPrefix(id, "__var_") || strings.HasPrefix(id, "__cap_") {
		return true
	}
	allowed := map[string]bool{
		"true": true, "false": true, "nil": true, "null": true,
		"and": true, "or": true, "not": true,
	}
	return allowed[id]
}
