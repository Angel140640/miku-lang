package bot

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"miku/lang"
)

type slashCmd struct {
	name     string
	desc     string
	requires []string
	params   []lang.Param
	defaults []lang.Value
	body     []lang.Stmt
}

var slashNameRe = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

func (b *Bot) addSlash(in *lang.Interp, d *lang.SlashDecl, env *lang.Env) error {
	if err := b.checkParams(d.Params, true); err != nil {
		return err
	}
	if !slashNameRe.MatchString(d.Name) {
		return fmt.Errorf("nome de slash '%s' inválido: use minúsculas, números, - ou _ (até 32 caracteres)", d.Name)
	}
	if n := len([]rune(d.Desc)); n < 1 || n > 100 {
		return errors.New("a descrição do slash precisa ter de 1 a 100 caracteres")
	}
	if len(d.Params) > 25 {
		return errors.New("um slash command aceita no máximo 25 parâmetros")
	}
	for _, s := range b.slashes {
		if s.name == d.Name {
			return fmt.Errorf("o slash '%s' já foi declarado", d.Name)
		}
	}
	if len(b.slashes) >= 100 {
		return errors.New("o Discord aceita no máximo 100 slash commands")
	}
	if _, err := parsePerms(d.Requires); err != nil {
		return err
	}
	defs, err := b.evalDefaults(in, d.Params, env)
	if err != nil {
		return err
	}
	b.slashes = append(b.slashes, &slashCmd{name: d.Name, desc: d.Desc, requires: d.Requires, params: d.Params, defaults: defs, body: d.Body})
	return nil
}

func optionType(t string) int {
	switch t {
	case "int":
		return 4
	case "bool":
		return 5
	case "user", "member":
		return 6
	case "channel":
		return 7
	case "role":
		return 8
	case "float", "number":
		return 10
	}
	return 3
}

func (b *Bot) slashPayload() []any {
	out := make([]any, 0, len(b.slashes))
	for _, s := range b.slashes {
		opts := make([]any, 0, len(s.params))
		for _, p := range s.params {
			opts = append(opts, map[string]any{
				"name":        p.Name,
				"description": p.Name,
				"type":        optionType(p.Type),
				"required":    p.Default == nil,
			})
		}
		cmd := map[string]any{"name": s.name, "description": s.desc, "type": 1}
		if bits, _ := parsePerms(s.requires); bits != 0 {
			cmd["default_member_permissions"] = strconv.FormatUint(bits, 10)
			cmd["dm_permission"] = false
		}
		if len(opts) > 0 {
			cmd["options"] = opts
		}
		out = append(out, cmd)
	}
	return out
}

// syncSlash registra os slash commands no Discord (global ou, com MIKU_GUILD, num servidor de teste).
func (b *Bot) syncSlash() {
	b.mu.Lock()
	if b.slashSynced || len(b.slashes) == 0 {
		b.mu.Unlock()
		return
	}
	b.slashSynced = true
	app := b.appID
	b.mu.Unlock()
	path := "/applications/" + app + "/commands"
	where := "globais"
	if g := os.Getenv("MIKU_GUILD"); g != "" {
		path = "/applications/" + app + "/guilds/" + g + "/commands"
		where = "do servidor " + g
	}
	if _, err := b.Rest.Put(path, b.slashPayload()); err != nil {
		b.Log("não consegui registrar os slash commands: %v", friendly(err))
		return
	}
	b.Log("%d slash command(s) %s registrado(s)%s", len(b.slashes), where, map[bool]string{true: "", false: " (comandos globais podem levar um tempo para aparecer; teste rápido: defina MIKU_GUILD com o id de um servidor)"}[os.Getenv("MIKU_GUILD") != ""])
}

// ---- interações ----

type interaction struct {
	b         *Bot
	id, token string
	mu        sync.Mutex
	responded bool
	deferred  bool
	deferType int // 5 = "pensando…" (resposta nova), 6 = adiamento silencioso de botão/menu
}

func (it *interaction) do(in *lang.Interp, method, path string, body any) (any, error) {
	if in != nil {
		return it.b.call(in, method, path, body, "")
	}
	return it.b.Rest.Do(method, path, body)
}

func (it *interaction) callbackPath() string {
	return "/interactions/" + it.id + "/" + it.token + "/callback"
}

func (it *interaction) deferNow(in *lang.Interp, ephemeral bool) error {
	return it.deferAs(in, 5, ephemeral)
}

func (it *interaction) deferAs(in *lang.Interp, typ int, ephemeral bool) error {
	it.mu.Lock()
	defer it.mu.Unlock()
	if it.responded || it.deferred {
		return nil
	}
	body := map[string]any{"type": typ}
	if ephemeral {
		body["data"] = map[string]any{"flags": 64}
	}
	if _, err := it.do(in, "POST", it.callbackPath(), body); err != nil {
		return err
	}
	it.deferred = true
	it.deferType = typ
	return nil
}

func (it *interaction) reply(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
	p, err := buildPayload(a, kw)
	if err != nil {
		return nil, err
	}
	p["allowed_mentions"] = map[string]any{"parse": []string{"users"}}
	if truthy, _ := kw["ephemeral"].(bool); truthy {
		p["flags"] = 64
	}
	it.mu.Lock()
	defer it.mu.Unlock()
	app := it.b.appID
	switch {
	case !it.responded && !it.deferred:
		if _, err := it.do(in, "POST", it.callbackPath(), map[string]any{"type": 4, "data": p}); err != nil {
			return nil, err
		}
		it.responded = true
		return nil, nil
	case it.deferred && !it.responded && it.deferType == 5:
		delete(p, "flags")
		res, err := it.do(in, "PATCH", "/webhooks/"+app+"/"+it.token+"/messages/@original", p)
		if err != nil {
			return nil, err
		}
		it.responded = true
		mm, _ := res.(map[string]any)
		return it.b.messageObj(mm), nil
	}
	res, err := it.do(in, "POST", "/webhooks/"+app+"/"+it.token, p)
	if err != nil {
		return nil, err
	}
	mm, _ := res.(map[string]any)
	return it.b.messageObj(mm), nil
}

func (b *Bot) newSlashCtx(d map[string]any, it *interaction) *lang.Object {
	gid, cid := str(d, "guild_id"), str(d, "channel_id")
	o := lang.NewObject("SlashContext")
	var user map[string]any
	if mem := sub(d, "member"); mem != nil {
		user = sub(mem, "user")
		o.Attrs["member"] = b.memberObj(mem, gid)
	} else {
		user = sub(d, "user")
		o.Attrs["member"] = nil
	}
	o.Attrs["author"] = b.userObj(user)
	o.Attrs["guild_id"] = optStr(gid)
	o.Attrs["guild"] = b.guildObj(gid)
	o.Attrs["channel_id"] = optStr(cid)
	o.Attrs["channel"] = b.channelObj(cid)
	o.Attrs["command"] = str(sub(d, "data"), "name")
	o.Repr = func() string { return "<SlashContext>" }
	reply := fn("reply", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		return it.reply(in, a, kw)
	})
	o.Attrs["reply"] = reply
	o.Attrs["send"] = reply
	o.Attrs["defer"] = fn("defer", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		eph, _ := kw["ephemeral"].(bool)
		return nil, it.deferNow(in, eph)
	})
	return o
}

func (b *Bot) optionValue(p lang.Param, raw any, resolved map[string]any, gid string) lang.Value {
	switch p.Type {
	case "int":
		if f, ok := raw.(float64); ok {
			return int64(f)
		}
	case "float", "number":
		if f, ok := raw.(float64); ok {
			return f
		}
	case "bool":
		if v, ok := raw.(bool); ok {
			return v
		}
	case "user", "member":
		id, _ := raw.(string)
		if u := sub(sub(resolved, "users"), id); u != nil {
			return b.newUser(u)
		}
		return id
	case "channel":
		id, _ := raw.(string)
		return b.channelObj(id)
	}
	return lang.FromGo(raw)
}

func (b *Bot) handleInteraction(d map[string]any) {
	switch intv(d, "type") {
	case 2: // slash command
		if !b.Installed["slash"] {
			return
		}
	case 3, 5: // botão/menu, modal
		if b.Installed["components"] {
			b.handleComponent(d)
		}
		return
	default:
		return
	}
	data := sub(d, "data")
	name := str(data, "name")
	var cmd *slashCmd
	for _, s := range b.slashes {
		if s.name == name {
			cmd = s
			break
		}
	}
	if cmd == nil {
		return
	}
	given := map[string]any{}
	for _, o := range sl(data, "options") {
		if om, ok := o.(map[string]any); ok {
			given[str(om, "name")] = om["value"]
		}
	}
	vars := make(map[string]lang.Value, len(cmd.params)+1)
	for i, p := range cmd.params {
		if raw, ok := given[p.Name]; ok {
			vars[p.Name] = b.optionValue(p, raw, sub(data, "resolved"), str(d, "guild_id"))
		} else {
			vars[p.Name] = cmd.defaults[i]
		}
	}
	it := &interaction{b: b, id: str(d, "id"), token: str(d, "token")}
	if len(cmd.requires) > 0 {
		if msg := b.slashPermError(d, cmd.requires); msg != "" {
			_, _ = it.reply(nil, []lang.Value{msg}, map[string]lang.Value{"ephemeral": true})
			return
		}
	}
	vars["ctx"] = b.newSlashCtx(d, it)

	// o Discord exige resposta em 3s: se o código demorar, avisa que está "pensando"
	timer := time.AfterFunc(2500*time.Millisecond, func() {
		if err := it.deferNow(nil, false); err != nil {
			b.Log("não consegui adiar a resposta do slash %s: %v", name, err)
		}
	})
	err := b.In.RunHandler(cmd.body, vars)
	timer.Stop()
	if err != nil {
		b.logHandlerErr("slash "+name, err)
	}

	it.mu.Lock()
	noReply := !it.responded
	it.mu.Unlock()
	if noReply {
		text := "✅ Feito."
		if err != nil {
			text = "⚠️ Deu erro nesse comando. Quem cuida do bot pode ver os detalhes no console."
		}
		_, rerr := it.reply(nil, []lang.Value{text}, map[string]lang.Value{"ephemeral": true})
		if rerr != nil {
			b.Log("não consegui responder o slash %s: %v", name, rerr)
		}
	} else if err != nil {
		_, _ = it.reply(nil, []lang.Value{"⚠️ Deu erro depois da resposta. Veja o console."}, map[string]lang.Value{"ephemeral": true})
	}
}

func (b *Bot) slashPermError(d map[string]any, req []string) string {
	mem := sub(d, "member")
	if mem == nil {
		return "🚫 esse comando só funciona em servidores."
	}
	var have uint64
	if ps := str(mem, "permissions"); ps != "" { // o Discord já manda as permissões calculadas
		have, _ = strconv.ParseUint(ps, 10, 64)
		if have&permBits["administrator"] != 0 {
			have = allPerms
		}
	} else if h, ok := b.guildPerms(str(d, "guild_id"), str(sub(mem, "user"), "id"), memberRoleIDs(mem)); ok {
		have = h
	}
	if miss := missingPerms(have, req); len(miss) > 0 {
		return "🚫 você precisa da permissão: " + strings.Join(miss, ", ")
	}
	return ""
}
