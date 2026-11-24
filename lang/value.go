package lang

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Value é qualquer valor da linguagem: nil (None), bool, int64, float64, string,
// *List, *Dict, *Func, *Builtin ou *Object.
type Value = any

type List struct {
	Items []Value
	Tuple bool
}

func NewList(items ...Value) *List  { return &List{Items: items} }
func NewTuple(items ...Value) *List { return &List{Items: items, Tuple: true} }

// Dict mantém a ordem de inserção, como no Python.
type Dict struct {
	keys []Value
	m    map[any]Value
}

type nilKey struct{}
type tupleKey struct{ s string }

func NewDict() *Dict { return &Dict{m: map[any]Value{}} }

func hashKey(v Value) (any, error) {
	switch x := v.(type) {
	case nil:
		return nilKey{}, nil
	case bool, int64, string:
		return x, nil
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return int64(x), nil
		}
		return x, nil
	case *List:
		if x.Tuple {
			return tupleKey{Repr(x)}, nil
		}
		return nil, fmt.Errorf("uma lista não pode ser chave de dicionário")
	case *Dict:
		return nil, fmt.Errorf("um dicionário não pode ser chave de dicionário")
	}
	return v, nil // ponteiros (Func, Builtin, Object) comparam por identidade
}

func (d *Dict) Get(k Value) (Value, bool, error) {
	hk, err := hashKey(k)
	if err != nil {
		return nil, false, err
	}
	v, ok := d.m[hk]
	return v, ok, nil
}

func (d *Dict) Set(k, v Value) error {
	hk, err := hashKey(k)
	if err != nil {
		return err
	}
	if _, ok := d.m[hk]; !ok {
		d.keys = append(d.keys, k)
	}
	d.m[hk] = v
	return nil
}

func (d *Dict) Delete(k Value) (bool, error) {
	hk, err := hashKey(k)
	if err != nil {
		return false, err
	}
	if _, ok := d.m[hk]; !ok {
		return false, nil
	}
	delete(d.m, hk)
	for i, key := range d.keys {
		if h, _ := hashKey(key); h == hk {
			d.keys = append(d.keys[:i:i], d.keys[i+1:]...)
			break
		}
	}
	return true, nil
}

func (d *Dict) Len() int { return len(d.keys) }

func (d *Dict) Keys() []Value { return append([]Value(nil), d.keys...) }

func (d *Dict) GetStr(k string) (Value, bool) { v, ok := d.m[k]; return v, ok }

func (d *Dict) SetStr(k string, v Value) { _ = d.Set(k, v) }

type Func struct {
	Name     string
	Params   []Param
	Defaults []Value
	Body     []Stmt
	LamBody  Expr
	Env      *Env
}

type Builtin struct {
	Name string
	Fn   func(in *Interp, args []Value, kw map[string]Value) (Value, error)
}

// Object é um valor "nativo" do bot (usuário, mensagem, canal, embed...).
type Object struct {
	Type   string
	Attrs  map[string]Value
	Repr   func() string
	Native any // dados do host (ex.: o JSON de um embed); usado por ToGo
	// Get é consultado quando o atributo não está em Attrs (atributos calculados na hora).
	Get func(name string) (Value, bool)
}

func NewObject(typ string) *Object { return &Object{Type: typ, Attrs: map[string]Value{}} }

// ---- escopos ----

type Env struct {
	vars    map[string]Value
	parent  *Env
	globals map[string]bool
	depth   int
}

func NewEnv(parent *Env) *Env {
	e := &Env{vars: map[string]Value{}, parent: parent}
	if parent != nil {
		e.depth = parent.depth
	}
	return e
}

func (e *Env) Lookup(name string) (Value, bool) {
	for c := e; c != nil; c = c.parent {
		if v, ok := c.vars[name]; ok {
			return v, true
		}
	}
	return nil, false
}

func (e *Env) root() *Env {
	c := e
	for c.parent != nil {
		c = c.parent
	}
	return c
}

// Set grava na variável local (ou na global, se foi declarada com `global`).
func (e *Env) Set(name string, v Value) {
	if e.globals[name] {
		e.root().vars[name] = v
		return
	}
	e.vars[name] = v
}

func (e *Env) SetLocal(name string, v Value) { e.vars[name] = v }

// ---- conversões e texto ----

func typeName(v Value) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		return "bool"
	case int64:
		return "int"
	case float64:
		return "float"
	case string:
		return "str"
	case *List:
		if x.Tuple {
			return "tuple"
		}
		return "list"
	case *Dict:
		return "dict"
	case *Func:
		return "function"
	case *Builtin:
		return "function"
	case *Object:
		return x.Type
	}
	return fmt.Sprintf("%T", v)
}

func toFloat(v Value) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

func toInt(v Value) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

func formatFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	a := math.Abs(f)
	if a != 0 && (a >= 1e16 || a < 1e-4) {
		return strconv.FormatFloat(f, 'e', -1, 64)
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsRune(s, '.') {
		s += ".0"
	}
	return s
}

func quote(s string) string {
	q := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		q = '"'
	}
	var sb strings.Builder
	sb.WriteByte(q)
	for _, r := range s {
		switch {
		case r == '\\':
			sb.WriteString(`\\`)
		case r == '\n':
			sb.WriteString(`\n`)
		case r == '\t':
			sb.WriteString(`\t`)
		case r == '\r':
			sb.WriteString(`\r`)
		case r == rune(q):
			sb.WriteByte('\\')
			sb.WriteRune(r)
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte(q)
	return sb.String()
}

// Str é o que print() e f"{x}" mostram.
func Str(v Value) string {
	if s, ok := v.(string); ok {
		return s
	}
	return repr(v, 0)
}

// Repr é a forma "com aspas" usada dentro de listas e dicionários.
func Repr(v Value) string { return repr(v, 0) }

func repr(v Value, depth int) string {
	if depth > 20 {
		return "..."
	}
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return formatFloat(x)
	case string:
		return quote(x)
	case *List:
		parts := make([]string, len(x.Items))
		for i, it := range x.Items {
			parts[i] = repr(it, depth+1)
		}
		if x.Tuple {
			if len(parts) == 1 {
				return "(" + parts[0] + ",)"
			}
			return "(" + strings.Join(parts, ", ") + ")"
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *Dict:
		parts := make([]string, 0, len(x.keys))
		for _, k := range x.keys {
			hk, _ := hashKey(k)
			parts = append(parts, repr(k, depth+1)+": "+repr(x.m[hk], depth+1))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *Func:
		return "<função " + x.Name + ">"
	case *Builtin:
		return "<função embutida " + x.Name + ">"
	case *Object:
		if x.Repr != nil {
			return x.Repr()
		}
		return "<" + x.Type + ">"
	}
	return fmt.Sprint(v)
}

func truthy(v Value) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case int64:
		return x != 0
	case float64:
		return x != 0
	case string:
		return x != ""
	case *List:
		return len(x.Items) > 0
	case *Dict:
		return x.Len() > 0
	}
	return true
}

func equal(a, b Value) bool {
	if fa, ok := toFloat(a); ok {
		if fb, ok := toFloat(b); ok {
			ia, aInt := toInt(a)
			ib, bInt := toInt(b)
			if aInt && bInt {
				return ia == ib
			}
			return fa == fb
		}
		return false
	}
	switch x := a.(type) {
	case nil:
		return b == nil
	case string:
		y, ok := b.(string)
		return ok && x == y
	case *List:
		y, ok := b.(*List)
		if !ok || x.Tuple != y.Tuple || len(x.Items) != len(y.Items) {
			return false
		}
		for i := range x.Items {
			if !equal(x.Items[i], y.Items[i]) {
				return false
			}
		}
		return true
	case *Dict:
		y, ok := b.(*Dict)
		if !ok || x.Len() != y.Len() {
			return false
		}
		for _, k := range x.keys {
			hk, _ := hashKey(k)
			yv, ok := y.m[hk]
			if !ok || !equal(x.m[hk], yv) {
				return false
			}
		}
		return true
	}
	return a == b
}

// compareValues devolve -1, 0 ou 1 (números, textos e listas).
func compareValues(a, b Value) (int, error) {
	if fa, ok := toFloat(a); ok {
		if fb, ok := toFloat(b); ok {
			ia, aInt := toInt(a)
			ib, bInt := toInt(b)
			if aInt && bInt {
				switch {
				case ia < ib:
					return -1, nil
				case ia > ib:
					return 1, nil
				}
				return 0, nil
			}
			switch {
			case fa < fb:
				return -1, nil
			case fa > fb:
				return 1, nil
			}
			return 0, nil
		}
	}
	if sa, ok := a.(string); ok {
		if sb, ok := b.(string); ok {
			return strings.Compare(sa, sb), nil
		}
	}
	if la, ok := a.(*List); ok {
		if lb, ok := b.(*List); ok {
			for i := 0; i < len(la.Items) && i < len(lb.Items); i++ {
				c, err := compareValues(la.Items[i], lb.Items[i])
				if err != nil || c != 0 {
					return c, err
				}
			}
			switch {
			case len(la.Items) < len(lb.Items):
				return -1, nil
			case len(la.Items) > len(lb.Items):
				return 1, nil
			}
			return 0, nil
		}
	}
	return 0, fmt.Errorf("não dá para comparar %s com %s", typeName(a), typeName(b))
}
