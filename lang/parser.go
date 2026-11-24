package lang

import (
	"fmt"
	"strconv"
	"strings"
)

type parser struct {
	toks []Token
	p    int
}

var reserved = map[string]bool{
	"and": true, "or": true, "not": true, "in": true, "is": true, "if": true, "else": true,
	"elif": true, "for": true, "while": true, "def": true, "return": true, "break": true,
	"continue": true, "pass": true, "try": true, "except": true, "finally": true,
	"raise": true, "del": true, "global": true, "lambda": true, "as": true,
}

// palavras que, logo após uma diretiva (reply, token...), mostram que ela é só um nome comum.
var opWords = map[string]bool{"in": true, "is": true, "and": true, "or": true, "if": true, "else": true}

// Parse transforma código .mk em AST.
func Parse(src string) (prog *Program, err error) {
	toks, err := Lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(*Error); ok {
				prog, err = nil, e
				return
			}
			panic(r)
		}
	}()
	prog = &Program{}
	for p.cur().Kind != EOF {
		if p.cur().Kind == INDENT {
			p.fail(p.cur().Line, "indentação inesperada")
		}
		prog.Stmts = append(prog.Stmts, p.parseStmt())
	}
	return prog, nil
}

func (p *parser) fail(line int, format string, a ...any) {
	panic(errAt(line, format, a...))
}

func (p *parser) cur() Token { return p.toks[p.p] }

func (p *parser) peek(k int) Token {
	if p.p+k >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[p.p+k]
}

func (p *parser) next() Token {
	t := p.toks[p.p]
	if t.Kind != EOF {
		p.p++
	}
	return t
}

func (p *parser) isOp(s string) bool   { t := p.cur(); return t.Kind == OP && t.Text == s }
func (p *parser) isName(s string) bool { t := p.cur(); return t.Kind == NAME && t.Text == s }

func (p *parser) accept(op string) bool {
	if p.isOp(op) {
		p.p++
		return true
	}
	return false
}

func describe(t Token) string {
	switch t.Kind {
	case EOF:
		return "o fim do arquivo"
	case NEWLINE:
		return "o fim da linha"
	case INDENT:
		return "uma indentação"
	case DEDENT:
		return "o fim do bloco"
	case STRING, FSTRING:
		return "um texto"
	}
	return "'" + t.Text + "'"
}

func (p *parser) expect(op string) Token {
	t := p.cur()
	if t.Kind != OP || t.Text != op {
		p.fail(t.Line, "esperava '%s' mas achei %s", op, describe(t))
	}
	return p.next()
}

func (p *parser) expectName(what string) Token {
	t := p.cur()
	if t.Kind != NAME || reserved[t.Text] {
		p.fail(t.Line, "esperava %s mas achei %s", what, describe(t))
	}
	return p.next()
}

func (p *parser) atLineEnd() bool {
	k := p.cur().Kind
	return k == NEWLINE || k == EOF
}

func (p *parser) endLine() {
	switch p.cur().Kind {
	case NEWLINE:
		p.next()
	case EOF:
	default:
		p.fail(p.cur().Line, "esperava o fim da linha mas achei %s", describe(p.cur()))
	}
}

// ---------------- comandos ----------------

func (p *parser) parseBlock() []Stmt {
	if p.cur().Kind == NEWLINE {
		p.next()
		if p.cur().Kind != INDENT {
			p.fail(p.cur().Line, "esperava um bloco indentado")
		}
		p.next()
		var body []Stmt
		for p.cur().Kind != DEDENT && p.cur().Kind != EOF {
			body = append(body, p.parseStmt())
		}
		if p.cur().Kind == DEDENT {
			p.next()
		}
		return body
	}
	s := p.parseSimple()
	p.endLine()
	return []Stmt{s}
}

func (p *parser) nextStartsDirective() bool {
	n := p.peek(1)
	return n.Kind == NAME && !opWords[n.Text]
}

func (p *parser) parseStmt() Stmt {
	t := p.cur()
	if t.Kind == NAME {
		switch t.Text {
		case "if":
			return p.parseIf()
		case "while":
			p.next()
			cond := p.parseTernary()
			p.expect(":")
			return &While{st{t.Line}, cond, p.parseBlock()}
		case "for":
			p.next()
			target := p.parseTargets()
			if !p.isName("in") {
				p.fail(p.cur().Line, "esperava 'in' no for mas achei %s", describe(p.cur()))
			}
			p.next()
			iter := p.parseExprList()
			p.expect(":")
			return &For{st{t.Line}, target, iter, p.parseBlock()}
		case "def":
			p.next()
			name := p.expectName("o nome da função").Text
			params := p.parseParams()
			p.expect(":")
			return &Def{st{t.Line}, name, params, p.parseBlock()}
		case "try":
			return p.parseTry()
		case "command":
			if p.nextStartsDirective() {
				return p.parseCommand()
			}
		case "on":
			if p.nextStartsDirective() {
				return p.parseOn()
			}
		case "slash":
			if p.nextStartsDirective() {
				return p.parseSlash()
			}
		}
	}
	s := p.parseSimple()
	p.endLine()
	return s
}

func (p *parser) parseIf() Stmt {
	t := p.next() // if / elif
	cond := p.parseTernary()
	p.expect(":")
	then := p.parseBlock()
	var els []Stmt
	if p.isName("elif") {
		els = []Stmt{p.parseIf()}
	} else if p.isName("else") {
		p.next()
		p.expect(":")
		els = p.parseBlock()
	}
	return &If{st{t.Line}, cond, then, els}
}

func (p *parser) parseTry() Stmt {
	t := p.next()
	p.expect(":")
	node := &Try{st: st{t.Line}, Body: p.parseBlock()}
	if p.isName("except") {
		p.next()
		node.HasExcept = true
		if p.cur().Kind == NAME && !p.isName("as") {
			p.next() // classe da exceção (ignorada: pega qualquer erro)
		}
		if p.isName("as") {
			p.next()
			node.ExcName = p.expectName("o nome da variável do erro").Text
		}
		p.expect(":")
		node.Except = p.parseBlock()
	}
	if p.isName("finally") {
		p.next()
		p.expect(":")
		node.Finally = p.parseBlock()
		if node.Finally == nil {
			node.Finally = []Stmt{}
		}
	}
	if !node.HasExcept && node.Finally == nil {
		p.fail(t.Line, "try precisa de except ou finally")
	}
	return node
}

func (p *parser) parseCommand() Stmt {
	t := p.next()
	name := p.expectName("o nome do comando").Text
	d := &CommandDecl{st: st{t.Line}, Name: name}
	if p.isOp("(") {
		d.Params = p.parseParams()
	}
	for p.isName("aliases") || p.isName("requires") {
		if p.next().Text == "requires" {
			d.Requires = p.parseNameList("uma permissão")
		} else {
			d.Aliases = p.parseNameList("um apelido (alias)")
		}
	}
	p.expect(":")
	d.Body = p.parseBlock()
	return d
}

func (p *parser) parseOn() Stmt {
	t := p.next()
	d := &OnDecl{st: st{t.Line}, Event: p.expectName("o nome do evento").Text}
	if p.isOp("(") {
		d.Params = p.parseParams()
		d.HasParams = true
	}
	p.expect(":")
	d.Body = p.parseBlock()
	return d
}

func (p *parser) parseSlash() Stmt {
	t := p.next()
	name := p.expectName("o nome do slash command").Text
	for p.isOp("-") && p.peek(1).Kind == NAME {
		p.next()
		name += "-" + p.next().Text
	}
	d := &SlashDecl{st: st{t.Line}, Name: name, Desc: "sem descrição"}
	if p.cur().Kind == STRING {
		d.Desc = p.next().Text
	}
	if p.isOp("(") {
		d.Params = p.parseParams()
	}
	if p.isName("requires") {
		p.next()
		d.Requires = p.parseNameList("uma permissão")
	}
	p.expect(":")
	d.Body = p.parseBlock()
	return d
}

func (p *parser) parseNameList(what string) []string {
	var out []string
	for {
		out = append(out, p.expectName(what).Text)
		if !p.accept(",") {
			return out
		}
	}
}

// parseParams lê "(a, b: int, c=1, *resto)" ou "(*, texto)".
func (p *parser) parseParams() []Param {
	p.expect("(")
	var ps []Param
	for !p.isOp(")") {
		kind := PNormal
		if p.accept("*") {
			kind = PVar
			if p.accept(",") {
				kind = PRest
			}
		}
		prm := Param{Name: p.expectName("o nome do parâmetro").Text, Kind: kind}
		if p.accept(":") {
			prm.Type = p.expectName("o tipo do parâmetro").Text
		}
		if kind == PNormal && p.accept("=") {
			prm.Default = p.parseTernary()
		}
		ps = append(ps, prm)
		if !p.accept(",") {
			break
		}
	}
	p.expect(")")
	return ps
}

func (p *parser) startsValue() bool {
	n := p.peek(1)
	switch n.Kind {
	case NAME:
		return !opWords[n.Text]
	case INT, FLOAT, STRING, FSTRING:
		return true
	case OP:
		return n.Text == "(" || n.Text == "[" || n.Text == "{" || n.Text == "-" || n.Text == "+"
	}
	return false
}

func isTarget(e Expr) bool {
	switch x := e.(type) {
	case *Name, *Attr, *Index:
		return true
	case *TupleLit:
		for _, el := range x.Elems {
			if !isTarget(el) {
				return false
			}
		}
		return true
	case *ListLit:
		for _, el := range x.Elems {
			if !isTarget(el) {
				return false
			}
		}
		return true
	}
	return false
}

func (p *parser) parseSimple() Stmt {
	t := p.cur()
	if t.Kind == NAME {
		switch t.Text {
		case "pass":
			p.next()
			return &Pass{st{t.Line}}
		case "break":
			p.next()
			return &Break{st{t.Line}}
		case "continue":
			p.next()
			return &Continue{st{t.Line}}
		case "return":
			p.next()
			var v Expr
			if !p.atLineEnd() {
				v = p.parseExprList()
			}
			return &Return{st{t.Line}, v}
		case "global":
			p.next()
			g := &Global{st: st{t.Line}}
			for {
				g.Names = append(g.Names, p.expectName("um nome de variável").Text)
				if !p.accept(",") {
					break
				}
			}
			return g
		case "raise":
			p.next()
			var v Expr
			if !p.atLineEnd() {
				v = p.parseTernary()
			}
			return &Raise{st{t.Line}, v}
		case "del":
			p.next()
			target := p.parsePostfix()
			if _, ok := target.(*Name); !ok {
				if _, ok := target.(*Index); !ok {
					p.fail(t.Line, "del só aceita variável ou item (x[i])")
				}
			}
			return &Del{st{t.Line}, target}
		case "token", "prefix", "status":
			if p.startsValue() {
				p.next()
				if t.Text == "status" && p.cur().Kind == NAME {
					switch p.cur().Text {
					case "online", "idle", "dnd", "invisible":
						if n := p.peek(1); n.Kind == NEWLINE || n.Kind == EOF {
							w := p.next()
							return &StatusDecl{st{t.Line}, &Const{ex{t.Line}, w.Text}}
						}
					}
				}
				v := p.parseTernary()
				switch t.Text {
				case "token":
					return &TokenDecl{st{t.Line}, v}
				case "prefix":
					return &PrefixDecl{st{t.Line}, v}
				}
				return &StatusDecl{st{t.Line}, v}
			}
		case "intents":
			if p.nextStartsDirective() {
				p.next()
				d := &IntentsDecl{st: st{t.Line}}
				for {
					d.Names = append(d.Names, p.expectName("o nome de uma intent").Text)
					if !p.accept(",") {
						break
					}
				}
				return d
			}
		case "shards":
			if n := p.peek(1); (n.Kind == NAME && n.Text == "auto") || n.Kind == INT {
				p.next()
				w := p.next()
				count := 0
				if w.Kind == INT {
					count, _ = strconv.Atoi(w.Text)
					if count < 1 {
						p.fail(t.Line, "shards precisa de um número maior que 0 (ou 'auto')")
					}
				}
				return &ShardsDecl{st{t.Line}, count}
			}
		case "activity":
			if n := p.peek(1); n.Kind == NAME {
				switch n.Text {
				case "playing", "listening", "watching", "competing":
					p.next()
					kind := p.next().Text
					return &ActivityDecl{st{t.Line}, kind, p.parseTernary()}
				}
			}
		case "reply", "send":
			if p.startsValue() {
				p.next()
				say := &Say{st: st{t.Line}, Kind: t.Text}
				for {
					if p.cur().Kind == NAME && p.peek(1).Kind == OP && p.peek(1).Text == "=" {
						say.KwNames = append(say.KwNames, p.next().Text)
						p.next()
						say.KwVals = append(say.KwVals, p.parseTernary())
					} else {
						if len(say.KwNames) > 0 {
							p.fail(p.cur().Line, "valor posicional depois de argumento nomeado")
						}
						say.Args = append(say.Args, p.parseTernary())
					}
					if !p.accept(",") || p.atLineEnd() {
						break
					}
				}
				return say
			}
		}
	}

	e := p.parseExprList()
	if p.cur().Kind == OP {
		switch op := p.cur().Text; op {
		case "=":
			targets := []Expr{e}
			var val Expr
			for p.accept("=") {
				val = p.parseExprList()
				if p.isOp("=") {
					targets = append(targets, val)
				}
			}
			for _, tg := range targets {
				if !isTarget(tg) {
					p.fail(tg.Pos(), "não dá para atribuir a isso")
				}
			}
			return &Assign{st{t.Line}, targets, val}
		case "+=", "-=", "*=", "/=", "//=", "%=", "**=":
			p.next()
			switch e.(type) {
			case *Name, *Attr, *Index:
			default:
				p.fail(t.Line, "não dá para usar %s nisso", op)
			}
			return &AugAssign{st{t.Line}, e, strings.TrimSuffix(op, "="), p.parseExprList()}
		}
	}
	return &ExprStmt{st{t.Line}, e}
}

// ---------------- expressões ----------------

func (p *parser) parseTargets() Expr {
	ln := p.cur().Line
	var elems []Expr
	for {
		elems = append(elems, p.parsePostfix())
		if !p.accept(",") || p.isName("in") {
			break
		}
	}
	for _, e := range elems {
		if !isTarget(e) {
			p.fail(ln, "alvo inválido no for")
		}
	}
	if len(elems) == 1 {
		return elems[0]
	}
	return &TupleLit{ex{ln}, elems}
}

func (p *parser) canStartExpr() bool {
	t := p.cur()
	switch t.Kind {
	case NAME:
		return !opWords[t.Text] || t.Text == "not"
	case INT, FLOAT, STRING, FSTRING:
		return true
	case OP:
		return t.Text == "(" || t.Text == "[" || t.Text == "{" || t.Text == "-" || t.Text == "+"
	}
	return false
}

func (p *parser) parseExprList() Expr {
	ln := p.cur().Line
	first := p.parseTernary()
	if !p.isOp(",") {
		return first
	}
	elems := []Expr{first}
	for p.accept(",") {
		if !p.canStartExpr() {
			break
		}
		elems = append(elems, p.parseTernary())
	}
	return &TupleLit{ex{ln}, elems}
}

func (p *parser) parseTernary() Expr {
	if p.isName("lambda") {
		return p.parseLambda()
	}
	x := p.parseOr()
	if p.isName("if") {
		t := p.next()
		cond := p.parseOr()
		if !p.isName("else") {
			p.fail(t.Line, "esperava 'else' na expressão condicional (a if cond else b)")
		}
		p.next()
		return &IfExp{ex{t.Line}, cond, x, p.parseTernary()}
	}
	return x
}

func (p *parser) parseLambda() Expr {
	t := p.next()
	var ps []Param
	for !p.isOp(":") {
		prm := Param{Name: p.expectName("o nome do parâmetro").Text}
		if p.accept("=") {
			prm.Default = p.parseTernary()
		}
		ps = append(ps, prm)
		if !p.accept(",") {
			break
		}
	}
	p.expect(":")
	return &Lambda{ex{t.Line}, ps, p.parseTernary()}
}

func (p *parser) parseOr() Expr {
	x := p.parseAnd()
	for p.isName("or") {
		t := p.next()
		x = &BoolOp{ex{t.Line}, false, x, p.parseAnd()}
	}
	return x
}

func (p *parser) parseAnd() Expr {
	x := p.parseNot()
	for p.isName("and") {
		t := p.next()
		x = &BoolOp{ex{t.Line}, true, x, p.parseNot()}
	}
	return x
}

func (p *parser) parseNot() Expr {
	if p.isName("not") {
		t := p.next()
		return &Unary{ex{t.Line}, "not", p.parseNot()}
	}
	return p.parseCompare()
}

func (p *parser) parseCompare() Expr {
	first := p.parseArith()
	var ops []string
	var rest []Expr
	ln := p.cur().Line
	for {
		t := p.cur()
		op := ""
		switch {
		case t.Kind == OP && (t.Text == "==" || t.Text == "!=" || t.Text == "<" || t.Text == "<=" || t.Text == ">" || t.Text == ">="):
			op = t.Text
			p.next()
		case t.Kind == NAME && t.Text == "in":
			op = "in"
			p.next()
		case t.Kind == NAME && t.Text == "is":
			p.next()
			op = "is"
			if p.isName("not") {
				p.next()
				op = "is not"
			}
		case t.Kind == NAME && t.Text == "not" && p.peek(1).Kind == NAME && p.peek(1).Text == "in":
			p.next()
			p.next()
			op = "not in"
		}
		if op == "" {
			break
		}
		ops = append(ops, op)
		rest = append(rest, p.parseArith())
	}
	if len(ops) == 0 {
		return first
	}
	return &Compare{ex{ln}, first, ops, rest}
}

func (p *parser) parseArith() Expr {
	x := p.parseTerm()
	for p.isOp("+") || p.isOp("-") {
		t := p.next()
		x = &Binary{ex{t.Line}, t.Text, x, p.parseTerm()}
	}
	return x
}

func (p *parser) parseTerm() Expr {
	x := p.parseUnary()
	for p.isOp("*") || p.isOp("/") || p.isOp("//") || p.isOp("%") {
		t := p.next()
		x = &Binary{ex{t.Line}, t.Text, x, p.parseUnary()}
	}
	return x
}

func (p *parser) parseUnary() Expr {
	if p.isOp("-") || p.isOp("+") {
		t := p.next()
		return &Unary{ex{t.Line}, t.Text, p.parseUnary()}
	}
	if p.isName("await") && p.peek(1).Kind != OP || p.isName("await") && p.startsValue() {
		p.next() // "await" é aceito por compatibilidade com discord.py e ignorado
		return p.parseUnary()
	}
	return p.parsePower()
}

func (p *parser) parsePower() Expr {
	x := p.parsePostfix()
	if p.isOp("**") {
		t := p.next()
		return &Binary{ex{t.Line}, "**", x, p.parseUnary()}
	}
	return x
}

func (p *parser) parsePostfix() Expr {
	x := p.parseAtom()
	for {
		t := p.cur()
		switch {
		case t.Kind == OP && t.Text == "(":
			p.next()
			call := &Call{ex: ex{t.Line}, Fn: x}
			for !p.isOp(")") {
				if p.cur().Kind == NAME && p.peek(1).Kind == OP && p.peek(1).Text == "=" {
					call.KwNames = append(call.KwNames, p.next().Text)
					p.next()
					call.KwVals = append(call.KwVals, p.parseTernary())
				} else {
					if len(call.KwNames) > 0 {
						p.fail(p.cur().Line, "argumento posicional depois de argumento nomeado")
					}
					call.Args = append(call.Args, p.parseTernary())
				}
				if !p.accept(",") {
					break
				}
			}
			p.expect(")")
			x = call
		case t.Kind == OP && t.Text == "[":
			p.next()
			x = p.parseSubscript(x, t.Line)
		case t.Kind == OP && t.Text == ".":
			p.next()
			nm := p.cur()
			if nm.Kind != NAME {
				p.fail(nm.Line, "esperava um nome depois do '.' mas achei %s", describe(nm))
			}
			p.next()
			x = &Attr{ex{t.Line}, x, nm.Text}
		default:
			return x
		}
	}
}

func (p *parser) parseSubscript(x Expr, line int) Expr {
	var parts [3]Expr
	colons := 0
	for !p.isOp("]") {
		if p.accept(":") {
			colons++
			if colons > 2 {
				p.fail(line, "fatia inválida")
			}
			continue
		}
		parts[colons] = p.parseTernary()
	}
	p.expect("]")
	if colons == 0 {
		if parts[0] == nil {
			p.fail(line, "índice vazio")
		}
		return &Index{ex{line}, x, parts[0]}
	}
	return &SliceExpr{ex{line}, x, parts[0], parts[1], parts[2]}
}

func (p *parser) parseAtom() Expr {
	t := p.cur()
	switch t.Kind {
	case INT:
		p.next()
		base := 10
		if len(t.Text) > 1 && t.Text[0] == '0' && strings.ContainsRune("xXbBoO", rune(t.Text[1])) {
			base = 0
		}
		v, _ := strconv.ParseInt(t.Text, base, 64)
		return &Const{ex{t.Line}, v}
	case FLOAT:
		p.next()
		v, _ := strconv.ParseFloat(t.Text, 64)
		return &Const{ex{t.Line}, v}
	case STRING:
		p.next()
		s := t.Text
		for p.cur().Kind == STRING { // "a" "b" => "ab"
			s += p.next().Text
		}
		return &Const{ex{t.Line}, s}
	case FSTRING:
		p.next()
		return p.parseFString(t)
	case NAME:
		switch t.Text {
		case "True":
			p.next()
			return &Const{ex{t.Line}, true}
		case "False":
			p.next()
			return &Const{ex{t.Line}, false}
		case "None":
			p.next()
			return &Const{ex{t.Line}, nil}
		}
		if reserved[t.Text] {
			p.fail(t.Line, "'%s' não pode ser usado aqui", t.Text)
		}
		p.next()
		return &Name{ex{t.Line}, t.Text}
	case OP:
		switch t.Text {
		case "(":
			p.next()
			if p.accept(")") {
				return &TupleLit{ex{t.Line}, nil}
			}
			first := p.parseTernary()
			if p.accept(")") {
				return first
			}
			if !p.isOp(",") {
				p.fail(p.cur().Line, "esperava ')' mas achei %s", describe(p.cur()))
			}
			elems := []Expr{first}
			for p.accept(",") && !p.isOp(")") {
				elems = append(elems, p.parseTernary())
			}
			p.expect(")")
			return &TupleLit{ex{t.Line}, elems}
		case "[":
			p.next()
			if p.accept("]") {
				return &ListLit{ex{t.Line}, nil}
			}
			first := p.parseTernary()
			if p.isName("for") {
				p.next()
				target := p.parseTargets()
				if !p.isName("in") {
					p.fail(p.cur().Line, "esperava 'in' na compreensão de lista")
				}
				p.next()
				iter := p.parseOr()
				var cond Expr
				if p.isName("if") {
					p.next()
					cond = p.parseOr()
				}
				p.expect("]")
				return &ListComp{ex{t.Line}, first, target, iter, cond}
			}
			elems := []Expr{first}
			for p.accept(",") && !p.isOp("]") {
				elems = append(elems, p.parseTernary())
			}
			p.expect("]")
			return &ListLit{ex{t.Line}, elems}
		case "{":
			p.next()
			d := &DictLit{ex: ex{t.Line}}
			for !p.isOp("}") {
				k := p.parseTernary()
				if !p.isOp(":") {
					p.fail(p.cur().Line, "esperava ':' no dicionário mas achei %s", describe(p.cur()))
				}
				p.next()
				d.Keys = append(d.Keys, k)
				d.Vals = append(d.Vals, p.parseTernary())
				if !p.accept(",") {
					break
				}
			}
			p.expect("}")
			return d
		}
	}
	p.fail(t.Line, "esperava um valor mas achei %s", describe(t))
	return nil
}

// parseFString divide f"texto {expr!r:spec} texto" em partes.
func (p *parser) parseFString(t Token) Expr {
	rs := []rune(t.Text)
	var parts []FPart
	var lit strings.Builder
	flush := func() {
		if lit.Len() == 0 {
			return
		}
		s, err := unescape(lit.String(), t.Raw)
		if err != nil {
			p.fail(t.Line, "%v", err)
		}
		parts = append(parts, FPart{Lit: s})
		lit.Reset()
	}
	for i := 0; i < len(rs); {
		c := rs[i]
		switch {
		case c == '{' && i+1 < len(rs) && rs[i+1] == '{':
			lit.WriteRune('{')
			i += 2
		case c == '}' && i+1 < len(rs) && rs[i+1] == '}':
			lit.WriteRune('}')
			i += 2
		case c == '}':
			p.fail(t.Line, "'}' sozinho no f-string (use '}}' para escrever uma chave)")
		case c == '{':
			flush()
			j, depth := i+1, 0
			exprEnd, conv, specStart := -1, byte(0), -1
		scan:
			for j < len(rs) {
				ch := rs[j]
				switch ch {
				case '\'', '"':
					k := j + 1
					for k < len(rs) && rs[k] != ch {
						if rs[k] == '\\' {
							k++
						}
						k++
					}
					j = k + 1
					continue
				case '(', '[', '{':
					depth++
				case ')', ']':
					depth--
				case '}':
					if depth == 0 {
						exprEnd = j
						break scan
					}
					depth--
				case '!':
					if depth == 0 && j+1 < len(rs) && rs[j+1] != '=' {
						exprEnd = j
						if j+2 >= len(rs) || (rs[j+2] != '}' && rs[j+2] != ':') {
							p.fail(t.Line, "conversão inválida no f-string")
						}
						conv = byte(rs[j+1])
						if conv != 'r' && conv != 's' {
							p.fail(t.Line, "conversão !%c não existe (use !r ou !s)", conv)
						}
						j += 2
						break scan
					}
				case ':':
					if depth == 0 {
						exprEnd = j
						break scan
					}
				}
				j++
			}
			if j >= len(rs) {
				p.fail(t.Line, "'{' sem '}' no f-string")
			}
			if exprEnd == -1 {
				exprEnd = j
			}
			spec := ""
			if rs[j] == ':' {
				specStart = j + 1
				k := specStart
				for k < len(rs) && rs[k] != '}' {
					k++
				}
				if k >= len(rs) {
					p.fail(t.Line, "'{' sem '}' no f-string")
				}
				spec = string(rs[specStart:k])
				j = k
			}
			text := strings.TrimSpace(string(rs[i+1 : exprEnd]))
			if text == "" {
				p.fail(t.Line, "expressão vazia no f-string")
			}
			parts = append(parts, FPart{X: p.subExpr(text, t.Line), Spec: spec, Conv: conv})
			i = j + 1
		default:
			lit.WriteRune(c)
			i++
		}
	}
	flush()
	return &FStr{ex{t.Line}, parts}
}

func (p *parser) subExpr(text string, line int) Expr {
	toks, err := Lex(text)
	if err != nil {
		p.fail(line, "no f-string: %v", err.(*Error).Msg)
	}
	for i := range toks {
		toks[i].Line = line
	}
	sp := &parser{toks: toks}
	e := sp.parseExprList()
	if !sp.atLineEnd() {
		p.fail(line, "no f-string: não entendi '%s'", text)
	}
	return e
}

var _ = fmt.Sprintf
