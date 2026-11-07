package engine

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/vokramd/markov/pkg/parser"
)

type tokenKind int

const (
	tokIdent  tokenKind = iota // bare identifier
	tokNumber                  // numeric literal
	tokString                  // single or double quoted string
	tokOp                      // == != <= >= < >
	tokAnd                     // and
	tokOr                      // or
	tokNot                     // not
	tokNone                    // None
	tokTrue                    // true / True
	tokFalse                   // false / False
	tokLParen                  // (
	tokRParen                  // )
)

type token struct {
	kind tokenKind
	val  string
}

func tokenize(expr string) []token {
	var tokens []token
	i := 0
	for i < len(expr) {
		ch := expr[i]

		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			i++
			continue
		}

		if ch == '\'' {
			j := i + 1
			for j < len(expr) && expr[j] != '\'' {
				j++
			}
			tokens = append(tokens, token{tokString, expr[i+1 : j]})
			i = j + 1
			continue
		}

		if ch == '"' {
			j := i + 1
			for j < len(expr) && expr[j] != '"' {
				j++
			}
			tokens = append(tokens, token{tokString, expr[i+1 : j]})
			i = j + 1
			continue
		}

		if ch == '=' && i+1 < len(expr) && expr[i+1] == '=' {
			tokens = append(tokens, token{tokOp, "=="})
			i += 2
			continue
		}
		if ch == '!' && i+1 < len(expr) && expr[i+1] == '=' {
			tokens = append(tokens, token{tokOp, "!="})
			i += 2
			continue
		}
		if ch == '<' && i+1 < len(expr) && expr[i+1] == '=' {
			tokens = append(tokens, token{tokOp, "<="})
			i += 2
			continue
		}
		if ch == '>' && i+1 < len(expr) && expr[i+1] == '=' {
			tokens = append(tokens, token{tokOp, ">="})
			i += 2
			continue
		}
		if ch == '<' {
			tokens = append(tokens, token{tokOp, "<"})
			i++
			continue
		}
		if ch == '>' {
			tokens = append(tokens, token{tokOp, ">"})
			i++
			continue
		}

		if ch == '(' {
			tokens = append(tokens, token{tokLParen, "("})
			i++
			continue
		}
		if ch == ')' {
			tokens = append(tokens, token{tokRParen, ")"})
			i++
			continue
		}

		if unicode.IsDigit(rune(ch)) || (ch == '-' && i+1 < len(expr) && unicode.IsDigit(rune(expr[i+1]))) {
			j := i
			if ch == '-' {
				j++
			}
			for j < len(expr) && (unicode.IsDigit(rune(expr[j])) || expr[j] == '.') {
				j++
			}
			tokens = append(tokens, token{tokNumber, expr[i:j]})
			i = j
			continue
		}

		if unicode.IsLetter(rune(ch)) || ch == '_' {
			j := i
			for j < len(expr) && (unicode.IsLetter(rune(expr[j])) || unicode.IsDigit(rune(expr[j])) || expr[j] == '_') {
				j++
			}
			word := expr[i:j]
			switch word {
			case "and":
				tokens = append(tokens, token{tokAnd, "and"})
			case "or":
				tokens = append(tokens, token{tokOr, "or"})
			case "not":
				tokens = append(tokens, token{tokNot, "not"})
			case "None":
				tokens = append(tokens, token{tokNone, "None"})
			case "true", "True":
				tokens = append(tokens, token{tokTrue, "true"})
			case "false", "False":
				tokens = append(tokens, token{tokFalse, "false"})
			default:
				tokens = append(tokens, token{tokIdent, word})
			}
			i = j
			continue
		}

		i++
	}
	return tokens
}

func translateCondition(expr string) (string, error) {
	tokens := tokenize(expr)
	return translateTokens(tokens)
}

func translateTokens(tokens []token) (string, error) {
	var parts []string
	i := 0

	for i < len(tokens) {
		switch tokens[i].kind {
		case tokAnd:
			parts = append(parts, "&&")
			i++

		case tokOr:
			parts = append(parts, "||")
			i++

		case tokLParen:
			parts = append(parts, "(")
			i++

		case tokRParen:
			parts = append(parts, ")")
			i++

		case tokNot:
			if i+1 < len(tokens) && tokens[i+1].kind == tokIdent && !isFollowedByOp(tokens, i+1) {
				parts = append(parts, fmt.Sprintf(`!Facts.IsTrue("%s")`, tokens[i+1].val))
				i += 2
			} else {
				parts = append(parts, "!")
				i++
			}

		case tokIdent:
			consumed, s, err := translateIdentExpr(tokens, i)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
			i += consumed

		default:
			return "", fmt.Errorf("unexpected token at position %d: kind=%d val=%q", i, tokens[i].kind, tokens[i].val)
		}
	}

	return strings.Join(parts, " "), nil
}

func isFollowedByOp(tokens []token, pos int) bool {
	return pos+1 < len(tokens) && tokens[pos+1].kind == tokOp
}

func translateIdentExpr(tokens []token, pos int) (int, string, error) {
	ident := tokens[pos].val

	if pos+1 >= len(tokens) || tokens[pos+1].kind != tokOp {
		return 1, fmt.Sprintf(`Facts.IsTrue("%s")`, ident), nil
	}

	if pos+2 >= len(tokens) {
		return 0, "", fmt.Errorf("unexpected end after operator at position %d", pos+1)
	}

	op := tokens[pos+1].val
	rhs := tokens[pos+2]

	switch rhs.kind {
	case tokNone:
		if op == "==" {
			return 3, fmt.Sprintf(`Facts.IsNil("%s")`, ident), nil
		}
		return 3, fmt.Sprintf(`!Facts.IsNil("%s")`, ident), nil

	case tokTrue:
		if op == "==" {
			return 3, fmt.Sprintf(`Facts.IsTrue("%s")`, ident), nil
		}
		return 3, fmt.Sprintf(`!Facts.IsTrue("%s")`, ident), nil

	case tokFalse:
		if op == "==" {
			return 3, fmt.Sprintf(`!Facts.IsTrue("%s")`, ident), nil
		}
		return 3, fmt.Sprintf(`Facts.IsTrue("%s")`, ident), nil

	case tokString:
		return 3, fmt.Sprintf(`Facts.GetStr("%s") %s "%s"`, ident, op, rhs.val), nil

	case tokNumber:
		return 3, fmt.Sprintf(`Facts.GetNum("%s") %s %s`, ident, op, rhs.val), nil

	case tokIdent:
		if op == "<" || op == ">" || op == "<=" || op == ">=" {
			return 3, fmt.Sprintf(`Facts.GetNum("%s") %s Facts.GetNum("%s")`, ident, op, rhs.val), nil
		}
		return 3, fmt.Sprintf(`Facts.GetStr("%s") %s Facts.GetStr("%s")`, ident, op, rhs.val), nil

	default:
		return 0, "", fmt.Errorf("unexpected token after operator: kind=%d val=%q", rhs.kind, rhs.val)
	}
}

func compileRuleToGRL(rule parser.Rule) (string, error) {
	condition, err := translateCondition(rule.When)
	if err != nil {
		return "", fmt.Errorf("translating condition for rule %q: %w", rule.Name, err)
	}

	var thenParts []string
	for k, v := range rule.SetFact {
		thenParts = append(thenParts, compileSetFactToGRL(k, v))
	}

	thenParts = append(thenParts, fmt.Sprintf(`Facts.MarkFired("%s");`, rule.Name))
	thenParts = append(thenParts, `Changed("Facts");`)
	thenParts = append(thenParts, fmt.Sprintf(`Retract("%s");`, rule.Name))

	desc := rule.Description
	if desc == "" {
		desc = rule.Name
	}
	desc = strings.ReplaceAll(desc, `"`, `\"`)

	return fmt.Sprintf("rule %s \"%s\" salience %d {\n    when\n        %s\n    then\n        %s\n}",
		rule.Name, desc, rule.Salience, condition, strings.Join(thenParts, "\n        ")), nil
}

func compileSetFactToGRL(key string, val any) string {
	switch v := val.(type) {
	case bool:
		return fmt.Sprintf(`Facts.SetBool("%s", %t);`, key, v)
	case string:
		escaped := strings.ReplaceAll(v, `"`, `\"`)
		return fmt.Sprintf(`Facts.SetStr("%s", "%s");`, key, escaped)
	case int:
		return fmt.Sprintf(`Facts.SetNum("%s", %.1f);`, key, float64(v))
	case float64:
		return fmt.Sprintf(`Facts.SetNum("%s", %v);`, key, v)
	default:
		escaped := strings.ReplaceAll(fmt.Sprintf("%v", val), `"`, `\"`)
		return fmt.Sprintf(`Facts.SetStr("%s", "%s");`, key, escaped)
	}
}
