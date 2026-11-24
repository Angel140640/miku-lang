package lang

import (
	"bytes"
	"strings"
	"testing"
)

func run(t *testing.T, src string) (string, error) {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	in := New(&out)
	err = in.Exec(prog)
	return out.String(), err
}

func mustRun(t *testing.T, src string) string {
	t.Helper()
	out, err := run(t, src)
	if err != nil {
		t.Fatalf("erro inesperado: %v\ncódigo:\n%s", err, src)
	}
	return out
}

func TestBasics(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"aritmética", "print(1 + 2 * 3, 7 // 2, -7 // 2, 7 % 3, -7 % 3, 2 ** 10, 2 ** -1, 7 / 2)", "7 3 -4 1 2 1024 0.5 3.5\n"},
		{"precedência unária", "print(-2 ** 2)", "-4\n"},
		{"float", "print(1.0, 0.1 + 0.2, 1e16, 3 * 1.5)", "1.0 0.30000000000000004 1e+16 4.5\n"},
		{"strings", "print('a' + 'b', 'ab' * 3, len('olá'), 'x' in 'xyz')", "ab ababab 3 True\n"},
		{"listas", "x = [1, 2, 3]\nx.append(4)\nprint(x, x[0], x[-1], x[1:3], x[::-1], len(x))", "[1, 2, 3, 4] 1 4 [2, 3] [4, 3, 2, 1] 4\n"},
		{"dict", "d = {'a': 1}\nd['b'] = 2\nprint(d, d.get('c', 9), 'a' in d, list(d.keys()))", "{'a': 1, 'b': 2} 9 True ['a', 'b']\n"},
		{"tupla", "a, b = 1, 2\na, b = b, a\nprint(a, b, (1,), ())", "2 1 (1,) ()\n"},
		{"if/elif/else", "x = 5\nif x < 3:\n    print('a')\nelif x < 10:\n    print('b')\nelse:\n    print('c')", "b\n"},
		{"while/break/continue", "i = 0\ns = 0\nwhile True:\n    i += 1\n    if i % 2 == 0:\n        continue\n    if i > 7:\n        break\n    s += i\nprint(s)", "16\n"},
		{"for range", "t = 0\nfor i in range(5):\n    t += i\nprint(t)", "10\n"},
		{"for dict items", "d = {'a': 1, 'b': 2}\nfor k, v in d.items():\n    print(k, v)", "a 1\nb 2\n"},
		{"função", "def f(a, b=2, *rest):\n    return a + b + len(rest)\nprint(f(1), f(1, 5), f(1, 5, 9, 9), f(b=10, a=1))", "3 6 8 11\n"},
		{"recursão", "def fib(n):\n    return n if n < 2 else fib(n - 1) + fib(n - 2)\nprint(fib(15))", "610\n"},
		{"lambda e sorted", "print(sorted([3, 1, 2], key=lambda x: -x), sorted(['b', 'a']))", "[3, 2, 1] ['a', 'b']\n"},
		{"compreensão", "print([x * x for x in range(6) if x % 2 == 0])", "[0, 4, 16]\n"},
		{"fstring", "n = 3.14159\nname = 'Miku'\nprint(f'{name} {n:.2f} {n:8.1f}| {1234567:,} {5:03d} {name!r} {{ok}}')", "Miku 3.14      3.1| 1,234,567 005 'Miku' {ok}\n"},
		{"fstring expr", "d = {'a': [1, 2]}\nprint(f\"{d['a'][1] + 1} {len(d)} {'x' if d else 'y'}\")", "3 1 x\n"},
		{"format", "print('{} e {}'.format('a', 'b'), '{0}{0}'.format('x'), '{n:>4}'.format(n=7))", "a e b xx    7\n"},
		{"métodos str", "print('a,b,c'.split(','), ' x '.strip(), 'abc'.upper(), '-'.join(['a', 'b']), 'Olá mundo'.title())", "['a', 'b', 'c'] x ABC a-b Olá Mundo\n"},
		{"global", "n = 0\ndef inc():\n    global n\n    n += 1\ninc()\ninc()\nprint(n)", "2\n"},
		{"escopo local", "n = 1\ndef f():\n    n = 5\n    return n\nprint(f(), n)", "5 1\n"},
		{"try/except", "try:\n    x = 1 / 0\nexcept Exception as e:\n    print('erro:', e)", "erro: divisão por zero\n"},
		{"raise", "try:\n    raise 'boom'\nexcept as e:\n    print(e)\nfinally:\n    print('fim')", "boom\nfim\n"},
		{"comparação encadeada", "print(1 < 2 < 3, 1 < 3 < 2, 1 == 1.0, None is None, 3 not in [1, 2])", "True False True True True\n"},
		{"and/or", "print(0 or 'x', 1 and 2, None and 1, not 0)", "x 2 None True\n"},
		{"in-place +=", "a = [1]\nb = a\na += [2]\nprint(b)", "[1, 2]\n"},
		{"del", "d = {'a': 1, 'b': 2}\ndel d['a']\nx = [1, 2, 3]\ndel x[0]\nprint(d, x)", "{'b': 2} [2, 3]\n"},
		{"min/max/sum", "print(min(3, 1, 2), max([4, 9, 2]), sum([1, 2, 3]), abs(-3), round(2.5), round(3.14159, 2))", "1 9 6 3 2 3.14\n"},
		{"enumerate/zip", "print(enumerate(['a', 'b']), zip([1, 2], ['x', 'y']))", "[(0, 'a'), (1, 'b')] [(1, 'x'), (2, 'y')]\n"},
		{"json", "s = to_json({'a': [1, 2.5, 'x', None, True]})\nprint(s, from_json(s)['a'][1])", "{\"a\":[1,2.5,\"x\",null,true]} 2.5\n"},
		{"await ignorado", "def f():\n    return 1\nprint(await f())", "1\n"},
		{"comentários e linhas em branco", "# oi\n\nx = 1  # fim\n\n\nprint(x)\n", "1\n"},
		{"multilinha em colchetes", "x = [\n    1,\n    2,\n]\nprint(x)", "[1, 2]\n"},
		{"string tripla", "s = '''a\nb'''\nprint(len(s))", "3\n"},
		{"escapes", "print('a\\tb', len('\\n'), r'\\n')", "a\tb 1 \\n\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mustRun(t, c.src); got != c.want {
				t.Errorf("\n got: %q\nwant: %q", got, c.want)
			}
		})
	}
}

func TestErrors(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"variável inexistente", "print(x)", "a variável 'x' não existe"},
		{"tipos", "print(1 + 'a')", "não dá para fazer int + str"},
		{"índice", "x = [1]\nprint(x[5])", "fora do alcance"},
		{"chave", "d = {}\nprint(d['k'])", "não existe no dicionário"},
		{"args faltando", "def f(a):\n    pass\nf()", "precisa do argumento 'a'"},
		{"args demais", "def f():\n    pass\nf(1)", "a mais"},
		{"recursão infinita", "def f():\n    return f()\nf()", "aninhadas demais"},
		{"indentação", "if True:\nprint(1)", "bloco indentado"},
		{"texto aberto", "x = 'abc", "sem aspas"},
		{"palavra reservada", "x = else", "não pode ser usado aqui"},
		{"atribuição inválida", "1 = 2", "não dá para atribuir"},
		{"fstring aberta", "print(f'{x')", "'{' sem '}'"},
		{"reply fora de handler", "reply 'oi'", "só funciona dentro de"},
		{"attr inexistente", "x = 'a'\nx.foo()", "não tem o atributo 'foo'"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := run(t, c.src)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("erro = %v, queria conter %q", err, c.want)
			}
		})
	}
}

func TestErrorLineNumbers(t *testing.T) {
	_, err := run(t, "x = 1\ny = 2\nprint(z)\n")
	if ErrorLine(err) != 3 {
		t.Errorf("linha = %d, queria 3 (%v)", ErrorLine(err), err)
	}
	_, err = run(t, "def f():\n    return 1 / 0\n\nx = 1\nf()\n")
	if ErrorLine(err) != 2 {
		t.Errorf("linha = %d, queria 2 (%v)", ErrorLine(err), err)
	}
	_, err = Parse("x = 1\nif x\n    pass\n")
	if ErrorLine(err) != 2 {
		t.Errorf("linha de sintaxe = %d, queria 2 (%v)", ErrorLine(err), err)
	}
}

// diretivas de bot que não são palavras reservadas: continuam funcionando como nomes comuns
func TestDirectiveWordsAsNames(t *testing.T) {
	got := mustRun(t, "on = 1\ncommand = 2\ntoken = 3\nprefix = 4\nsend = 5\nreply = 6\nprint(on + command + token + prefix + send + reply)\n")
	if got != "21\n" {
		t.Errorf("got %q", got)
	}
}

type recHost struct{ got []string }

func (h *recHost) Declare(in *Interp, s Stmt, env *Env) error {
	switch d := s.(type) {
	case *CommandDecl:
		h.got = append(h.got, "command:"+d.Name+":"+strings.Join(d.Aliases, ","))
	case *OnDecl:
		h.got = append(h.got, "on:"+d.Event)
	case *SlashDecl:
		h.got = append(h.got, "slash:"+d.Name+":"+d.Desc)
	case *TokenDecl:
		v, err := in.Eval(d.Val, env)
		if err != nil {
			return err
		}
		h.got = append(h.got, "token:"+Str(v))
	case *PrefixDecl:
		h.got = append(h.got, "prefix")
	case *IntentsDecl:
		h.got = append(h.got, "intents:"+strings.Join(d.Names, ","))
	case *ShardsDecl:
		h.got = append(h.got, "shards")
	case *StatusDecl, *ActivityDecl:
		h.got = append(h.got, "presence")
	}
	return nil
}

func TestDirectives(t *testing.T) {
	src := `token "abc"
prefix "!"
intents members, presences
shards auto
status dnd
activity playing "Miku"

on ready:
    print("ok")

command ping: reply "pong"

command oi aliases ola, hello:
    reply "oi"

command soma(a: int, b: int = 2, *, resto):
    reply a + b

slash dado "Rola um dado" (lados: int = 6):
    reply "x"

slash meu-slash:
    reply "y"
`
	prog, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	h := &recHost{}
	in := New(nil)
	in.Host = h
	if err := in.Exec(prog); err != nil {
		t.Fatal(err)
	}
	want := "token:abc prefix intents:members,presences shards presence presence on:ready command:ping: command:oi:ola,hello command:soma: slash:dado:Rola um dado slash:meu-slash:sem descrição"
	if got := strings.Join(h.got, " "); got != want {
		t.Errorf("\n got: %s\nwant: %s", got, want)
	}
	// parâmetros do comando soma
	for _, s := range prog.Stmts {
		if c, ok := s.(*CommandDecl); ok && c.Name == "soma" {
			if len(c.Params) != 3 || c.Params[0].Type != "int" || c.Params[1].Default == nil || c.Params[2].Kind != PRest {
				t.Errorf("params errados: %+v", c.Params)
			}
		}
	}
}

func TestSayReplyMultiline(t *testing.T) {
	src := "command x:\n    reply \"a\", [\n        1,\n        2,\n    ]\n    send f\"{1 + 1}\"\n"
	prog, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	cmd := prog.Stmts[0].(*CommandDecl)
	if len(cmd.Body) != 2 {
		t.Fatalf("corpo com %d comandos", len(cmd.Body))
	}
	if s, ok := cmd.Body[0].(*Say); !ok || s.Kind != "reply" || len(s.Args) != 2 {
		t.Errorf("primeiro comando errado: %#v", cmd.Body[0])
	}
}

func TestSayKeywordArgs(t *testing.T) {
	prog, err := Parse("command x:\n    reply \"a\", \"b\", ephemeral=True, embed=f(1)\n")
	if err != nil {
		t.Fatal(err)
	}
	s := prog.Stmts[0].(*CommandDecl).Body[0].(*Say)
	if len(s.Args) != 2 || len(s.KwNames) != 2 || s.KwNames[0] != "ephemeral" || s.KwNames[1] != "embed" {
		t.Errorf("say errado: %+v", s)
	}
	if _, err := Parse("command x:\n    reply a=1, \"b\"\n"); err == nil || !strings.Contains(err.Error(), "posicional depois de nomeado") && !strings.Contains(err.Error(), "posicional depois") {
		t.Errorf("devia recusar posicional depois de nomeado: %v", err)
	}
}
