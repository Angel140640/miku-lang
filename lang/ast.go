package lang

type Node interface{ Pos() int }

type Expr interface {
	Node
	expr()
}

type Stmt interface {
	Node
	stmt()
}

type ex struct{ Ln int }

func (e ex) Pos() int { return e.Ln }
func (ex) expr()      {}

type st struct{ Ln int }

func (s st) Pos() int { return s.Ln }
func (st) stmt()      {}

// ---- expressões ----

type Const struct { // int64, float64, string, bool ou nil
	ex
	Val any
}

type FPart struct {
	Lit  string
	X    Expr // nil => só texto
	Spec string
	Conv byte // 'r', 's' ou 0
}

type FStr struct {
	ex
	Parts []FPart
}

type Name struct {
	ex
	Id string
}

type ListLit struct {
	ex
	Elems []Expr
}

type TupleLit struct {
	ex
	Elems []Expr
}

type DictLit struct {
	ex
	Keys, Vals []Expr
}

type Unary struct {
	ex
	Op string // "-", "+", "not"
	X  Expr
}

type Binary struct {
	ex
	Op   string
	L, R Expr
}

type BoolOp struct {
	ex
	And  bool
	L, R Expr
}

type Compare struct {
	ex
	First Expr
	Ops   []string // ==, !=, <, <=, >, >=, in, not in, is, is not
	Rest  []Expr
}

type IfExp struct {
	ex
	Cond, Then, Else Expr
}

type Call struct {
	ex
	Fn      Expr
	Args    []Expr
	KwNames []string
	KwVals  []Expr
}

type Attr struct {
	ex
	X    Expr
	Name string
}

type Index struct {
	ex
	X, I Expr
}

type SliceExpr struct {
	ex
	X, Lo, Hi, Step Expr
}

type Lambda struct {
	ex
	Params []Param
	Body   Expr
}

type ListComp struct {
	ex
	Elem   Expr
	Target Expr
	Iter   Expr
	Cond   Expr
}

const (
	PNormal = iota
	PVar    // *args
	PRest   // *, nome  (pega o resto da linha, só em comandos)
)

type Param struct {
	Name    string
	Type    string
	Default Expr
	Kind    int
}

// ---- comandos ----

type ExprStmt struct {
	st
	X Expr
}

type Assign struct {
	st
	Targets []Expr
	Value   Expr
}

type AugAssign struct {
	st
	Target Expr
	Op     string
	Value  Expr
}

type If struct {
	st
	Cond       Expr
	Then, Else []Stmt
}

type While struct {
	st
	Cond Expr
	Body []Stmt
}

type For struct {
	st
	Target Expr
	Iter   Expr
	Body   []Stmt
}

type Break struct{ st }
type Continue struct{ st }
type Pass struct{ st }

type Return struct {
	st
	Val Expr
}

type Def struct {
	st
	Name   string
	Params []Param
	Body   []Stmt
}

type Global struct {
	st
	Names []string
}

type Try struct {
	st
	Body      []Stmt
	HasExcept bool
	ExcName   string
	Except    []Stmt
	Finally   []Stmt
}

type Raise struct {
	st
	Val Expr
}

type Del struct {
	st
	Target Expr
}

// ---- diretivas de bot ----

type TokenDecl struct {
	st
	Val Expr
}

type PrefixDecl struct {
	st
	Val Expr
}

type IntentsDecl struct {
	st
	Names []string
}

type ShardsDecl struct {
	st
	Count int // 0 = auto
}

type StatusDecl struct {
	st
	Val Expr
}

type ActivityDecl struct {
	st
	Kind string // playing, listening, watching, competing
	Val  Expr
}

type CommandDecl struct {
	st
	Name    string
	Aliases []string
	Params  []Param
	Body    []Stmt
}

type OnDecl struct {
	st
	Event     string
	Params    []Param
	HasParams bool
	Body      []Stmt
}

type SlashDecl struct {
	st
	Name   string
	Desc   string
	Params []Param
	Body   []Stmt
}

type Say struct { // reply ... / send ...
	st
	Kind    string
	Args    []Expr
	KwNames []string // reply "x", ephemeral=True
	KwVals  []Expr
}

type Program struct {
	Stmts []Stmt
}
