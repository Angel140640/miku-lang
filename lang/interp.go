package lang

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
)

// Host recebe as diretivas de bot (command, on, slash, token...) quando elas executam.
type Host interface {
	Declare(in *Interp, s Stmt, env *Env) error
}

type BuiltinFn = func(in *Interp, args []Value, kw map[string]Value) (Value, error)

type Interp struct {
	Globals *Env
	Host    Host
	Out     io.Writer
	Hints   map[string]string // nome -> dica mostrada quando a variável não existe
	mu      sync.Mutex
}

type ctl int

const (
	ctlNone ctl = iota
	ctlBreak
	ctlContinue
	ctlReturn
)

type frame struct{ ret Value }

// raised é o erro criado por `raise valor`.
type raised struct {
	Line int
	Val  Value
}

func (r *raised) Error() string {
	if r.Line > 0 {
		return fmt.Sprintf("linha %d: %s", r.Line, Str(r.Val))
	}
	return Str(r.Val)
}

const maxDepth = 150

func New(out io.Writer) *Interp {
	if out == nil {
		out = os.Stdout
	}
	in := &Interp{Globals: NewEnv(nil), Out: out, Hints: map[string]string{}}
	registerBuiltins(in)
	return in
}

func (in *Interp) Define(name string, v Value) { in.Globals.vars[name] = v }

func (in *Interp) DefineFunc(name string, f BuiltinFn) {
	in.Define(name, &Builtin{Name: name, Fn: f})
}

// Block solta o lock do interpretador durante uma operação lenta (rede, sleep).
// Só pode ser chamado de dentro de um builtin/handler (que já tem o lock).
func (in *Interp) Block(f func()) {
	in.mu.Unlock()
	defer in.mu.Lock()
	f()
}

// Exec roda o programa inteiro (declarações incluídas).
func (in *Interp) Exec(prog *Program) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	fr := &frame{}
	for _, s := range prog.Stmts {
		c, err := in.exec(s, in.Globals, fr)
		if err != nil {
			return err
		}
		if c == ctlReturn {
			break
		}
	}
	return nil
}

// RunHandler roda o corpo de um command/on/slash com as variáveis dadas.
func (in *Interp) RunHandler(body []Stmt, vars map[string]Value) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	env := NewEnv(in.Globals)
	for k, v := range vars {
		env.vars[k] = v
	}
	_, err := in.execBlock(body, env, &frame{})
	return err
}

// Eval avalia uma expressão (use dentro de Host.Declare, que já tem o lock).
func (in *Interp) Eval(e Expr, env *Env) (Value, error) { return in.eval(e, env) }

// Call chama uma função da linguagem estando dentro do lock.
func (in *Interp) Call(fn Value, args []Value, kw map[string]Value) (Value, error) {
	return in.callValue(fn, args, kw, 0)
}

// CallLocked chama uma função da linguagem de fora do lock (ex.: timers).
func (in *Interp) CallLocked(fn Value, args []Value, kw map[string]Value) (Value, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.callValue(fn, args, kw, 0)
}

func fixLine(err error, line int) error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		if e.Line == 0 {
			return &Error{Line: line, Msg: e.Msg}
		}
		return err
	}
	var r *raised
	if errors.As(err, &r) {
		return err
	}
	return &Error{Line: line, Msg: err.Error()}
}

// ErrorMessage devolve só o texto do erro, sem o prefixo "linha N".
func ErrorMessage(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Msg
	}
	var r *raised
	if errors.As(err, &r) {
		return Str(r.Val)
	}
	return err.Error()
}

// ErrorLine devolve a linha do erro (0 se não souber).
func ErrorLine(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Line
	}
	var r *raised
	if errors.As(err, &r) {
		return r.Line
	}
	return 0
}

// ---------------- execução ----------------

func (in *Interp) execBlock(body []Stmt, env *Env, fr *frame) (ctl, error) {
	for _, s := range body {
		c, err := in.exec(s, env, fr)
		if err != nil || c != ctlNone {
			return c, err
		}
	}
	return ctlNone, nil
}

func (in *Interp) exec(s Stmt, env *Env, fr *frame) (ctl, error) {
	switch s := s.(type) {
	case *ExprStmt:
		_, err := in.eval(s.X, env)
		return ctlNone, err

	case *Assign:
		v, err := in.eval(s.Value, env)
		if err != nil {
			return ctlNone, err
		}
		for _, t := range s.Targets {
			if err := in.assign(t, v, env); err != nil {
				return ctlNone, err
			}
		}

	case *AugAssign:
		cur, err := in.eval(s.Target, env)
		if err != nil {
			return ctlNone, err
		}
		val, err := in.eval(s.Value, env)
		if err != nil {
			return ctlNone, err
		}
		var res Value
		done := false
		if l, ok := cur.(*List); ok && s.Op == "+" && !l.Tuple {
			if r, ok := val.(*List); ok {
				l.Items = append(l.Items, r.Items...)
				res, done = l, true
			}
		}
		if !done {
			if res, err = in.binop(s.Op, cur, val); err != nil {
				return ctlNone, fixLine(err, s.Ln)
			}
		}
		return ctlNone, in.assign(s.Target, res, env)

	case *If:
		c, err := in.eval(s.Cond, env)
		if err != nil {
			return ctlNone, err
		}
		if truthy(c) {
			return in.execBlock(s.Then, env, fr)
		}
		return in.execBlock(s.Else, env, fr)

	case *While:
		for {
			c, err := in.eval(s.Cond, env)
			if err != nil {
				return ctlNone, err
			}
			if !truthy(c) {
				break
			}
			k, err := in.execBlock(s.Body, env, fr)
			if err != nil {
				return ctlNone, err
			}
			if k == ctlBreak {
				break
			}
			if k == ctlReturn {
				return k, nil
			}
		}

	case *For:
		itv, err := in.eval(s.Iter, env)
		if err != nil {
			return ctlNone, err
		}
		step := func(item Value) (stop bool, k ctl, err error) {
			if err := in.assign(s.Target, item, env); err != nil {
				return true, ctlNone, err
			}
			k, err = in.execBlock(s.Body, env, fr)
			if err != nil || k == ctlReturn {
				return true, k, err
			}
			return k == ctlBreak, ctlNone, nil
		}
		if l, ok := itv.(*List); ok {
			for i := 0; i < len(l.Items); i++ {
				if stop, k, err := step(l.Items[i]); stop {
					return k, err
				}
			}
			break
		}
		items, err := in.iterate(itv, s.Ln)
		if err != nil {
			return ctlNone, err
		}
		for _, it := range items {
			if stop, k, err := step(it); stop {
				return k, err
			}
		}

	case *Break:
		return ctlBreak, nil
	case *Continue:
		return ctlContinue, nil
	case *Pass:

	case *Return:
		fr.ret = nil
		if s.Val != nil {
			v, err := in.eval(s.Val, env)
			if err != nil {
				return ctlNone, err
			}
			fr.ret = v
		}
		return ctlReturn, nil

	case *Def:
		f := &Func{Name: s.Name, Params: s.Params, Body: s.Body, Env: env}
		defs, err := in.evalDefaults(s.Params, env)
		if err != nil {
			return ctlNone, err
		}
		f.Defaults = defs
		env.Set(s.Name, f)

	case *Global:
		if env.globals == nil {
			env.globals = map[string]bool{}
		}
		for _, n := range s.Names {
			env.globals[n] = true
		}

	case *Try:
		return in.execTry(s, env, fr)

	case *Raise:
		if s.Val == nil {
			return ctlNone, errAt(s.Ln, "raise precisa de um valor (ex.: raise \"deu ruim\")")
		}
		v, err := in.eval(s.Val, env)
		if err != nil {
			return ctlNone, err
		}
		return ctlNone, &raised{Line: s.Ln, Val: v}

	case *Del:
		switch t := s.Target.(type) {
		case *Name:
			if _, ok := env.vars[t.Id]; !ok {
				return ctlNone, errAt(s.Ln, "a variável '%s' não existe aqui", t.Id)
			}
			delete(env.vars, t.Id)
		case *Index:
			c, err := in.eval(t.X, env)
			if err != nil {
				return ctlNone, err
			}
			i, err := in.eval(t.I, env)
			if err != nil {
				return ctlNone, err
			}
			switch c := c.(type) {
			case *Dict:
				ok, err := c.Delete(i)
				if err != nil {
					return ctlNone, errAt(s.Ln, "%v", err)
				}
				if !ok {
					return ctlNone, errAt(s.Ln, "a chave %s não existe", Repr(i))
				}
			case *List:
				if c.Tuple {
					return ctlNone, errAt(s.Ln, "uma tupla não pode ser alterada")
				}
				idx, err := normIndex(len(c.Items), i)
				if err != nil {
					return ctlNone, errAt(s.Ln, "%v", err)
				}
				c.Items = append(c.Items[:idx:idx], c.Items[idx+1:]...)
			default:
				return ctlNone, errAt(s.Ln, "não dá para usar del em %s", typeName(c))
			}
		}

	case *Say:
		return ctlNone, in.execSay(s, env)

	case *TokenDecl, *PrefixDecl, *IntentsDecl, *ShardsDecl, *StatusDecl, *ActivityDecl,
		*CommandDecl, *OnDecl, *SlashDecl:
		if in.Host == nil {
			return ctlNone, errAt(s.Pos(), "esta diretiva só funciona dentro de um bot")
		}
		return ctlNone, fixLine(in.Host.Declare(in, s, env), s.Pos())

	default:
		return ctlNone, errAt(s.Pos(), "comando não suportado")
	}
	return ctlNone, nil
}

func (in *Interp) evalDefaults(params []Param, env *Env) ([]Value, error) {
	defs := make([]Value, len(params))
	for i, p := range params {
		if p.Default != nil {
			v, err := in.eval(p.Default, env)
			if err != nil {
				return nil, err
			}
			defs[i] = v
		}
	}
	return defs, nil
}

func (in *Interp) execTry(s *Try, env *Env, fr *frame) (ctl, error) {
	c, err := in.execBlock(s.Body, env, fr)
	if err != nil && s.HasExcept {
		var e *Error
		var r *raised
		if errors.As(err, &e) || errors.As(err, &r) {
			if s.ExcName != "" {
				if r != nil {
					env.Set(s.ExcName, r.Val)
				} else {
					env.Set(s.ExcName, e.Msg)
				}
			}
			c, err = in.execBlock(s.Except, env, fr)
		}
	}
	if s.Finally != nil {
		fc, ferr := in.execBlock(s.Finally, env, fr)
		if ferr != nil {
			return fc, ferr
		}
		if fc != ctlNone {
			return fc, nil
		}
	}
	return c, err
}

func (in *Interp) execSay(s *Say, env *Env) error {
	var target Value
	found := false
	for _, nm := range []string{"ctx", "message"} {
		if v, ok := env.Lookup(nm); ok {
			target, found = v, true
			break
		}
	}
	if !found {
		return errAt(s.Ln, "'%s' só funciona dentro de command, slash ou on message", s.Kind)
	}
	args := make([]Value, len(s.Args))
	for i, a := range s.Args {
		v, err := in.eval(a, env)
		if err != nil {
			return err
		}
		args[i] = v
	}
	var kw map[string]Value
	if len(s.KwNames) > 0 {
		kw = make(map[string]Value, len(s.KwNames))
		for i, n := range s.KwNames {
			v, err := in.eval(s.KwVals[i], env)
			if err != nil {
				return err
			}
			kw[n] = v
		}
	}
	fn, err := in.getAttr(target, s.Kind)
	if err != nil {
		return errAt(s.Ln, "%v", err)
	}
	_, err = in.callValue(fn, args, kw, env.depth)
	return fixLine(err, s.Ln)
}

func (in *Interp) assign(t Expr, v Value, env *Env) error {
	switch t := t.(type) {
	case *Name:
		env.Set(t.Id, v)
	case *Attr:
		o, err := in.eval(t.X, env)
		if err != nil {
			return err
		}
		obj, ok := o.(*Object)
		if !ok {
			return errAt(t.Ln, "não dá para mudar '%s' em %s", t.Name, typeName(o))
		}
		obj.Attrs[t.Name] = v
	case *Index:
		c, err := in.eval(t.X, env)
		if err != nil {
			return err
		}
		i, err := in.eval(t.I, env)
		if err != nil {
			return err
		}
		switch c := c.(type) {
		case *List:
			if c.Tuple {
				return errAt(t.Ln, "uma tupla não pode ser alterada")
			}
			idx, err := normIndex(len(c.Items), i)
			if err != nil {
				return errAt(t.Ln, "%v", err)
			}
			c.Items[idx] = v
		case *Dict:
			if err := c.Set(i, v); err != nil {
				return errAt(t.Ln, "%v", err)
			}
		default:
			return errAt(t.Ln, "não dá para atribuir um item em %s", typeName(c))
		}
	case *TupleLit:
		return in.unpack(t.Elems, v, env, t.Ln)
	case *ListLit:
		return in.unpack(t.Elems, v, env, t.Ln)
	default:
		return errAt(t.Pos(), "não dá para atribuir a isso")
	}
	return nil
}

func (in *Interp) unpack(targets []Expr, v Value, env *Env, line int) error {
	items, err := in.iterate(v, line)
	if err != nil {
		return err
	}
	if len(items) != len(targets) {
		return errAt(line, "esperava desempacotar %d valores, mas vieram %d", len(targets), len(items))
	}
	for i, t := range targets {
		if err := in.assign(t, items[i], env); err != nil {
			return err
		}
	}
	return nil
}

func (in *Interp) iterate(v Value, line int) ([]Value, error) {
	switch x := v.(type) {
	case *List:
		return x.Items, nil
	case *Dict:
		return x.Keys(), nil
	case string:
		rs := []rune(x)
		out := make([]Value, len(rs))
		for i, r := range rs {
			out[i] = string(r)
		}
		return out, nil
	}
	return nil, errAt(line, "não dá para percorrer um valor do tipo %s", typeName(v))
}

// ---------------- expressões ----------------

func (in *Interp) evalAll(es []Expr, env *Env) ([]Value, error) {
	out := make([]Value, len(es))
	for i, e := range es {
		v, err := in.eval(e, env)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func (in *Interp) eval(e Expr, env *Env) (Value, error) {
	switch x := e.(type) {
	case *Const:
		return x.Val, nil

	case *Name:
		if v, ok := env.Lookup(x.Id); ok {
			return v, nil
		}
		msg := fmt.Sprintf("a variável '%s' não existe", x.Id)
		if h := in.Hints[x.Id]; h != "" {
			msg += " — " + h
		}
		return nil, &Error{Line: x.Ln, Msg: msg}

	case *FStr:
		var sb strings.Builder
		for _, p := range x.Parts {
			if p.X == nil {
				sb.WriteString(p.Lit)
				continue
			}
			v, err := in.eval(p.X, env)
			if err != nil {
				return nil, err
			}
			if p.Conv == 'r' {
				v = Repr(v)
			}
			s, err := formatValue(v, p.Spec)
			if err != nil {
				return nil, errAt(x.Ln, "%v", err)
			}
			sb.WriteString(s)
		}
		return sb.String(), nil

	case *ListLit:
		items, err := in.evalAll(x.Elems, env)
		if err != nil {
			return nil, err
		}
		return &List{Items: items}, nil

	case *TupleLit:
		items, err := in.evalAll(x.Elems, env)
		if err != nil {
			return nil, err
		}
		return &List{Items: items, Tuple: true}, nil

	case *DictLit:
		d := NewDict()
		for i := range x.Keys {
			k, err := in.eval(x.Keys[i], env)
			if err != nil {
				return nil, err
			}
			v, err := in.eval(x.Vals[i], env)
			if err != nil {
				return nil, err
			}
			if err := d.Set(k, v); err != nil {
				return nil, errAt(x.Ln, "%v", err)
			}
		}
		return d, nil

	case *Unary:
		v, err := in.eval(x.X, env)
		if err != nil {
			return nil, err
		}
		if x.Op == "not" {
			return !truthy(v), nil
		}
		if i, ok := toInt(v); ok {
			if x.Op == "-" {
				return -i, nil
			}
			return i, nil
		}
		if f, ok := v.(float64); ok {
			if x.Op == "-" {
				return -f, nil
			}
			return f, nil
		}
		return nil, errAt(x.Ln, "não dá para usar '%s' em %s", x.Op, typeName(v))

	case *Binary:
		l, err := in.eval(x.L, env)
		if err != nil {
			return nil, err
		}
		r, err := in.eval(x.R, env)
		if err != nil {
			return nil, err
		}
		v, err := in.binop(x.Op, l, r)
		return v, fixLine(err, x.Ln)

	case *BoolOp:
		l, err := in.eval(x.L, env)
		if err != nil {
			return nil, err
		}
		if x.And != truthy(l) {
			return l, nil // and: falso encerra; or: verdadeiro encerra
		}
		return in.eval(x.R, env)

	case *Compare:
		left, err := in.eval(x.First, env)
		if err != nil {
			return nil, err
		}
		for i, op := range x.Ops {
			right, err := in.eval(x.Rest[i], env)
			if err != nil {
				return nil, err
			}
			ok, err := in.cmpOp(op, left, right)
			if err != nil {
				return nil, fixLine(err, x.Ln)
			}
			if !ok {
				return false, nil
			}
			left = right
		}
		return true, nil

	case *IfExp:
		c, err := in.eval(x.Cond, env)
		if err != nil {
			return nil, err
		}
		if truthy(c) {
			return in.eval(x.Then, env)
		}
		return in.eval(x.Else, env)

	case *Call:
		fn, err := in.eval(x.Fn, env)
		if err != nil {
			return nil, err
		}
		args, err := in.evalAll(x.Args, env)
		if err != nil {
			return nil, err
		}
		var kw map[string]Value
		if len(x.KwNames) > 0 {
			kw = make(map[string]Value, len(x.KwNames))
			for i, n := range x.KwNames {
				v, err := in.eval(x.KwVals[i], env)
				if err != nil {
					return nil, err
				}
				kw[n] = v
			}
		}
		v, err := in.callValue(fn, args, kw, env.depth)
		return v, fixLine(err, x.Ln)

	case *Attr:
		o, err := in.eval(x.X, env)
		if err != nil {
			return nil, err
		}
		v, err := in.getAttr(o, x.Name)
		return v, fixLine(err, x.Ln)

	case *Index:
		c, err := in.eval(x.X, env)
		if err != nil {
			return nil, err
		}
		i, err := in.eval(x.I, env)
		if err != nil {
			return nil, err
		}
		v, err := in.index(c, i)
		return v, fixLine(err, x.Ln)

	case *SliceExpr:
		c, err := in.eval(x.X, env)
		if err != nil {
			return nil, err
		}
		var parts [3]Value
		for k, pe := range []Expr{x.Lo, x.Hi, x.Step} {
			if pe != nil {
				if parts[k], err = in.eval(pe, env); err != nil {
					return nil, err
				}
			}
		}
		v, err := in.slice(c, parts[0], parts[1], parts[2])
		return v, fixLine(err, x.Ln)

	case *Lambda:
		defs, err := in.evalDefaults(x.Params, env)
		if err != nil {
			return nil, err
		}
		return &Func{Name: "<lambda>", Params: x.Params, Defaults: defs, LamBody: x.Body, Env: env}, nil

	case *ListComp:
		itv, err := in.eval(x.Iter, env)
		if err != nil {
			return nil, err
		}
		items, err := in.iterate(itv, x.Ln)
		if err != nil {
			return nil, err
		}
		ce := NewEnv(env)
		out := []Value{}
		for _, item := range append([]Value(nil), items...) {
			if err := in.assign(x.Target, item, ce); err != nil {
				return nil, err
			}
			if x.Cond != nil {
				c, err := in.eval(x.Cond, ce)
				if err != nil {
					return nil, err
				}
				if !truthy(c) {
					continue
				}
			}
			v, err := in.eval(x.Elem, ce)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return &List{Items: out}, nil
	}
	return nil, errAt(e.Pos(), "expressão não suportada")
}

func (in *Interp) callValue(fn Value, args []Value, kw map[string]Value, depth int) (Value, error) {
	switch f := fn.(type) {
	case *Builtin:
		return f.Fn(in, args, kw)
	case *Func:
		if depth >= maxDepth {
			return nil, errors.New("chamadas aninhadas demais (recursão sem fim?)")
		}
		env := NewEnv(f.Env)
		env.depth = depth + 1
		if err := bindParams(f, env, args, kw); err != nil {
			return nil, err
		}
		if f.LamBody != nil {
			return in.eval(f.LamBody, env)
		}
		fr := &frame{}
		if _, err := in.execBlock(f.Body, env, fr); err != nil {
			return nil, err
		}
		return fr.ret, nil
	}
	return nil, fmt.Errorf("%s não é uma função", typeName(fn))
}

func bindParams(f *Func, env *Env, args []Value, kw map[string]Value) error {
	i, used := 0, 0
	for pi, p := range f.Params {
		if p.Kind == PVar {
			rest := []Value{}
			if i < len(args) {
				rest = append(rest, args[i:]...)
			}
			i = len(args)
			env.vars[p.Name] = &List{Items: rest, Tuple: true}
			continue
		}
		if i < len(args) {
			if _, dup := kw[p.Name]; dup {
				return fmt.Errorf("%s() recebeu dois valores para '%s'", f.Name, p.Name)
			}
			env.vars[p.Name] = args[i]
			i++
		} else if v, ok := kw[p.Name]; ok {
			env.vars[p.Name] = v
			used++
		} else if p.Default != nil && pi < len(f.Defaults) {
			env.vars[p.Name] = f.Defaults[pi]
		} else {
			return fmt.Errorf("%s() precisa do argumento '%s'", f.Name, p.Name)
		}
	}
	if i < len(args) {
		return fmt.Errorf("%s() recebeu %d argumento(s) a mais do que aceita", f.Name, len(args)-i)
	}
	if used < len(kw) {
		for k := range kw {
			known := false
			for _, p := range f.Params {
				if p.Name == k && p.Kind != PVar {
					known = true
				}
			}
			if !known {
				return fmt.Errorf("%s() não tem o argumento '%s'", f.Name, k)
			}
		}
	}
	return nil
}

func (in *Interp) getAttr(v Value, name string) (Value, error) {
	switch x := v.(type) {
	case *Object:
		if a, ok := x.Attrs[name]; ok {
			return a, nil
		}
		if x.Get != nil {
			if a, ok := x.Get(name); ok {
				return a, nil
			}
		}
		return nil, fmt.Errorf("%s não tem o atributo '%s'", x.Type, name)
	case string:
		if m := strMethod(x, name); m != nil {
			return m, nil
		}
	case *List:
		if m := listMethod(x, name); m != nil {
			return m, nil
		}
	case *Dict:
		if m := dictMethod(x, name); m != nil {
			return m, nil
		}
	}
	return nil, fmt.Errorf("%s não tem o atributo '%s'", typeName(v), name)
}

func normIndex(n int, i Value) (int, error) {
	iv, ok := toInt(i)
	if !ok {
		return 0, fmt.Errorf("índice precisa ser inteiro, não %s", typeName(i))
	}
	idx := int(iv)
	if idx < 0 {
		idx += n
	}
	if idx < 0 || idx >= n {
		return 0, fmt.Errorf("índice %d fora do alcance (tamanho %d)", iv, n)
	}
	return idx, nil
}

func (in *Interp) index(c, i Value) (Value, error) {
	switch x := c.(type) {
	case *List:
		idx, err := normIndex(len(x.Items), i)
		if err != nil {
			return nil, err
		}
		return x.Items[idx], nil
	case string:
		rs := []rune(x)
		idx, err := normIndex(len(rs), i)
		if err != nil {
			return nil, err
		}
		return string(rs[idx]), nil
	case *Dict:
		v, ok, err := x.Get(i)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("a chave %s não existe no dicionário", Repr(i))
		}
		return v, nil
	}
	return nil, fmt.Errorf("não dá para usar [] em %s", typeName(c))
}

func (in *Interp) slice(c, lo, hi, step Value) (Value, error) {
	var n int
	var rs []rune
	var items []Value
	var tuple bool
	switch x := c.(type) {
	case string:
		rs = []rune(x)
		n = len(rs)
	case *List:
		items, n, tuple = x.Items, len(x.Items), x.Tuple
	default:
		return nil, fmt.Errorf("não dá para fatiar %s", typeName(c))
	}
	st := int64(1)
	if step != nil {
		v, ok := toInt(step)
		if !ok || v == 0 {
			return nil, fmt.Errorf("o passo da fatia precisa ser um inteiro diferente de 0")
		}
		st = v
	}
	bound := func(v Value, def int, isStop bool) (int, error) {
		if v == nil {
			return def, nil
		}
		iv, ok := toInt(v)
		if !ok {
			return 0, fmt.Errorf("índices da fatia precisam ser inteiros")
		}
		k := int(iv)
		if k < 0 {
			k += n
			if k < 0 {
				if st > 0 {
					k = 0
				} else {
					k = -1
				}
			}
		} else if k >= n {
			if st > 0 {
				k = n
			} else {
				k = n - 1
			}
		}
		return k, nil
	}
	var start, stop int
	var err error
	if st > 0 {
		if start, err = bound(lo, 0, false); err != nil {
			return nil, err
		}
		if stop, err = bound(hi, n, true); err != nil {
			return nil, err
		}
	} else {
		if start, err = bound(lo, n-1, false); err != nil {
			return nil, err
		}
		if stop, err = bound(hi, -1, true); err != nil {
			return nil, err
		}
	}
	var idxs []int
	if st > 0 {
		for k := start; k < stop; k += int(st) {
			idxs = append(idxs, k)
		}
	} else {
		for k := start; k > stop; k += int(st) {
			idxs = append(idxs, k)
		}
	}
	if rs != nil || (items == nil && n == 0 && isString(c)) {
		out := make([]rune, 0, len(idxs))
		for _, k := range idxs {
			out = append(out, rs[k])
		}
		return string(out), nil
	}
	out := make([]Value, 0, len(idxs))
	for _, k := range idxs {
		out = append(out, items[k])
	}
	return &List{Items: out, Tuple: tuple}, nil
}

func isString(v Value) bool { _, ok := v.(string); return ok }

// ---------------- operadores ----------------

func mulOverflows(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	c := a * b
	if c/b != a || (a == -1 && b == math.MinInt64) || (b == -1 && a == math.MinInt64) {
		return 0, false
	}
	return c, true
}

func ipow(base, exp int64) (int64, bool) {
	result := int64(1)
	var ok bool
	for exp > 0 {
		if exp&1 == 1 {
			if result, ok = mulOverflows(result, base); !ok {
				return 0, false
			}
		}
		exp >>= 1
		if exp > 0 {
			if base, ok = mulOverflows(base, base); !ok {
				return 0, false
			}
		}
	}
	return result, true
}

const maxRepeat = 50_000_000

func (in *Interp) binop(op string, a, b Value) (Value, error) {
	switch op {
	case "+":
		switch x := a.(type) {
		case string:
			if y, ok := b.(string); ok {
				return x + y, nil
			}
		case *List:
			if y, ok := b.(*List); ok {
				items := make([]Value, 0, len(x.Items)+len(y.Items))
				items = append(items, x.Items...)
				items = append(items, y.Items...)
				return &List{Items: items, Tuple: x.Tuple && y.Tuple}, nil
			}
		}
	case "*":
		rep := func(v Value, n Value) (Value, bool, error) {
			cnt, ok := toInt(n)
			if !ok {
				return nil, false, nil
			}
			if cnt < 0 {
				cnt = 0
			}
			switch s := v.(type) {
			case string:
				if int64(len(s))*cnt > maxRepeat {
					return nil, true, fmt.Errorf("texto grande demais")
				}
				return strings.Repeat(s, int(cnt)), true, nil
			case *List:
				if int64(len(s.Items))*cnt > maxRepeat {
					return nil, true, fmt.Errorf("lista grande demais")
				}
				out := make([]Value, 0, len(s.Items)*int(cnt))
				for i := int64(0); i < cnt; i++ {
					out = append(out, s.Items...)
				}
				return &List{Items: out, Tuple: s.Tuple}, true, nil
			}
			return nil, false, nil
		}
		if v, ok, err := rep(a, b); ok {
			return v, err
		}
		if v, ok, err := rep(b, a); ok {
			return v, err
		}
	}

	fa, aNum := toFloat(a)
	fb, bNum := toFloat(b)
	if !aNum || !bNum {
		return nil, fmt.Errorf("não dá para fazer %s %s %s", typeName(a), op, typeName(b))
	}
	ia, aInt := toInt(a)
	ib, bInt := toInt(b)
	if aInt && bInt {
		switch op {
		case "+":
			return ia + ib, nil
		case "-":
			return ia - ib, nil
		case "*":
			return ia * ib, nil
		case "/":
			if ib == 0 {
				return nil, errors.New("divisão por zero")
			}
			return fa / fb, nil
		case "//":
			if ib == 0 {
				return nil, errors.New("divisão por zero")
			}
			q := ia / ib
			if ia%ib != 0 && (ia < 0) != (ib < 0) {
				q--
			}
			return q, nil
		case "%":
			if ib == 0 {
				return nil, errors.New("divisão por zero")
			}
			r := ia % ib
			if r != 0 && (r < 0) != (ib < 0) {
				r += ib
			}
			return r, nil
		case "**":
			if ib >= 0 {
				if v, ok := ipow(ia, ib); ok {
					return v, nil
				}
			}
			return math.Pow(fa, fb), nil
		}
	}
	switch op {
	case "+":
		return fa + fb, nil
	case "-":
		return fa - fb, nil
	case "*":
		return fa * fb, nil
	case "/":
		if fb == 0 {
			return nil, errors.New("divisão por zero")
		}
		return fa / fb, nil
	case "//":
		if fb == 0 {
			return nil, errors.New("divisão por zero")
		}
		return math.Floor(fa / fb), nil
	case "%":
		if fb == 0 {
			return nil, errors.New("divisão por zero")
		}
		r := math.Mod(fa, fb)
		if r != 0 && (r < 0) != (fb < 0) {
			r += fb
		}
		return r, nil
	case "**":
		return math.Pow(fa, fb), nil
	}
	return nil, fmt.Errorf("operador desconhecido: %s", op)
}

func (in *Interp) cmpOp(op string, a, b Value) (bool, error) {
	switch op {
	case "==":
		return equal(a, b), nil
	case "!=":
		return !equal(a, b), nil
	case "<", "<=", ">", ">=":
		c, err := compareValues(a, b)
		if err != nil {
			return false, err
		}
		switch op {
		case "<":
			return c < 0, nil
		case "<=":
			return c <= 0, nil
		case ">":
			return c > 0, nil
		}
		return c >= 0, nil
	case "in", "not in":
		r, err := contains(b, a)
		if op == "not in" {
			r = !r
		}
		return r, err
	case "is":
		return identical(a, b), nil
	case "is not":
		return !identical(a, b), nil
	}
	return false, fmt.Errorf("comparação desconhecida: %s", op)
}

func identical(a, b Value) bool {
	switch a.(type) {
	case nil, bool, int64, float64, string:
		return equal(a, b) && typeName(a) == typeName(b)
	}
	return a == b
}

func contains(c, item Value) (bool, error) {
	switch x := c.(type) {
	case string:
		s, ok := item.(string)
		if !ok {
			return false, fmt.Errorf("'in' com texto precisa de um texto à esquerda, não %s", typeName(item))
		}
		return strings.Contains(x, s), nil
	case *List:
		for _, it := range x.Items {
			if equal(it, item) {
				return true, nil
			}
		}
		return false, nil
	case *Dict:
		_, ok, err := x.Get(item)
		return ok, err
	}
	return false, fmt.Errorf("não dá para usar 'in' com %s", typeName(c))
}
