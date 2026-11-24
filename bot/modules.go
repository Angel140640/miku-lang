package bot

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"miku/lang"
)

// Module é um recurso opcional embutido no binário; só vale depois de `miku install <nome>`.
type Module struct {
	Name     string
	Desc     string
	Provides []string // nomes que o módulo cria (usado para dar dica quando faltam)
	Apply    func(b *Bot)
	Requires []string // módulos que são instalados junto
	Source   string   // código .mk embutido (módulos escritos em Miku)
}

var Modules = []Module{
	{Name: "shards", Desc: "sharding automático para bots grandes (diretiva: shards auto | shards 4)"},
	{Name: "slash", Desc: `slash commands (diretiva: slash nome "descrição" (param: tipo):)`},
	{Name: "embed", Desc: "função embed(...) para montar embeds bonitos", Provides: []string{"embed"}, Apply: applyEmbed},
	{Name: "vars", Desc: "variáveis salvas em disco: var_get, var_set, var_add, var_has, var_del",
		Provides: []string{"var_get", "var_set", "var_add", "var_has", "var_del", "var_keys"}, Apply: applyVars},
	{Name: "rest", Desc: "chamadas diretas à API REST do Discord: rest_request, rest_send, rest_user",
		Provides: []string{"rest_request", "rest_send", "rest_user"}, Apply: applyRest},
	{Name: "http", Desc: "requisições web: http_get, http_post, http_request (bloqueia redes internas por segurança)",
		Provides: []string{"http_get", "http_post", "http_request"}, Apply: applyHTTP},
	{Name: "time", Desc: "tempo: every, after, cancel, format_time, parse_duration, human_duration, discord_time",
		Provides: []string{"every", "after", "cancel", "format_time", "parse_duration", "human_duration", "discord_time"}, Apply: applyTime},
	{Name: "mod", Desc: "moderação: purge, lock, unlock, slowmode, unban, warn, warns, clear_warns",
		Provides: []string{"purge", "lock", "unlock", "slowmode", "unban", "warn", "warns", "clear_warns"}, Apply: applyMod},
	{Name: "components", Desc: "botões, menus e modais: button, select, row, text_input, modal + on component/modal",
		Provides: []string{"button", "select", "row", "text_input", "modal"}, Apply: applyComponents},
	{Name: "economia", Desc: "pronto: moedas por servidor (saldo, diario, trabalhar, pagar, top)",
		Requires: []string{"vars", "embed", "time"}, Source: "mkmods/economia.mk"},
	{Name: "niveis", Desc: "pronto: XP por mensagem, níveis e ranking (rank, ranking)",
		Requires: []string{"vars", "embed", "time"}, Source: "mkmods/niveis.mk"},
	{Name: "boasvindas", Desc: "pronto: mensagem de boas-vindas quando alguém entra (precisa da intent Server Members)",
		Requires: []string{"vars"}, Source: "mkmods/boasvindas.mk"},
}

//go:embed mkmods/*.mk
var mkFS embed.FS

// MkSource é um módulo escrito em Miku (embutido) que deve rodar antes do programa.
type MkSource struct{ Name, Code string }

// InstalledMkModules devolve o código dos módulos .mk embutidos que estão instalados, na ordem da lista.
func InstalledMkModules() []MkSource {
	on := InstalledSet()
	var out []MkSource
	for _, m := range Modules {
		if m.Source != "" && on[m.Name] {
			if raw, err := mkFS.ReadFile(m.Source); err == nil {
				out = append(out, MkSource{m.Name, string(raw)})
			}
		}
	}
	return out
}

func IsBuiltinModule(name string) bool {
	for _, m := range Modules {
		if m.Name == name {
			return true
		}
	}
	return false
}

// ---------------- embed ----------------

func parseColor(v lang.Value) (int64, error) {
	switch x := v.(type) {
	case nil:
		return embedColors["miku"], nil
	case int64:
		if x < 0 || x > 0xFFFFFF {
			return 0, errors.New("embed: a cor precisa estar entre 0 e 0xFFFFFF")
		}
		return x, nil
	case string:
		if c, ok := embedColors[strings.ToLower(x)]; ok {
			return c, nil
		}
		hex := strings.TrimPrefix(x, "#")
		if n, err := strconv.ParseInt(hex, 16, 64); err == nil && len(hex) == 6 {
			return n, nil
		}
		return 0, fmt.Errorf("embed: cor '%s' desconhecida (use \"#39c5bb\", um número ou um nome como \"red\", \"blue\", \"pink\")", x)
	}
	return 0, errors.New("embed: a cor precisa ser um texto (\"#39c5bb\") ou um número")
}

func parseFields(v lang.Value) ([]any, error) {
	var out []any
	add := func(name, value lang.Value, inline bool) error {
		n, val := lang.Str(name), lang.Str(value)
		if n == "" || val == "" {
			return errors.New("embed: nome e valor de um campo não podem ser vazios")
		}
		if len([]rune(n)) > 256 || len([]rune(val)) > 1024 {
			return errors.New("embed: nome de campo vai até 256 caracteres e o valor até 1024")
		}
		out = append(out, map[string]any{"name": n, "value": val, "inline": inline})
		return nil
	}
	switch x := v.(type) {
	case nil:
		return nil, nil
	case *lang.Dict:
		for _, k := range x.Keys() {
			val, _, _ := x.Get(k)
			if err := add(k, val, false); err != nil {
				return nil, err
			}
		}
	case *lang.List:
		for _, it := range x.Items {
			row, ok := it.(*lang.List)
			if !ok || len(row.Items) < 2 || len(row.Items) > 3 {
				return nil, errors.New("embed: cada campo é [nome, valor] ou [nome, valor, inline]")
			}
			inline := false
			if len(row.Items) == 3 {
				inline, _ = row.Items[2].(bool)
			}
			if err := add(row.Items[0], row.Items[1], inline); err != nil {
				return nil, err
			}
		}
	default:
		return nil, errors.New("embed: fields precisa ser um dicionário {nome: valor} ou uma lista de [nome, valor]")
	}
	if len(out) > 25 {
		return nil, errors.New("embed: no máximo 25 campos")
	}
	return out, nil
}

func applyEmbed(b *Bot) {
	known := map[string]int{"title": 0, "description": 1, "color": -1, "fields": -1, "footer": -1,
		"image": -1, "thumbnail": -1, "author": -1, "url": -1, "timestamp": -1}
	b.In.DefineFunc("embed", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		if len(a) > 2 {
			return nil, errors.New("embed(): só título e descrição são posicionais; use nomes para o resto (ex.: color=\"red\")")
		}
		for k := range kw {
			if _, ok := known[k]; !ok {
				return nil, fmt.Errorf("embed() não tem o argumento '%s'", k)
			}
		}
		get := func(name string) lang.Value {
			if v, ok := kw[name]; ok {
				return v
			}
			if i := known[name]; i >= 0 && i < len(a) {
				return a[i]
			}
			return nil
		}
		e := map[string]any{}
		for _, f := range []struct {
			key   string
			limit int
		}{{"title", 256}, {"description", 4096}} {
			if v := get(f.key); v != nil {
				s := lang.Str(v)
				if len([]rune(s)) > f.limit {
					return nil, fmt.Errorf("embed: %s passa de %d caracteres", f.key, f.limit)
				}
				e[f.key] = s
			}
		}
		color, err := parseColor(get("color"))
		if err != nil {
			return nil, err
		}
		e["color"] = color
		fields, err := parseFields(get("fields"))
		if err != nil {
			return nil, err
		}
		if fields != nil {
			e["fields"] = fields
		}
		if v := get("footer"); v != nil {
			e["footer"] = map[string]any{"text": lang.Str(v)}
		}
		if v := get("image"); v != nil {
			e["image"] = map[string]any{"url": lang.Str(v)}
		}
		if v := get("thumbnail"); v != nil {
			e["thumbnail"] = map[string]any{"url": lang.Str(v)}
		}
		if v := get("author"); v != nil {
			e["author"] = map[string]any{"name": lang.Str(v)}
		}
		if v := get("url"); v != nil {
			e["url"] = lang.Str(v)
		}
		if ts, _ := get("timestamp").(bool); ts {
			e["timestamp"] = time.Now().UTC().Format(time.RFC3339)
		}
		if len(e) == 1 { // só a cor
			return nil, errors.New("embed() vazio: informe pelo menos título, descrição ou campos")
		}
		o := lang.NewObject("Embed")
		o.Native = e
		o.Repr = func() string { return "<Embed>" }
		return o, nil
	})
}

// ---------------- vars ----------------

func applyVars(b *Bot) {
	path := os.Getenv("MIKU_VARS")
	if path == "" {
		path = "miku_vars.json"
	}
	var data *lang.Dict
	load := func() error {
		if data != nil {
			return nil
		}
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			data = lang.NewDict()
			return nil
		}
		if err != nil {
			return fmt.Errorf("não consegui ler %s: %v", path, err)
		}
		v, err := lang.ParseJSON(string(raw))
		d, ok := v.(*lang.Dict)
		if err != nil || !ok {
			return fmt.Errorf("o arquivo %s não é um JSON de objeto válido", path)
		}
		data = d
		return nil
	}
	save := func() error {
		g, err := lang.ToGo(data)
		if err != nil {
			return err
		}
		out, err := json.MarshalIndent(g, "", "  ")
		if err != nil {
			return err
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, out, 0o644); err != nil {
			return err
		}
		return os.Rename(tmp, path)
	}
	key := func(name string, a []lang.Value) (string, error) {
		if len(a) == 0 {
			return "", fmt.Errorf("%s() precisa de uma chave", name)
		}
		switch x := a[0].(type) {
		case string:
			return x, nil
		case int64:
			return strconv.FormatInt(x, 10), nil
		}
		return "", fmt.Errorf("%s(): a chave precisa ser texto ou número inteiro", name)
	}
	def := func(name string, f func(k string, a []lang.Value) (lang.Value, error)) {
		b.In.DefineFunc(name, func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
			k, err := key(name, a)
			if err != nil {
				return nil, err
			}
			if err := load(); err != nil {
				return nil, err
			}
			return f(k, a[1:])
		})
	}
	set := func(k string, v lang.Value) error {
		if _, err := lang.ToGo(v); err != nil {
			return fmt.Errorf("var_set só guarda textos, números, listas e dicionários (%v)", err)
		}
		data.SetStr(k, v)
		return save()
	}
	def("var_get", func(k string, a []lang.Value) (lang.Value, error) {
		if v, ok := data.GetStr(k); ok {
			return v, nil
		}
		if len(a) > 0 {
			return a[0], nil
		}
		return nil, nil
	})
	def("var_set", func(k string, a []lang.Value) (lang.Value, error) {
		if len(a) != 1 {
			return nil, errors.New("var_set(chave, valor) precisa de um valor")
		}
		return a[0], set(k, a[0])
	})
	def("var_add", func(k string, a []lang.Value) (lang.Value, error) {
		var amount lang.Value = int64(1)
		if len(a) > 0 {
			amount = a[0]
		}
		cur, ok := data.GetStr(k)
		if !ok {
			cur = int64(0)
		}
		var sum lang.Value
		switch c := cur.(type) {
		case int64:
			switch n := amount.(type) {
			case int64:
				sum = c + n
			case float64:
				sum = float64(c) + n
			}
		case float64:
			switch n := amount.(type) {
			case int64:
				sum = c + float64(n)
			case float64:
				sum = c + n
			}
		}
		if sum == nil {
			return nil, fmt.Errorf("var_add: '%s' ou o valor somado não é número", k)
		}
		return sum, set(k, sum)
	})
	def("var_has", func(k string, a []lang.Value) (lang.Value, error) {
		_, ok := data.GetStr(k)
		return ok, nil
	})
	b.In.DefineFunc("var_keys", func(_ *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		if err := load(); err != nil {
			return nil, err
		}
		prefix := ""
		if len(a) > 0 {
			prefix = lang.Str(a[0])
		}
		out := []lang.Value{}
		for _, k := range data.Keys() {
			if ks, ok := k.(string); ok && strings.HasPrefix(ks, prefix) {
				out = append(out, ks)
			}
		}
		return &lang.List{Items: out}, nil
	})
	def("var_del", func(k string, a []lang.Value) (lang.Value, error) {
		ok, _ := data.Delete(k)
		if ok {
			return true, save()
		}
		return false, nil
	})
}

// ---------------- rest ----------------

func applyRest(b *Bot) {
	b.In.DefineFunc("rest_request", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		if len(a) < 2 {
			return nil, errors.New("rest_request(método, caminho, json=None) precisa de método e caminho")
		}
		method, ok1 := a[0].(string)
		path, ok2 := a[1].(string)
		if !ok1 || !ok2 || !strings.HasPrefix(path, "/") {
			return nil, errors.New("rest_request: método e caminho são textos e o caminho começa com / (ex.: \"/users/@me\")")
		}
		var body lang.Value
		if len(a) > 2 {
			body = a[2]
		}
		if v, ok := kw["json"]; ok {
			body = v
		}
		var payload any
		if body != nil {
			g, err := lang.ToGo(body)
			if err != nil {
				return nil, err
			}
			payload = g
		}
		res, err := b.call(in, strings.ToUpper(method), path, payload, "")
		if err != nil {
			return nil, err
		}
		return lang.FromGo(res), nil
	})
	b.In.DefineFunc("rest_send", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		cid, err := argStr("rest_send", a, 0)
		if err != nil {
			return nil, err
		}
		return b.sendTo(in, cid, a[1:], kw, "")
	})
	b.In.DefineFunc("rest_user", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		id, err := argStr("rest_user", a, 0)
		if err != nil {
			return nil, err
		}
		res, err := b.call(in, "GET", "/users/"+id, nil, "")
		if err != nil {
			return nil, err
		}
		return lang.FromGo(res), nil
	})
}
