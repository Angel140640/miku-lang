package lang

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type fmtSpec struct {
	fill  rune
	align byte
	sign  byte
	zero  bool
	width int
	comma bool
	prec  int
	typ   byte
}

func parseSpec(s string) (fmtSpec, error) {
	sp := fmtSpec{fill: ' ', prec: -1}
	rs := []rune(s)
	i := 0
	isAlign := func(r rune) bool { return r == '<' || r == '>' || r == '^' || r == '=' }
	if len(rs) >= 2 && isAlign(rs[1]) {
		sp.fill, sp.align, i = rs[0], byte(rs[1]), 2
	} else if len(rs) >= 1 && isAlign(rs[0]) {
		sp.align, i = byte(rs[0]), 1
	}
	if i < len(rs) && (rs[i] == '+' || rs[i] == '-' || rs[i] == ' ') {
		sp.sign = byte(rs[i])
		i++
	}
	if i < len(rs) && rs[i] == '#' {
		i++
	}
	if i < len(rs) && rs[i] == '0' {
		sp.zero = true
		i++
	}
	for i < len(rs) && isDigit(rs[i]) {
		sp.width = sp.width*10 + int(rs[i]-'0')
		i++
	}
	if i < len(rs) && (rs[i] == ',' || rs[i] == '_') {
		sp.comma = true
		i++
	}
	if i < len(rs) && rs[i] == '.' {
		i++
		sp.prec = 0
		for i < len(rs) && isDigit(rs[i]) {
			sp.prec = sp.prec*10 + int(rs[i]-'0')
			i++
		}
	}
	if i < len(rs) {
		sp.typ = byte(rs[i])
		i++
	}
	if i != len(rs) {
		return sp, fmt.Errorf("formato inválido: '%s'", s)
	}
	return sp, nil
}

func groupThousands(digits string) string {
	if len(digits) <= 3 {
		return digits
	}
	var sb strings.Builder
	pre := len(digits) % 3
	if pre > 0 {
		sb.WriteString(digits[:pre])
	}
	for i := pre; i < len(digits); i += 3 {
		if sb.Len() > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(digits[i : i+3])
	}
	return sb.String()
}

// formatValue aplica um mini-formato estilo Python: f"{x:>8.2f}", f"{n:,}", f"{n:05d}".
func formatValue(v Value, spec string) (string, error) {
	if spec == "" {
		return Str(v), nil
	}
	sp, err := parseSpec(spec)
	if err != nil {
		return "", err
	}
	var body, sign string
	numeric := false
	_, isStr := v.(string)
	typ := sp.typ

	switch {
	case typ == 's' || (typ == 0 && isStr):
		if !isStr {
			body = Str(v)
		} else {
			body = v.(string)
		}
		if sp.prec >= 0 && sp.prec < len([]rune(body)) {
			body = string([]rune(body)[:sp.prec])
		}
	case typ == 0 && func() bool { _, ok := v.(float64); return !ok }() && func() bool { _, ok := toInt(v); return !ok }():
		body = Str(v)
	default:
		numeric = true
		f, ok := toFloat(v)
		if !ok {
			return "", fmt.Errorf("o formato '%s' só serve para números, mas o valor é %s", spec, typeName(v))
		}
		iv, isInt := toInt(v)
		switch typ {
		case 0, 'd', 'n':
			if typ != 0 && !isInt {
				return "", fmt.Errorf("o formato 'd' não funciona com %s (use 'f')", typeName(v))
			}
			if isInt {
				if iv < 0 {
					sign = "-"
					body = strconv.FormatUint(uint64(-iv), 10)
				} else {
					body = strconv.FormatInt(iv, 10)
				}
			} else {
				if f < 0 {
					sign = "-"
					f = -f
				}
				if sp.prec >= 0 {
					body = strconv.FormatFloat(f, 'g', sp.prec, 64)
				} else {
					body = formatFloat(f)
				}
			}
		case 'f', 'F', 'e', 'E', 'g', 'G', '%':
			if f < 0 || (f == 0 && math.Signbit(f)) {
				sign = "-"
				f = -f
			}
			p := sp.prec
			if p < 0 {
				p = 6
			}
			switch typ {
			case '%':
				body = strconv.FormatFloat(f*100, 'f', p, 64) + "%"
			case 'F':
				body = strings.ToUpper(strconv.FormatFloat(f, 'f', p, 64))
			default:
				body = strconv.FormatFloat(f, typ, p, 64)
			}
		case 'x', 'X', 'b', 'o':
			if !isInt {
				return "", fmt.Errorf("o formato '%c' só funciona com inteiros", typ)
			}
			base := map[byte]int{'x': 16, 'X': 16, 'b': 2, 'o': 8}[typ]
			if iv < 0 {
				sign = "-"
				body = strconv.FormatUint(uint64(-iv), base)
			} else {
				body = strconv.FormatInt(iv, base)
			}
			if typ == 'X' {
				body = strings.ToUpper(body)
			}
		default:
			return "", fmt.Errorf("tipo de formato desconhecido: '%c'", typ)
		}
		if sp.comma {
			intPart, rest := body, ""
			if k := strings.IndexAny(body, ".e%"); k >= 0 {
				intPart, rest = body[:k], body[k:]
			}
			body = groupThousands(intPart) + rest
		}
		if sign == "" && sp.sign == '+' {
			sign = "+"
		} else if sign == "" && sp.sign == ' ' {
			sign = " "
		}
	}

	total := len([]rune(sign)) + len([]rune(body))
	pad := sp.width - total
	if pad <= 0 {
		return sign + body, nil
	}
	align := sp.align
	fill := sp.fill
	if sp.zero && align == 0 {
		align, fill = '=', '0'
	}
	if align == 0 {
		if numeric {
			align = '>'
		} else {
			align = '<'
		}
	}
	p := strings.Repeat(string(fill), pad)
	switch align {
	case '<':
		return sign + body + p, nil
	case '>':
		return p + sign + body, nil
	case '^':
		l := pad / 2
		return strings.Repeat(string(fill), l) + sign + body + strings.Repeat(string(fill), pad-l), nil
	default: // '='
		return sign + p + body, nil
	}
}

// strFormat implementa "texto {} {0} {nome:>5}".format(...).
func strFormat(tmpl string, args []Value, kw map[string]Value) (string, error) {
	rs := []rune(tmpl)
	var sb strings.Builder
	auto := 0
	for i := 0; i < len(rs); {
		c := rs[i]
		switch {
		case c == '{' && i+1 < len(rs) && rs[i+1] == '{':
			sb.WriteByte('{')
			i += 2
		case c == '}' && i+1 < len(rs) && rs[i+1] == '}':
			sb.WriteByte('}')
			i += 2
		case c == '{':
			j := i + 1
			for j < len(rs) && rs[j] != '}' {
				j++
			}
			if j >= len(rs) {
				return "", fmt.Errorf("'{' sem '}' no format")
			}
			field := string(rs[i+1 : j])
			spec, conv := "", byte(0)
			if k := strings.IndexByte(field, ':'); k >= 0 {
				field, spec = field[:k], field[k+1:]
			}
			if k := strings.IndexByte(field, '!'); k >= 0 {
				if k+1 < len(field) {
					conv = field[k+1]
				}
				field = field[:k]
			}
			var v Value
			switch {
			case field == "":
				if auto >= len(args) {
					return "", fmt.Errorf("format: faltam argumentos para os {}")
				}
				v = args[auto]
				auto++
			case isDigit(rune(field[0])):
				n, _ := strconv.Atoi(field)
				if n >= len(args) {
					return "", fmt.Errorf("format: não existe o argumento %d", n)
				}
				v = args[n]
			default:
				val, ok := kw[field]
				if !ok {
					return "", fmt.Errorf("format: não existe o argumento '%s'", field)
				}
				v = val
			}
			if conv == 'r' {
				v = Repr(v)
			}
			s, err := formatValue(v, spec)
			if err != nil {
				return "", err
			}
			sb.WriteString(s)
			i = j + 1
		default:
			sb.WriteRune(c)
			i++
		}
	}
	return sb.String(), nil
}
