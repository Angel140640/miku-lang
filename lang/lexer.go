// Package lang implementa a linguagem Miku (.mk): lexer, parser e interpretador.
package lang

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type Kind int

const (
	EOF Kind = iota
	NEWLINE
	INDENT
	DEDENT
	NAME
	INT
	FLOAT
	STRING
	FSTRING
	OP
)

type Token struct {
	Kind Kind
	Text string
	Line int
	Raw  bool // string "crua": r"..."
}

// Error é um erro da linguagem (sintaxe ou execução) com a linha do .mk.
type Error struct {
	Line int
	Msg  string
}

func (e *Error) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("linha %d: %s", e.Line, e.Msg)
	}
	return e.Msg
}

func errAt(line int, format string, a ...any) *Error {
	return &Error{Line: line, Msg: fmt.Sprintf(format, a...)}
}

var ops2 = []string{"**", "//", "==", "!=", "<=", ">=", "+=", "-=", "*=", "/=", "%=", "->"}

const ops1 = "+-*/%<>=()[]{},:.@"

func isIdentStart(c rune) bool { return c == '_' || unicode.IsLetter(c) }
func isIdentPart(c rune) bool  { return c == '_' || unicode.IsLetter(c) || unicode.IsDigit(c) }
func isDigit(c rune) bool      { return c >= '0' && c <= '9' }
func isHex(c rune) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// Lex quebra o código em tokens, gerando INDENT/DEDENT/NEWLINE como o Python.
func Lex(src string) ([]Token, error) {
	src = strings.TrimPrefix(src, "\ufeff")
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	r := []rune(src)
	n := len(r)

	var toks []Token
	indents := []int{0}
	depth, line, i := 0, 1, 0
	atStart := true
	emit := func(k Kind, text string, ln int) {
		toks = append(toks, Token{Kind: k, Text: text, Line: ln})
	}

	for i < n {
		if atStart && depth == 0 {
			col, j := 0, i
			for j < n && (r[j] == ' ' || r[j] == '\t') {
				if r[j] == '\t' {
					col = (col/8 + 1) * 8
				} else {
					col++
				}
				j++
			}
			if j >= n {
				i = j
				break
			}
			if r[j] == '\n' {
				i, line = j+1, line+1
				continue
			}
			if r[j] == '#' {
				for j < n && r[j] != '\n' {
					j++
				}
				i = j
				continue
			}
			if col > indents[len(indents)-1] {
				indents = append(indents, col)
				emit(INDENT, "", line)
			} else {
				for col < indents[len(indents)-1] {
					indents = indents[:len(indents)-1]
					emit(DEDENT, "", line)
				}
				if col != indents[len(indents)-1] {
					return nil, errAt(line, "indentação inconsistente")
				}
			}
			i, atStart = j, false
		}

		c := r[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '\n':
			if depth == 0 {
				if len(toks) > 0 && toks[len(toks)-1].Kind != NEWLINE {
					emit(NEWLINE, "", line)
				}
				atStart = true
			}
			i, line = i+1, line+1
		case c == '\\' && i+1 < n && r[i+1] == '\n':
			i, line = i+2, line+1
		case c == '#':
			for i < n && r[i] != '\n' {
				i++
			}
		case isDigit(c) || (c == '.' && i+1 < n && isDigit(r[i+1])):
			start := i
			isFloat := false
			prefixed := false
			if c == '0' && i+1 < n && strings.ContainsRune("xXbBoO", r[i+1]) {
				prefixed = true
				i += 2
				for i < n && (isHex(r[i]) || r[i] == '_') {
					i++
				}
			} else {
				for i < n && (isDigit(r[i]) || r[i] == '_') {
					i++
				}
				if i < n && r[i] == '.' && (i+1 >= n || !isIdentStart(r[i+1])) {
					isFloat = true
					i++
					for i < n && (isDigit(r[i]) || r[i] == '_') {
						i++
					}
				}
				if i < n && (r[i] == 'e' || r[i] == 'E') {
					k := i + 1
					if k < n && (r[k] == '+' || r[k] == '-') {
						k++
					}
					if k < n && isDigit(r[k]) {
						isFloat = true
						i = k
						for i < n && isDigit(r[i]) {
							i++
						}
					}
				}
			}
			text := strings.ReplaceAll(string(r[start:i]), "_", "")
			if isFloat {
				if _, err := strconv.ParseFloat(text, 64); err != nil {
					return nil, errAt(line, "número inválido: %s", text)
				}
				emit(FLOAT, text, line)
			} else {
				base := 10
				if prefixed {
					base = 0
				}
				if _, err := strconv.ParseInt(text, base, 64); err != nil {
					return nil, errAt(line, "número inválido ou grande demais: %s", text)
				}
				emit(INT, text, line)
			}
		case isIdentStart(c):
			j := i
			for j < n && isIdentPart(r[j]) {
				j++
			}
			name := string(r[i:j])
			if j < n && (r[j] == '"' || r[j] == '\'') && len(name) <= 2 {
				switch strings.ToLower(name) {
				case "f", "r", "rf", "fr":
					low := strings.ToLower(name)
					body, next, endLine, err := scanString(r, j, line)
					if err != nil {
						return nil, err
					}
					raw := strings.Contains(low, "r")
					if strings.Contains(low, "f") {
						toks = append(toks, Token{Kind: FSTRING, Text: body, Line: line, Raw: raw})
					} else {
						toks = append(toks, Token{Kind: STRING, Text: body, Line: line})
					}
					i, line = next, endLine
					continue
				}
			}
			emit(NAME, name, line)
			i = j
		case c == '"' || c == '\'':
			body, next, endLine, err := scanString(r, i, line)
			if err != nil {
				return nil, err
			}
			text, err := unescape(body, false)
			if err != nil {
				return nil, errAt(line, "%v", err)
			}
			emit(STRING, text, line)
			i, line = next, endLine
		default:
			matched := false
			if i+1 < n {
				two := string(r[i : i+2])
				for _, op := range ops2 {
					if two == op {
						emit(OP, op, line)
						i += 2
						matched = true
						break
					}
				}
			}
			if matched {
				continue
			}
			if c < 128 && strings.ContainsRune(ops1, c) {
				switch c {
				case '(', '[', '{':
					depth++
				case ')', ']', '}':
					if depth > 0 {
						depth--
					}
				}
				emit(OP, string(c), line)
				i++
				continue
			}
			return nil, errAt(line, "caractere inesperado: %q", c)
		}
	}

	if len(toks) > 0 && toks[len(toks)-1].Kind != NEWLINE {
		emit(NEWLINE, "", line)
	}
	for len(indents) > 1 {
		indents = indents[:len(indents)-1]
		emit(DEDENT, "", line)
	}
	emit(EOF, "", line)
	return toks, nil
}

// scanString lê um literal a partir da aspa de abertura r[i]; devolve o corpo SEM processar escapes.
func scanString(r []rune, i, line int) (body string, next, endLine int, err error) {
	q := r[i]
	start := line
	triple := i+2 < len(r) && r[i+1] == q && r[i+2] == q
	if triple {
		i += 3
	} else {
		i++
	}
	var sb strings.Builder
	for i < len(r) {
		c := r[i]
		if c == '\\' && i+1 < len(r) {
			sb.WriteRune(c)
			sb.WriteRune(r[i+1])
			if r[i+1] == '\n' {
				line++
			}
			i += 2
			continue
		}
		if triple {
			if c == q && i+2 < len(r) && r[i+1] == q && r[i+2] == q {
				return sb.String(), i + 3, line, nil
			}
			if c == '\n' {
				line++
			}
		} else {
			if c == q {
				return sb.String(), i + 1, line, nil
			}
			if c == '\n' {
				return "", 0, 0, errAt(start, "texto sem aspas de fechamento")
			}
		}
		sb.WriteRune(c)
		i++
	}
	return "", 0, 0, errAt(start, "texto sem aspas de fechamento")
}

func unescape(s string, raw bool) (string, error) {
	if raw || !strings.Contains(s, "\\") {
		return s, nil
	}
	rs := []rune(s)
	var sb strings.Builder
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if c != '\\' || i+1 >= len(rs) {
			sb.WriteRune(c)
			continue
		}
		i++
		switch rs[i] {
		case 'n':
			sb.WriteByte('\n')
		case 't':
			sb.WriteByte('\t')
		case 'r':
			sb.WriteByte('\r')
		case '0':
			sb.WriteByte(0)
		case 'a':
			sb.WriteByte(7)
		case '\\':
			sb.WriteByte('\\')
		case '\'':
			sb.WriteByte('\'')
		case '"':
			sb.WriteByte('"')
		case '\n':
		case 'x', 'u', 'U':
			k := map[rune]int{'x': 2, 'u': 4, 'U': 8}[rs[i]]
			if i+k >= len(rs) {
				return "", fmt.Errorf("sequência de escape \\%c incompleta", rs[i])
			}
			v, err := strconv.ParseUint(string(rs[i+1:i+1+k]), 16, 32)
			if err != nil {
				return "", fmt.Errorf("sequência de escape \\%c inválida", rs[i])
			}
			sb.WriteRune(rune(v))
			i += k
		default:
			sb.WriteByte('\\')
			sb.WriteRune(rs[i])
		}
	}
	return sb.String(), nil
}
