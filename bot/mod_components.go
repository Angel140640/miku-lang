package bot

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"miku/lang"
)

// Componentes são objetos "Component" cujo Native é o JSON do Discord.

var buttonStyles = map[string]int{"primary": 1, "blurple": 1, "secondary": 2, "grey": 2, "gray": 2, "success": 3, "green": 3, "danger": 4, "red": 4, "link": 5}

func newComponent(kind string, native map[string]any) *lang.Object {
	o := lang.NewObject("Component")
	o.Native = native
	o.Repr = func() string { return "<" + kind + ">" }
	return o
}

func compNative(v lang.Value) (map[string]any, string, bool) {
	o, ok := v.(*lang.Object)
	if !ok || o.Type != "Component" {
		return nil, "", false
	}
	m, ok := o.Native.(map[string]any)
	if !ok {
		return nil, "", false
	}
	return m, o.Repr(), true
}

func emojiPayload(v lang.Value) any {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	if strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">") { // <:nome:id> ou <a:nome:id>
		parts := strings.Split(strings.Trim(s, "<>"), ":")
		if len(parts) == 3 {
			return map[string]any{"name": parts[1], "id": parts[2], "animated": parts[0] == "a"}
		}
	}
	return map[string]any{"name": s}
}

// componentsPayload aceita uma row, um botão/menu solto ou uma lista misturando os dois.
func componentsPayload(v lang.Value) ([]any, error) {
	var items []lang.Value
	if l, ok := v.(*lang.List); ok {
		items = l.Items
	} else {
		items = []lang.Value{v}
	}
	var rows []any
	var loose []any
	flush := func() {
		for len(loose) > 0 {
			n := len(loose)
			if n > 5 {
				n = 5
			}
			rows = append(rows, map[string]any{"type": 1, "components": loose[:n]})
			loose = loose[n:]
		}
	}
	for _, it := range items {
		m, kind, ok := compNative(it)
		if !ok {
			return nil, fmt.Errorf("components: %s não é um botão/menu/row (crie com button(), select() ou row())", lang.Str(it))
		}
		switch ctype(m) {
		case 1:
			flush()
			rows = append(rows, m)
		case 2:
			loose = append(loose, m)
		case 3, 5, 6, 7, 8:
			flush()
			rows = append(rows, map[string]any{"type": 1, "components": []any{m}})
		default:
			return nil, fmt.Errorf("components: %s não pode ir numa mensagem", kind)
		}
	}
	flush()
	if len(rows) > 5 {
		return nil, errors.New("components: o Discord aceita no máximo 5 linhas de componentes por mensagem")
	}
	return rows, nil
}

func kwStr(kw map[string]lang.Value, k string) string {
	s, _ := kw[k].(string)
	return s
}

func optionList(v lang.Value) ([]any, error) {
	l, ok := v.(*lang.List)
	if !ok || len(l.Items) == 0 {
		return nil, errors.New("select: as opções precisam ser uma lista não vazia")
	}
	if len(l.Items) > 25 {
		return nil, errors.New("select: o Discord aceita no máximo 25 opções")
	}
	var out []any
	for _, it := range l.Items {
		opt := map[string]any{}
		switch x := it.(type) {
		case string:
			opt["label"], opt["value"] = x, x
		case *lang.List: // ("Rótulo", "valor") ou ("Rótulo", "valor", "descrição")
			if len(x.Items) < 2 {
				return nil, errors.New("select: cada opção (rótulo, valor) precisa de 2 itens")
			}
			opt["label"], opt["value"] = lang.Str(x.Items[0]), lang.Str(x.Items[1])
			if len(x.Items) > 2 {
				opt["description"] = lang.Str(x.Items[2])
			}
		case *lang.Dict:
			g, err := lang.ToGo(x)
			if err != nil {
				return nil, err
			}
			gm, _ := g.(map[string]any)
			if gm["label"] == nil {
				return nil, errors.New("select: opção em dicionário precisa de \"label\"")
			}
			if gm["value"] == nil {
				gm["value"] = gm["label"]
			}
			if e, ok := gm["emoji"].(string); ok {
				gm["emoji"] = emojiPayload(e)
			}
			opt = gm
		default:
			return nil, fmt.Errorf("select: opção inválida %s", lang.Str(it))
		}
		for _, k := range []string{"label", "value"} {
			if n := len([]rune(lang.Str(opt[k]))); n < 1 || n > 100 {
				return nil, fmt.Errorf("select: %s da opção precisa ter de 1 a 100 caracteres", k)
			}
		}
		out = append(out, opt)
	}
	return out, nil
}

func applyComponents(b *Bot) {
	in := b.In
	def := func(name string, f func(a []lang.Value, kw map[string]lang.Value) (lang.Value, error)) {
		in.DefineFunc(name, func(_ *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
			return f(a, kw)
		})
	}
	// button("Texto", "meu_id", style="success") | button("Site", url="https://…")
	def("button", func(a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		label := kwStr(kw, "label")
		id := kwStr(kw, "id")
		if len(a) > 0 {
			label = lang.Str(a[0])
		}
		if len(a) > 1 {
			id = lang.Str(a[1])
		}
		style := "primary"
		if s := kwStr(kw, "style"); s != "" {
			style = strings.ToLower(s)
		}
		st, ok := buttonStyles[style]
		if !ok {
			return nil, fmt.Errorf("button: estilo '%s' desconhecido (primary, secondary, success, danger, link)", style)
		}
		m := map[string]any{"type": 2, "style": st}
		emoji := emojiPayload(kw["emoji"])
		if label == "" && emoji == nil {
			return nil, errors.New("button precisa de um texto ou emoji")
		}
		if len([]rune(label)) > 80 {
			return nil, errors.New("button: o texto vai até 80 caracteres")
		}
		if label != "" {
			m["label"] = label
		}
		if emoji != nil {
			m["emoji"] = emoji
		}
		if u := kwStr(kw, "url"); u != "" {
			if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
				return nil, errors.New("button: a url precisa começar com http:// ou https://")
			}
			m["style"], m["url"] = 5, u
		} else {
			if st == 5 {
				return nil, errors.New("button de estilo link precisa de url=\"…\"")
			}
			if id == "" || len(id) > 100 {
				return nil, errors.New("button precisa de um id (segundo argumento, até 100 caracteres) para você tratar o clique em `on component`")
			}
			m["custom_id"] = id
		}
		if d, _ := kw["disabled"].(bool); d {
			m["disabled"] = true
		}
		return newComponent("button", m), nil
	})
	// select("id", ["a","b"], placeholder="Escolha", max=2)
	def("select", func(a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		if len(a) < 2 {
			return nil, errors.New("select(id, opções) precisa de id e lista de opções")
		}
		id := lang.Str(a[0])
		if id == "" || len(id) > 100 {
			return nil, errors.New("select: o id precisa ter de 1 a 100 caracteres")
		}
		opts, err := optionList(a[1])
		if err != nil {
			return nil, err
		}
		m := map[string]any{"type": 3, "custom_id": id, "options": opts}
		if p := kwStr(kw, "placeholder"); p != "" {
			m["placeholder"] = p
		}
		minV, maxV := int64(1), int64(1)
		if v, ok := kw["min"].(int64); ok {
			minV = v
		}
		if v, ok := kw["max"].(int64); ok {
			maxV = v
		}
		if minV < 0 || maxV < 1 || maxV > 25 || minV > maxV {
			return nil, errors.New("select: min/max inválidos (max vai de 1 a 25 e min não pode passar do max)")
		}
		m["min_values"], m["max_values"] = minV, maxV
		if d, _ := kw["disabled"].(bool); d {
			m["disabled"] = true
		}
		return newComponent("select", m), nil
	})
	def("row", func(a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		if len(a) == 1 {
			if l, ok := a[0].(*lang.List); ok {
				a = l.Items
			}
		}
		var cs []any
		sel := false
		for _, c := range a {
			m, _, ok := compNative(c)
			if !ok {
				return nil, fmt.Errorf("row: %s não é um botão/menu", lang.Str(c))
			}
			t := ctype(m)
			if t == 1 {
				return nil, errors.New("row: não dá para colocar uma row dentro de outra")
			}
			if t != 2 {
				sel = true
			}
			cs = append(cs, m)
		}
		if len(cs) == 0 {
			return nil, errors.New("row precisa de pelo menos um componente")
		}
		if sel && len(cs) > 1 {
			return nil, errors.New("row: um menu ocupa a linha inteira (coloque sozinho)")
		}
		if len(cs) > 5 {
			return nil, errors.New("row: no máximo 5 botões por linha")
		}
		return newComponent("row", map[string]any{"type": 1, "components": cs}), nil
	})
	// text_input("id", "Rótulo", style="paragraph", placeholder=, required=, min=, max=, value=)
	def("text_input", func(a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		if len(a) < 2 {
			return nil, errors.New("text_input(id, rótulo) precisa de id e rótulo")
		}
		style := 1
		if kwStr(kw, "style") == "paragraph" {
			style = 2
		}
		m := map[string]any{"type": 4, "custom_id": lang.Str(a[0]), "label": lang.Str(a[1]), "style": style, "required": true}
		if r, ok := kw["required"].(bool); ok {
			m["required"] = r
		}
		for _, k := range []string{"placeholder", "value"} {
			if v := kwStr(kw, k); v != "" {
				m[k] = v
			}
		}
		if v, ok := kw["min"].(int64); ok {
			m["min_length"] = v
		}
		if v, ok := kw["max"].(int64); ok {
			m["max_length"] = v
		}
		return newComponent("text_input", m), nil
	})
	// modal("id", "Título", text_input(...), ...)
	def("modal", func(a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		if len(a) < 3 {
			return nil, errors.New("modal(id, título, campos...) precisa de id, título e pelo menos um text_input")
		}
		if len(a) > 7 {
			return nil, errors.New("modal: no máximo 5 campos")
		}
		var rows []any
		for _, c := range a[2:] {
			m, _, ok := compNative(c)
			if !ok || ctype(m) != 4 {
				return nil, fmt.Errorf("modal: %s não é um text_input", lang.Str(c))
			}
			rows = append(rows, map[string]any{"type": 1, "components": []any{m}})
		}
		title := lang.Str(a[1])
		if n := len([]rune(title)); n < 1 || n > 45 {
			return nil, errors.New("modal: o título vai de 1 a 45 caracteres")
		}
		return newComponent("modal", map[string]any{"custom_id": lang.Str(a[0]), "title": title, "components": rows}), nil
	})
}

// handleComponent trata cliques em botões/menus (type 3) e envios de modal (type 5).
func (b *Bot) handleComponent(d map[string]any) {
	typ := intv(d, "type")
	gw := "MIKU_COMPONENT"
	if typ == 5 {
		gw = "MIKU_MODAL"
	}
	if len(b.listeners[gw]) == 0 {
		return
	}
	data := sub(d, "data")
	gid, cid := str(d, "guild_id"), str(d, "channel_id")
	it := &interaction{b: b, id: str(d, "id"), token: str(d, "token")}
	o := lang.NewObject("Interaction")
	var user map[string]any
	if mem := sub(d, "member"); mem != nil {
		user = sub(mem, "user")
		o.Attrs["member"] = b.memberObj(mem, gid)
	} else {
		user = sub(d, "user")
		o.Attrs["member"] = nil
	}
	o.Attrs["author"] = b.userObj(user)
	o.Attrs["user"] = o.Attrs["author"]
	o.Attrs["guild_id"] = optStr(gid)
	o.Attrs["guild"] = b.guildObj(gid)
	o.Attrs["channel_id"] = optStr(cid)
	o.Attrs["channel"] = b.channelObj(cid)
	o.Attrs["custom_id"] = str(data, "custom_id")
	values := []lang.Value{}
	for _, v := range sl(data, "values") {
		values = append(values, lang.FromGo(v))
	}
	o.Attrs["values"] = &lang.List{Items: values}
	fields := lang.NewDict()
	for _, row := range sl(data, "components") {
		rm, _ := row.(map[string]any)
		for _, c := range sl(rm, "components") {
			cm, _ := c.(map[string]any)
			if id := str(cm, "custom_id"); id != "" {
				fields.SetStr(id, str(cm, "value"))
			}
		}
	}
	o.Attrs["fields"] = fields
	if typ == 5 {
		o.Attrs["kind"] = "modal"
	} else if intv(data, "component_type") == 2 {
		o.Attrs["kind"] = "button"
	} else {
		o.Attrs["kind"] = "select"
	}
	if m := sub(d, "message"); m != nil {
		o.Attrs["message"] = b.messageObj(m)
	} else {
		o.Attrs["message"] = nil
	}
	o.Repr = func() string { return "<Interaction " + str(data, "custom_id") + ">" }

	o.Attrs["reply"] = fn("reply", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		return it.reply(in, a, kw)
	})
	o.Attrs["send"] = o.Attrs["reply"]
	o.Attrs["defer"] = fn("defer", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		eph, _ := kw["ephemeral"].(bool)
		if typ == 3 && !eph {
			return nil, it.deferAs(in, 6, false) // silencioso: depois use update() ou reply()
		}
		return nil, it.deferAs(in, 5, eph)
	})
	o.Attrs["update"] = fn("update", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		if typ != 3 && sub(d, "message") == nil {
			return nil, errors.New("update() só funciona em botões e menus")
		}
		p, err := buildPayload(a, kw)
		if err != nil {
			if len(a) == 0 && kw["components"] != nil {
				p = map[string]any{}
			} else {
				return nil, err
			}
		}
		if _, has := kw["components"]; has && kw["components"] == nil {
			p["components"] = []any{}
		}
		delete(p, "allowed_mentions")
		it.mu.Lock()
		defer it.mu.Unlock()
		if it.responded {
			return nil, errors.New("update(): já respondi essa interação")
		}
		if it.deferred {
			_, err = it.do(in, "PATCH", "/webhooks/"+b.appID+"/"+it.token+"/messages/@original", p)
		} else {
			_, err = it.do(in, "POST", it.callbackPath(), map[string]any{"type": 7, "data": p})
		}
		if err == nil {
			it.responded = true
		}
		return nil, err
	})
	o.Attrs["show_modal"] = fn("show_modal", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		if len(a) != 1 {
			return nil, errors.New("show_modal(modal) precisa de um modal criado com modal(...)")
		}
		m, kind, ok := compNative(a[0])
		if !ok || kind != "<modal>" {
			return nil, errors.New("show_modal: o argumento precisa vir de modal(...)")
		}
		if typ == 5 {
			return nil, errors.New("show_modal: não dá para abrir um modal a partir de outro modal")
		}
		it.mu.Lock()
		defer it.mu.Unlock()
		if it.responded || it.deferred {
			return nil, errors.New("show_modal precisa ser a primeira resposta da interação (antes de reply/defer)")
		}
		_, err := it.do(in, "POST", it.callbackPath(), map[string]any{"type": 9, "data": m})
		if err == nil {
			it.responded = true
		}
		return nil, err
	})

	timer := time.AfterFunc(2500*time.Millisecond, func() {
		if err := it.deferNow(nil, false); err != nil {
			b.Log("não consegui adiar a resposta da interação: %v", err)
		}
	})
	b.runListeners(gw, []lang.Value{o}).Wait()
	timer.Stop()

	it.mu.Lock()
	responded, deferred, dtype := it.responded, it.deferred, it.deferType
	it.mu.Unlock()
	switch {
	case !responded && !deferred && typ == 3:
		if _, err := b.Rest.Do("POST", it.callbackPath(), map[string]any{"type": 6}); err != nil {
			b.Log("não consegui confirmar a interação: %v", friendly(err))
		}
	case !responded && !deferred:
		_, _ = it.reply(nil, []lang.Value{"✅ Feito."}, map[string]lang.Value{"ephemeral": true})
	case !responded && deferred && dtype == 5:
		_, _ = it.reply(nil, []lang.Value{"✅ Feito."}, nil)
	}
}

func ctype(m map[string]any) int {
	switch t := m["type"].(type) {
	case int:
		return t
	case float64:
		return int(t)
	}
	return 0
}
