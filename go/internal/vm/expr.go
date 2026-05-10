package vm

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/expr-lang/expr"
)

// EvalExpression evaluates a RubyMUD expression using expr-lang/expr.
func EvalExpression(expression string, variables map[string]string) (any, error) {
	processedExpr, varMap, err := preprocessAndValidate(expression)
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
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				env[safeID] = f
			} else {
				env[safeID] = val
			}
		} else {
			env[safeID] = ""
		}
	}

	program, err := expr.Compile(processedExpr, expr.Env(env))
	if err != nil {
		return nil, fmt.Errorf("compile error: %w", err)
	}

	output, err := expr.Run(program, env)
	if err != nil {
		return nil, fmt.Errorf("eval error: %w", err)
	}

	return output, nil
}

func preprocessAndValidate(input string) (string, map[string]string, error) {
	var result strings.Builder
	varMap := make(map[string]string)
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

		// Whitelist check for forbidden characters outside strings
		switch r {
		case '>', '<', '!', '&', '|', '%', '[', ']', '{', '}', ',', '?', ':':
			return "", nil, fmt.Errorf("operator or character %q is not supported in this version", r)
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
		result.WriteRune(r)
	}

	processed := result.String()

	// Strict validation of identifiers and function calls
	if err := validateIdentifiers(processed); err != nil {
		return "", nil, err
	}

	return processed, varMap, nil
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
	if strings.HasPrefix(id, "__var_") {
		return true
	}
	if id == "true" || id == "false" {
		return true
	}
	return false
}
