package lang

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func bi(name string, f func(a []Value) (Value, error)) *Builtin {
	return &Builtin{Name: name, Fn: func(_ *Interp, a []Value, _ map[string]Value) (Value, error) { return f(a) }}
}

func biK(name string, f BuiltinFn) *Builtin { return &Builtin{Name: name, Fn: f} }

func needArgs(name string, a []Value, min, max int) error {
	if len(a) >= min && (max < 0 || len(a) <= max) {
		return nil
	}
	want := fmt.Sprint(min)
	switch {
	case max < 0:
		want = fmt.Sprintf("pelo menos %d", min)
	case max != min:
		want = fmt.Sprintf("de %d a %d", min, max)
	}
	return fmt.Errorf("%s() espera %s argumento(s), mas recebeu %d", name, want, len(a))
}

func asStr(name string, v Value) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s() precisa de um texto, não %s", name, typeName(v))
	}
	return s, nil
}

func asInt(name string, v Value) (int64, error) {
	i, ok := toInt(v)
	if !ok {
		return 0, fmt.Errorf("%s() precisa de um número inteiro, não %s", name, typeName(v))
	}
	return i, nil
}

// ---------------- JSON ----------------

// ToGo converte um valor da linguagem para Go puro (map, slice, string...), p/ JSON.
func ToGo(v Value) (any, error) {
	switch x := v.(type) {
	case nil, bool, int64, float64, string:
		return x, nil
	case *List:
		out := make([]any, len(x.Items))
		for i, it := range x.Items {
			g, err := ToGo(it)
			if err != nil {
				return nil, err
			}
			out[i] = g
		}
		return out, nil
	case *Dict:
		out := make(map[string]any, x.Len())
		for _, k := range x.keys {
			hk, _ := hashKey(k)
			g, err := ToGo(x.m[hk])
			if err != nil {
				return nil, err
			}
			out[Str(k)] = g
		}
		return out, nil
	case *Object:
		if x.Native != nil {
			return x.Native, nil
		}
	}
	return nil, fmt.Errorf("não dá para converter %s em JSON", typeName(v))
}

// FromGo converte o resultado de encoding/json (map[string]any, []any, float64...) em valores da linguagem.
func FromGo(v any) Value {
	switch x := v.(type) {
	case nil, bool, string:
		return x
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return int64(x)
		}
		return x
	case int:
		return int64(x)
	case int64:
		return x
	case []any:
		items := make([]Value, len(x))
		for i, it := range x {
			items[i] = FromGo(it)
		}
		return &List{Items: items}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		d := NewDict()
		for _, k := range keys {
			d.SetStr(k, FromGo(x[k]))
		}
		return d
	}
	return fmt.Sprint(v)
}

func decodeVal(dec *json.Decoder) (Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			d := NewDict()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				v, err := decodeVal(dec)
				if err != nil {
					return nil, err
				}
				d.SetStr(kt.(string), v)
			}
			_, err := dec.Token()
			return d, err
		}
		l := &List{Items: []Value{}}
		for dec.More() {
			v, err := decodeVal(dec)
			if err != nil {
				return nil, err
			}
			l.Items = append(l.Items, v)
		}
		_, err := dec.Token()
		return l, err
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i, nil
		}
		f, _ := t.Float64()
		return f, nil
	}
	return tok, nil // string, bool, nil
}

// ParseJSON lê JSON preservando a ordem das chaves e a diferença entre int e float.
func ParseJSON(s string) (Value, error) { return fromJSONText(s) }

func fromJSONText(s string) (Value, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	v, err := decodeVal(dec)
	if err != nil {
		return nil, fmt.Errorf("JSON inválido: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("JSON inválido: sobrou texto depois do valor")
	}
	return v, nil
}

// ---------------- funções embutidas ----------------

func sortValues(in *Interp, items []Value, key Value, reverse bool) error {
	keys := items
	if key != nil {
		keys = make([]Value, len(items))
		for i, it := range items {
			k, err := in.Call(key, []Value{it}, nil)
			if err != nil {
				return err
			}
			keys[i] = k
		}
	}
	idx := make([]int, len(items))
	for i := range idx {
		idx[i] = i
	}
	var cmpErr error
	sort.SliceStable(idx, func(a, b int) bool {
		c, err := compareValues(keys[idx[a]], keys[idx[b]])
		if err != nil && cmpErr == nil {
			cmpErr = err
		}
		if reverse {
			return c > 0
		}
		return c < 0
	})
	if cmpErr != nil {
		return cmpErr
	}
	sorted := make([]Value, len(items))
	for i, j := range idx {
		sorted[i] = items[j]
	}
	copy(items, sorted)
	return nil
}

func registerBuiltins(in *Interp) {
	def := func(name string, f func(a []Value) (Value, error)) { in.Define(name, bi(name, f)) }
	defK := func(name string, f BuiltinFn) { in.Define(name, biK(name, f)) }
	items := func(name string, v Value) ([]Value, error) {
		it, err := in.iterate(v, 0)
		if err != nil {
			return nil, fmt.Errorf("%s() precisa de algo que se possa percorrer, não %s", name, typeName(v))
		}
		return append([]Value(nil), it...), nil
	}

	defK("print", func(in *Interp, a []Value, kw map[string]Value) (Value, error) {
		sep, end := " ", "\n"
		if v, ok := kw["sep"]; ok && v != nil {
			sep = Str(v)
		}
		if v, ok := kw["end"]; ok && v != nil {
			end = Str(v)
		}
		parts := make([]string, len(a))
		for i, v := range a {
			parts[i] = Str(v)
		}
		fmt.Fprint(in.Out, strings.Join(parts, sep)+end)
		return nil, nil
	})

	def("len", func(a []Value) (Value, error) {
		if err := needArgs("len", a, 1, 1); err != nil {
			return nil, err
		}
		switch x := a[0].(type) {
		case string:
			return int64(utf8.RuneCountInString(x)), nil
		case *List:
			return int64(len(x.Items)), nil
		case *Dict:
			return int64(x.Len()), nil
		}
		return nil, fmt.Errorf("len() não funciona com %s", typeName(a[0]))
	})

	def("str", func(a []Value) (Value, error) {
		if err := needArgs("str", a, 0, 1); err != nil {
			return nil, err
		}
		if len(a) == 0 {
			return "", nil
		}
		return Str(a[0]), nil
	})

	def("int", func(a []Value) (Value, error) {
		if err := needArgs("int", a, 0, 1); err != nil {
			return nil, err
		}
		if len(a) == 0 {
			return int64(0), nil
		}
		switch x := a[0].(type) {
		case int64:
			return x, nil
		case bool:
			if x {
				return int64(1), nil
			}
			return int64(0), nil
		case float64:
			if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) >= 9.2e18 {
				return nil, errors.New("int(): número fora do alcance")
			}
			return int64(x), nil
		case string:
			s := strings.ReplaceAll(strings.TrimSpace(x), "_", "")
			i, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("não consegui converter %s para int", quote(x))
			}
			return i, nil
		}
		return nil, fmt.Errorf("int() não funciona com %s", typeName(a[0]))
	})

	def("float", func(a []Value) (Value, error) {
		if err := needArgs("float", a, 0, 1); err != nil {
			return nil, err
		}
		if len(a) == 0 {
			return 0.0, nil
		}
		if s, ok := a[0].(string); ok {
			f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), "_", ""), 64)
			if err != nil {
				return nil, fmt.Errorf("não consegui converter %s para float", quote(s))
			}
			return f, nil
		}
		if f, ok := toFloat(a[0]); ok {
			return f, nil
		}
		return nil, fmt.Errorf("float() não funciona com %s", typeName(a[0]))
	})

	def("bool", func(a []Value) (Value, error) {
		if err := needArgs("bool", a, 0, 1); err != nil {
			return nil, err
		}
		return len(a) == 1 && truthy(a[0]), nil
	})

	def("list", func(a []Value) (Value, error) {
		if err := needArgs("list", a, 0, 1); err != nil {
			return nil, err
		}
		if len(a) == 0 {
			return &List{Items: []Value{}}, nil
		}
		it, err := items("list", a[0])
		return &List{Items: it}, err
	})

	def("tuple", func(a []Value) (Value, error) {
		if err := needArgs("tuple", a, 0, 1); err != nil {
			return nil, err
		}
		if len(a) == 0 {
			return &List{Items: []Value{}, Tuple: true}, nil
		}
		it, err := items("tuple", a[0])
		return &List{Items: it, Tuple: true}, err
	})

	defK("dict", func(in *Interp, a []Value, kw map[string]Value) (Value, error) {
		if err := needArgs("dict", a, 0, 1); err != nil {
			return nil, err
		}
		d := NewDict()
		if len(a) == 1 {
			if src, ok := a[0].(*Dict); ok {
				for _, k := range src.keys {
					hk, _ := hashKey(k)
					_ = d.Set(k, src.m[hk])
				}
			} else {
				it, err := items("dict", a[0])
				if err != nil {
					return nil, err
				}
				for _, pair := range it {
					p, ok := pair.(*List)
					if !ok || len(p.Items) != 2 {
						return nil, errors.New("dict() espera uma lista de pares [chave, valor]")
					}
					if err := d.Set(p.Items[0], p.Items[1]); err != nil {
						return nil, err
					}
				}
			}
		}
		keys := make([]string, 0, len(kw))
		for k := range kw {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			d.SetStr(k, kw[k])
		}
		return d, nil
	})

	def("range", func(a []Value) (Value, error) {
		if err := needArgs("range", a, 1, 3); err != nil {
			return nil, err
		}
		var n [3]int64
		n[2] = 1
		for i, v := range a {
			x, err := asInt("range", v)
			if err != nil {
				return nil, err
			}
			n[i] = x
		}
		start, stop, step := n[0], n[1], n[2]
		if len(a) == 1 {
			start, stop = 0, n[0]
		}
		if step == 0 {
			return nil, errors.New("range(): o passo não pode ser 0")
		}
		count := int64(0)
		if step > 0 && stop > start {
			count = (stop - start + step - 1) / step
		} else if step < 0 && start > stop {
			count = (start - stop - step - 1) / -step
		}
		if count > 10_000_000 {
			return nil, errors.New("range(): intervalo grande demais")
		}
		out := make([]Value, 0, count)
		for i := int64(0); i < count; i++ {
			out = append(out, start+i*step)
		}
		return &List{Items: out}, nil
	})

	minmax := func(name string, wantMax bool) {
		defK(name, func(in *Interp, a []Value, kw map[string]Value) (Value, error) {
			var vals []Value
			if len(a) == 1 {
				it, err := items(name, a[0])
				if err != nil {
					return nil, err
				}
				vals = it
			} else {
				vals = a
			}
			if len(vals) == 0 {
				return nil, fmt.Errorf("%s() não tem valores para comparar", name)
			}
			key := kw["key"]
			best, bestKey := vals[0], vals[0]
			if key != nil {
				k, err := in.Call(key, []Value{best}, nil)
				if err != nil {
					return nil, err
				}
				bestKey = k
			}
			for _, v := range vals[1:] {
				k := v
				if key != nil {
					var err error
					if k, err = in.Call(key, []Value{v}, nil); err != nil {
						return nil, err
					}
				}
				c, err := compareValues(k, bestKey)
				if err != nil {
					return nil, err
				}
				if (wantMax && c > 0) || (!wantMax && c < 0) {
					best, bestKey = v, k
				}
			}
			return best, nil
		})
	}
	minmax("min", false)
	minmax("max", true)

	defK("sum", func(in *Interp, a []Value, kw map[string]Value) (Value, error) {
		if err := needArgs("sum", a, 1, 2); err != nil {
			return nil, err
		}
		it, err := items("sum", a[0])
		if err != nil {
			return nil, err
		}
		var total Value = int64(0)
		if len(a) == 2 {
			total = a[1]
		}
		for _, v := range it {
			if total, err = in.binop("+", total, v); err != nil {
				return nil, err
			}
		}
		return total, nil
	})

	def("abs", func(a []Value) (Value, error) {
		if err := needArgs("abs", a, 1, 1); err != nil {
			return nil, err
		}
		if i, ok := toInt(a[0]); ok {
			if i < 0 {
				i = -i
			}
			return i, nil
		}
		if f, ok := a[0].(float64); ok {
			return math.Abs(f), nil
		}
		return nil, fmt.Errorf("abs() não funciona com %s", typeName(a[0]))
	})

	def("round", func(a []Value) (Value, error) {
		if err := needArgs("round", a, 1, 2); err != nil {
			return nil, err
		}
		f, ok := toFloat(a[0])
		if !ok {
			return nil, fmt.Errorf("round() não funciona com %s", typeName(a[0]))
		}
		if len(a) == 1 || a[1] == nil {
			return int64(math.RoundToEven(f)), nil
		}
		nd, err := asInt("round", a[1])
		if err != nil {
			return nil, err
		}
		if _, isInt := toInt(a[0]); isInt && nd >= 0 {
			return a[0], nil
		}
		p := math.Pow(10, float64(nd))
		return math.RoundToEven(f*p) / p, nil
	})

	defK("sorted", func(in *Interp, a []Value, kw map[string]Value) (Value, error) {
		if err := needArgs("sorted", a, 1, 1); err != nil {
			return nil, err
		}
		it, err := items("sorted", a[0])
		if err != nil {
			return nil, err
		}
		if err := sortValues(in, it, kw["key"], truthy(kw["reverse"])); err != nil {
			return nil, err
		}
		return &List{Items: it}, nil
	})

	def("reversed", func(a []Value) (Value, error) {
		if err := needArgs("reversed", a, 1, 1); err != nil {
			return nil, err
		}
		it, err := items("reversed", a[0])
		if err != nil {
			return nil, err
		}
		for i, j := 0, len(it)-1; i < j; i, j = i+1, j-1 {
			it[i], it[j] = it[j], it[i]
		}
		return &List{Items: it}, nil
	})

	defK("enumerate", func(in *Interp, a []Value, kw map[string]Value) (Value, error) {
		if err := needArgs("enumerate", a, 1, 2); err != nil {
			return nil, err
		}
		it, err := items("enumerate", a[0])
		if err != nil {
			return nil, err
		}
		start := int64(0)
		if len(a) == 2 {
			if start, err = asInt("enumerate", a[1]); err != nil {
				return nil, err
			}
		} else if v, ok := kw["start"]; ok {
			if start, err = asInt("enumerate", v); err != nil {
				return nil, err
			}
		}
		out := make([]Value, len(it))
		for i, v := range it {
			out[i] = NewTuple(start+int64(i), v)
		}
		return &List{Items: out}, nil
	})

	def("zip", func(a []Value) (Value, error) {
		lists := make([][]Value, len(a))
		n := -1
		for i, v := range a {
			it, err := items("zip", v)
			if err != nil {
				return nil, err
			}
			lists[i] = it
			if n < 0 || len(it) < n {
				n = len(it)
			}
		}
		out := make([]Value, 0, max(n, 0))
		for k := 0; k < n; k++ {
			row := make([]Value, len(lists))
			for i := range lists {
				row[i] = lists[i][k]
			}
			out = append(out, &List{Items: row, Tuple: true})
		}
		return &List{Items: out}, nil
	})

	def("any", func(a []Value) (Value, error) {
		if err := needArgs("any", a, 1, 1); err != nil {
			return nil, err
		}
		it, err := items("any", a[0])
		for _, v := range it {
			if truthy(v) {
				return true, nil
			}
		}
		return false, err
	})

	def("all", func(a []Value) (Value, error) {
		if err := needArgs("all", a, 1, 1); err != nil {
			return nil, err
		}
		it, err := items("all", a[0])
		for _, v := range it {
			if !truthy(v) {
				return false, nil
			}
		}
		return true, err
	})

	def("type", func(a []Value) (Value, error) {
		if err := needArgs("type", a, 1, 1); err != nil {
			return nil, err
		}
		return typeName(a[0]), nil
	})

	def("ord", func(a []Value) (Value, error) {
		if err := needArgs("ord", a, 1, 1); err != nil {
			return nil, err
		}
		s, err := asStr("ord", a[0])
		if err != nil {
			return nil, err
		}
		r, size := utf8.DecodeRuneInString(s)
		if size == 0 || size != len(s) {
			return nil, errors.New("ord() precisa de um texto com exatamente 1 caractere")
		}
		return int64(r), nil
	})

	def("chr", func(a []Value) (Value, error) {
		if err := needArgs("chr", a, 1, 1); err != nil {
			return nil, err
		}
		i, err := asInt("chr", a[0])
		if err != nil {
			return nil, err
		}
		if i < 0 || i > unicode.MaxRune {
			return nil, errors.New("chr(): código fora do alcance")
		}
		return string(rune(i)), nil
	})

	def("divmod", func(a []Value) (Value, error) {
		if err := needArgs("divmod", a, 2, 2); err != nil {
			return nil, err
		}
		q, err := in.binop("//", a[0], a[1])
		if err != nil {
			return nil, err
		}
		r, err := in.binop("%", a[0], a[1])
		if err != nil {
			return nil, err
		}
		return NewTuple(q, r), nil
	})

	// ---- aleatório e tempo ----
	def("random", func(a []Value) (Value, error) { return rand.Float64(), nil })

	def("randint", func(a []Value) (Value, error) {
		if err := needArgs("randint", a, 2, 2); err != nil {
			return nil, err
		}
		lo, err := asInt("randint", a[0])
		if err != nil {
			return nil, err
		}
		hi, err := asInt("randint", a[1])
		if err != nil {
			return nil, err
		}
		if hi < lo {
			return nil, errors.New("randint(a, b): a não pode ser maior que b")
		}
		return lo + rand.Int63n(hi-lo+1), nil
	})

	def("choice", func(a []Value) (Value, error) {
		if err := needArgs("choice", a, 1, 1); err != nil {
			return nil, err
		}
		it, err := items("choice", a[0])
		if err != nil {
			return nil, err
		}
		if len(it) == 0 {
			return nil, errors.New("choice() não aceita lista vazia")
		}
		return it[rand.Intn(len(it))], nil
	})

	def("shuffle", func(a []Value) (Value, error) {
		if err := needArgs("shuffle", a, 1, 1); err != nil {
			return nil, err
		}
		l, ok := a[0].(*List)
		if !ok || l.Tuple {
			return nil, errors.New("shuffle() precisa de uma lista")
		}
		rand.Shuffle(len(l.Items), func(i, j int) { l.Items[i], l.Items[j] = l.Items[j], l.Items[i] })
		return nil, nil
	})

	defK("sleep", func(in *Interp, a []Value, kw map[string]Value) (Value, error) {
		if err := needArgs("sleep", a, 1, 1); err != nil {
			return nil, err
		}
		secs, ok := toFloat(a[0])
		if !ok || secs < 0 {
			return nil, errors.New("sleep() precisa de um número de segundos (>= 0)")
		}
		in.Block(func() { time.Sleep(time.Duration(secs * float64(time.Second))) })
		return nil, nil
	})

	def("now", func(a []Value) (Value, error) { return float64(time.Now().UnixNano()) / 1e9, nil })

	def("env", func(a []Value) (Value, error) {
		if err := needArgs("env", a, 1, 2); err != nil {
			return nil, err
		}
		name, err := asStr("env", a[0])
		if err != nil {
			return nil, err
		}
		if v, ok := os.LookupEnv(name); ok {
			return v, nil
		}
		if len(a) == 2 {
			return a[1], nil
		}
		return nil, nil
	})

	def("Exception", func(a []Value) (Value, error) {
		if len(a) == 0 {
			return "", nil
		}
		return Str(a[0]), nil
	})

	defK("to_json", func(in *Interp, a []Value, kw map[string]Value) (Value, error) {
		if err := needArgs("to_json", a, 1, 1); err != nil {
			return nil, err
		}
		g, err := ToGo(a[0])
		if err != nil {
			return nil, err
		}
		var b []byte
		if ind, ok := toInt(kw["indent"]); ok && ind > 0 {
			b, err = json.MarshalIndent(g, "", strings.Repeat(" ", int(ind)))
		} else {
			b, err = json.Marshal(g)
		}
		return string(b), err
	})

	def("from_json", func(a []Value) (Value, error) {
		if err := needArgs("from_json", a, 1, 1); err != nil {
			return nil, err
		}
		s, err := asStr("from_json", a[0])
		if err != nil {
			return nil, err
		}
		return fromJSONText(s)
	})
}

// ---------------- métodos de texto ----------------

func titleCase(s string) string {
	var sb strings.Builder
	prevLetter := false
	for _, r := range s {
		if unicode.IsLetter(r) {
			if prevLetter {
				sb.WriteRune(unicode.ToLower(r))
			} else {
				sb.WriteRune(unicode.ToUpper(r))
			}
			prevLetter = true
		} else {
			sb.WriteRune(r)
			prevLetter = false
		}
	}
	return sb.String()
}

func allRunes(s string, f func(rune) bool) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !f(r) {
			return false
		}
	}
	return true
}

func padArgs(name string, a []Value) (int, string, error) {
	if err := needArgs(name, a, 1, 2); err != nil {
		return 0, "", err
	}
	w, err := asInt(name, a[0])
	if err != nil {
		return 0, "", err
	}
	fill := " "
	if len(a) == 2 {
		if fill, err = asStr(name, a[1]); err != nil || utf8.RuneCountInString(fill) != 1 {
			return 0, "", fmt.Errorf("%s(): o preenchimento precisa ter 1 caractere", name)
		}
	}
	return int(w), fill, nil
}

func strMethod(s string, name string) *Builtin {
	str1 := func(f func(string) Value) *Builtin {
		return bi(name, func(a []Value) (Value, error) {
			if err := needArgs(name, a, 0, 0); err != nil {
				return nil, err
			}
			return f(s), nil
		})
	}
	// métodos que recebem um texto
	withStr := func(f func(arg string) Value) *Builtin {
		return bi(name, func(a []Value) (Value, error) {
			if err := needArgs(name, a, 1, 1); err != nil {
				return nil, err
			}
			x, err := asStr(name, a[0])
			if err != nil {
				return nil, err
			}
			return f(x), nil
		})
	}
	switch name {
	case "upper":
		return str1(func(s string) Value { return strings.ToUpper(s) })
	case "lower":
		return str1(func(s string) Value { return strings.ToLower(s) })
	case "title":
		return str1(func(s string) Value { return titleCase(s) })
	case "capitalize":
		return str1(func(s string) Value {
			if s == "" {
				return s
			}
			r, size := utf8.DecodeRuneInString(s)
			return string(unicode.ToUpper(r)) + strings.ToLower(s[size:])
		})
	case "isdigit":
		return str1(func(s string) Value { return allRunes(s, unicode.IsDigit) })
	case "isalpha":
		return str1(func(s string) Value { return allRunes(s, unicode.IsLetter) })
	case "isalnum":
		return str1(func(s string) Value {
			return allRunes(s, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) })
		})
	case "isspace":
		return str1(func(s string) Value { return allRunes(s, unicode.IsSpace) })
	case "isupper":
		return str1(func(s string) Value { return s != strings.ToLower(s) && s == strings.ToUpper(s) })
	case "islower":
		return str1(func(s string) Value { return s != strings.ToUpper(s) && s == strings.ToLower(s) })
	case "splitlines":
		return str1(func(s string) Value {
			out := []Value{}
			if s == "" {
				return &List{Items: out}
			}
			for _, l := range strings.Split(strings.TrimSuffix(strings.ReplaceAll(s, "\r\n", "\n"), "\n"), "\n") {
				out = append(out, l)
			}
			return &List{Items: out}
		})
	case "strip", "lstrip", "rstrip":
		return bi(name, func(a []Value) (Value, error) {
			if err := needArgs(name, a, 0, 1); err != nil {
				return nil, err
			}
			chars := ""
			if len(a) == 1 && a[0] != nil {
				c, err := asStr(name, a[0])
				if err != nil {
					return nil, err
				}
				chars = c
			}
			trim := func(x string, f func(string, string) string, g func(string, func(rune) bool) string) string {
				if chars == "" {
					return g(x, unicode.IsSpace)
				}
				return f(x, chars)
			}
			switch name {
			case "lstrip":
				return trim(s, strings.TrimLeft, strings.TrimLeftFunc), nil
			case "rstrip":
				return trim(s, strings.TrimRight, strings.TrimRightFunc), nil
			}
			return trim(s, strings.Trim, strings.TrimFunc), nil
		})
	case "split":
		return bi(name, func(a []Value) (Value, error) {
			if err := needArgs(name, a, 0, 2); err != nil {
				return nil, err
			}
			n := -1
			if len(a) == 2 {
				m, err := asInt(name, a[1])
				if err != nil {
					return nil, err
				}
				if m >= 0 {
					n = int(m) + 1
				}
			}
			var parts []string
			if len(a) == 0 || a[0] == nil {
				parts = strings.Fields(s)
				if n > 0 && len(parts) > n {
					rest := parts[n-1:]
					parts = append(parts[:n-1:n-1], strings.Join(rest, " "))
				}
			} else {
				sep, err := asStr(name, a[0])
				if err != nil {
					return nil, err
				}
				if sep == "" {
					return nil, errors.New("split(): o separador não pode ser vazio")
				}
				parts = strings.SplitN(s, sep, n)
			}
			out := make([]Value, len(parts))
			for i, p := range parts {
				out[i] = p
			}
			return &List{Items: out}, nil
		})
	case "join":
		return biK(name, func(in *Interp, a []Value, kw map[string]Value) (Value, error) {
			if err := needArgs(name, a, 1, 1); err != nil {
				return nil, err
			}
			it, err := in.iterate(a[0], 0)
			if err != nil {
				return nil, fmt.Errorf("join() precisa de uma lista, não %s", typeName(a[0]))
			}
			parts := make([]string, len(it))
			for i, v := range it {
				p, ok := v.(string)
				if !ok {
					return nil, fmt.Errorf("join(): o item %d é %s, mas só aceita textos (use str())", i, typeName(v))
				}
				parts[i] = p
			}
			return strings.Join(parts, s), nil
		})
	case "replace":
		return bi(name, func(a []Value) (Value, error) {
			if err := needArgs(name, a, 2, 3); err != nil {
				return nil, err
			}
			old, err := asStr(name, a[0])
			if err != nil {
				return nil, err
			}
			nw, err := asStr(name, a[1])
			if err != nil {
				return nil, err
			}
			n := -1
			if len(a) == 3 {
				c, err := asInt(name, a[2])
				if err != nil {
					return nil, err
				}
				n = int(c)
			}
			return strings.Replace(s, old, nw, n), nil
		})
	case "startswith", "endswith":
		return bi(name, func(a []Value) (Value, error) {
			if err := needArgs(name, a, 1, 1); err != nil {
				return nil, err
			}
			var opts []Value
			if l, ok := a[0].(*List); ok {
				opts = l.Items
			} else {
				opts = []Value{a[0]}
			}
			for _, o := range opts {
				x, err := asStr(name, o)
				if err != nil {
					return nil, err
				}
				if (name == "startswith" && strings.HasPrefix(s, x)) || (name == "endswith" && strings.HasSuffix(s, x)) {
					return true, nil
				}
			}
			return false, nil
		})
	case "find":
		return withStr(func(x string) Value {
			i := strings.Index(s, x)
			if i < 0 {
				return int64(-1)
			}
			return int64(utf8.RuneCountInString(s[:i]))
		})
	case "count":
		return withStr(func(x string) Value {
			if x == "" {
				return int64(utf8.RuneCountInString(s) + 1)
			}
			return int64(strings.Count(s, x))
		})
	case "removeprefix":
		return withStr(func(x string) Value { return strings.TrimPrefix(s, x) })
	case "removesuffix":
		return withStr(func(x string) Value { return strings.TrimSuffix(s, x) })
	case "center", "ljust", "rjust":
		return bi(name, func(a []Value) (Value, error) {
			w, fill, err := padArgs(name, a)
			if err != nil {
				return nil, err
			}
			pad := w - utf8.RuneCountInString(s)
			if pad <= 0 {
				return s, nil
			}
			switch name {
			case "ljust":
				return s + strings.Repeat(fill, pad), nil
			case "rjust":
				return strings.Repeat(fill, pad) + s, nil
			}
			l := pad / 2
			return strings.Repeat(fill, l) + s + strings.Repeat(fill, pad-l), nil
		})
	case "zfill":
		return bi(name, func(a []Value) (Value, error) {
			if err := needArgs(name, a, 1, 1); err != nil {
				return nil, err
			}
			w, err := asInt(name, a[0])
			if err != nil {
				return nil, err
			}
			pad := int(w) - utf8.RuneCountInString(s)
			if pad <= 0 {
				return s, nil
			}
			sign := ""
			if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
				sign, s = s[:1], s[1:]
			}
			return sign + strings.Repeat("0", pad) + s, nil
		})
	case "format":
		return biK(name, func(in *Interp, a []Value, kw map[string]Value) (Value, error) {
			return strFormatResult(s, a, kw)
		})
	}
	return nil
}

func strFormatResult(tmpl string, a []Value, kw map[string]Value) (Value, error) {
	out, err := strFormat(tmpl, a, kw)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---------------- métodos de lista ----------------

func listMethod(l *List, name string) *Builtin {
	switch name {
	case "index":
		return bi(name, func(a []Value) (Value, error) {
			if err := needArgs(name, a, 1, 1); err != nil {
				return nil, err
			}
			for i, v := range l.Items {
				if equal(v, a[0]) {
					return int64(i), nil
				}
			}
			return nil, fmt.Errorf("%s não está na lista", Repr(a[0]))
		})
	case "count":
		return bi(name, func(a []Value) (Value, error) {
			if err := needArgs(name, a, 1, 1); err != nil {
				return nil, err
			}
			n := int64(0)
			for _, v := range l.Items {
				if equal(v, a[0]) {
					n++
				}
			}
			return n, nil
		})
	case "copy":
		return bi(name, func(a []Value) (Value, error) {
			return &List{Items: append([]Value(nil), l.Items...), Tuple: l.Tuple}, nil
		})
	}
	if l.Tuple {
		return nil
	}
	switch name {
	case "append":
		return bi(name, func(a []Value) (Value, error) {
			if err := needArgs(name, a, 1, 1); err != nil {
				return nil, err
			}
			l.Items = append(l.Items, a[0])
			return nil, nil
		})
	case "extend":
		return biK(name, func(in *Interp, a []Value, kw map[string]Value) (Value, error) {
			if err := needArgs(name, a, 1, 1); err != nil {
				return nil, err
			}
			it, err := in.iterate(a[0], 0)
			if err != nil {
				return nil, fmt.Errorf("extend() precisa de uma lista, não %s", typeName(a[0]))
			}
			l.Items = append(l.Items, it...)
			return nil, nil
		})
	case "pop":
		return bi(name, func(a []Value) (Value, error) {
			if err := needArgs(name, a, 0, 1); err != nil {
				return nil, err
			}
			if len(l.Items) == 0 {
				return nil, errors.New("pop() de lista vazia")
			}
			var at Value = int64(-1)
			if len(a) == 1 {
				at = a[0]
			}
			idx, err := normIndex(len(l.Items), at)
			if err != nil {
				return nil, err
			}
			v := l.Items[idx]
			l.Items = append(l.Items[:idx:idx], l.Items[idx+1:]...)
			return v, nil
		})
	case "insert":
		return bi(name, func(a []Value) (Value, error) {
			if err := needArgs(name, a, 2, 2); err != nil {
				return nil, err
			}
			i, err := asInt(name, a[0])
			if err != nil {
				return nil, err
			}
			n := int64(len(l.Items))
			if i < 0 {
				i = max(i+n, 0)
			}
			i = min(i, n)
			l.Items = append(l.Items, nil)
			copy(l.Items[i+1:], l.Items[i:])
			l.Items[i] = a[1]
			return nil, nil
		})
	case "remove":
		return bi(name, func(a []Value) (Value, error) {
			if err := needArgs(name, a, 1, 1); err != nil {
				return nil, err
			}
			for i, v := range l.Items {
				if equal(v, a[0]) {
					l.Items = append(l.Items[:i:i], l.Items[i+1:]...)
					return nil, nil
				}
			}
			return nil, fmt.Errorf("%s não está na lista", Repr(a[0]))
		})
	case "sort":
		return biK(name, func(in *Interp, a []Value, kw map[string]Value) (Value, error) {
			return nil, sortValues(in, l.Items, kw["key"], truthy(kw["reverse"]))
		})
	case "reverse":
		return bi(name, func(a []Value) (Value, error) {
			for i, j := 0, len(l.Items)-1; i < j; i, j = i+1, j-1 {
				l.Items[i], l.Items[j] = l.Items[j], l.Items[i]
			}
			return nil, nil
		})
	case "clear":
		return bi(name, func(a []Value) (Value, error) {
			l.Items = []Value{}
			return nil, nil
		})
	}
	return nil
}

// ---------------- métodos de dicionário ----------------

func dictMethod(d *Dict, name string) *Builtin {
	switch name {
	case "get":
		return bi(name, func(a []Value) (Value, error) {
			if err := needArgs(name, a, 1, 2); err != nil {
				return nil, err
			}
			v, ok, err := d.Get(a[0])
			if err != nil {
				return nil, err
			}
			if !ok {
				if len(a) == 2 {
					return a[1], nil
				}
				return nil, nil
			}
			return v, nil
		})
	case "keys":
		return bi(name, func(a []Value) (Value, error) { return &List{Items: d.Keys()}, nil })
	case "values":
		return bi(name, func(a []Value) (Value, error) {
			out := make([]Value, 0, d.Len())
			for _, k := range d.keys {
				hk, _ := hashKey(k)
				out = append(out, d.m[hk])
			}
			return &List{Items: out}, nil
		})
	case "items":
		return bi(name, func(a []Value) (Value, error) {
			out := make([]Value, 0, d.Len())
			for _, k := range d.keys {
				hk, _ := hashKey(k)
				out = append(out, NewTuple(k, d.m[hk]))
			}
			return &List{Items: out}, nil
		})
	case "pop":
		return bi(name, func(a []Value) (Value, error) {
			if err := needArgs(name, a, 1, 2); err != nil {
				return nil, err
			}
			v, ok, err := d.Get(a[0])
			if err != nil {
				return nil, err
			}
			if !ok {
				if len(a) == 2 {
					return a[1], nil
				}
				return nil, fmt.Errorf("a chave %s não existe no dicionário", Repr(a[0]))
			}
			_, err = d.Delete(a[0])
			return v, err
		})
	case "setdefault":
		return bi(name, func(a []Value) (Value, error) {
			if err := needArgs(name, a, 1, 2); err != nil {
				return nil, err
			}
			v, ok, err := d.Get(a[0])
			if err != nil {
				return nil, err
			}
			if ok {
				return v, nil
			}
			var def Value
			if len(a) == 2 {
				def = a[1]
			}
			return def, d.Set(a[0], def)
		})
	case "update":
		return biK(name, func(in *Interp, a []Value, kw map[string]Value) (Value, error) {
			if err := needArgs(name, a, 0, 1); err != nil {
				return nil, err
			}
			if len(a) == 1 {
				src, ok := a[0].(*Dict)
				if !ok {
					return nil, fmt.Errorf("update() precisa de um dicionário, não %s", typeName(a[0]))
				}
				for _, k := range src.Keys() {
					hk, _ := hashKey(k)
					if err := d.Set(k, src.m[hk]); err != nil {
						return nil, err
					}
				}
			}
			keys := make([]string, 0, len(kw))
			for k := range kw {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				d.SetStr(k, kw[k])
			}
			return nil, nil
		})
	case "copy":
		return bi(name, func(a []Value) (Value, error) {
			c := NewDict()
			for _, k := range d.keys {
				hk, _ := hashKey(k)
				_ = c.Set(k, d.m[hk])
			}
			return c, nil
		})
	case "clear":
		return bi(name, func(a []Value) (Value, error) {
			d.keys, d.m = nil, map[any]Value{}
			return nil, nil
		})
	}
	return nil
}
