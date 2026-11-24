// Package bot liga a linguagem Miku ao Discord: diretivas, eventos, comandos e objetos.
package bot

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"miku/discord"
	"miku/lang"
)

type command struct {
	name     string
	aliases  []string
	requires []string
	params   []lang.Param
	defaults []lang.Value
	body     []lang.Stmt
}

type listener struct {
	params []string
	body   []lang.Stmt
}

type Bot struct {
	In        *lang.Interp
	Rest      *discord.Client
	Log       func(format string, a ...any)
	Installed map[string]bool

	token      string
	prefix     string
	intents    int
	intentsSet bool
	useShards  bool
	shardCount int
	status     string
	activity   map[string]any

	commands  map[string]*command
	listeners map[string][]*listener
	slashes   []*slashCmd

	mu          sync.RWMutex
	guilds      map[string]map[string]any
	channels    map[string]map[string]any
	self        map[string]any
	appID       string
	startGuilds map[string]bool
	readyFired  bool
	slashSynced bool
	totalShards int
	runCtx      context.Context
	pending     []func(context.Context)
}

type eventDef struct {
	gw     string
	module string // módulo que precisa estar instalado para ouvir esse evento
	params []string
	build  func(b *Bot, d map[string]any) []lang.Value
	filter func(b *Bot, d map[string]any) bool
}

var eventDefs map[string]eventDef

func init() {
	eventDefs = map[string]eventDef{
		"ready":          {gw: "READY"},
		"message":        {gw: "MESSAGE_CREATE", params: []string{"message"}, build: func(b *Bot, d map[string]any) []lang.Value { return []lang.Value{b.messageObj(d)} }},
		"message_edit":   {gw: "MESSAGE_UPDATE", params: []string{"message"}, build: func(b *Bot, d map[string]any) []lang.Value { return []lang.Value{b.messageObj(d)} }},
		"message_delete": {gw: "MESSAGE_DELETE", params: []string{"message"}, build: func(b *Bot, d map[string]any) []lang.Value { return []lang.Value{b.messageObj(d)} }},
		"member_join": {gw: "GUILD_MEMBER_ADD", params: []string{"member"}, build: func(b *Bot, d map[string]any) []lang.Value {
			return []lang.Value{b.memberObj(d, str(d, "guild_id"))}
		}},
		"member_remove": {gw: "GUILD_MEMBER_REMOVE", params: []string{"user", "guild"}, build: func(b *Bot, d map[string]any) []lang.Value {
			return []lang.Value{b.userObj(sub(d, "user")), b.guildObj(str(d, "guild_id"))}
		}},
		"member_update": {gw: "GUILD_MEMBER_UPDATE", params: []string{"member"}, build: func(b *Bot, d map[string]any) []lang.Value {
			return []lang.Value{b.memberObj(d, str(d, "guild_id"))}
		}},
		"reaction_add":    {gw: "MESSAGE_REACTION_ADD", params: []string{"reaction"}, build: reactionArgs},
		"reaction_remove": {gw: "MESSAGE_REACTION_REMOVE", params: []string{"reaction"}, build: reactionArgs},
		"guild_join": {gw: "GUILD_CREATE", params: []string{"guild"},
			filter: func(b *Bot, d map[string]any) bool {
				b.mu.RLock()
				defer b.mu.RUnlock()
				return !b.startGuilds[str(d, "id")] // GUILD_CREATE do início da conexão não é "entrou num servidor"
			},
			build: func(b *Bot, d map[string]any) []lang.Value { return []lang.Value{b.guildObj(str(d, "id"))} }},
		"guild_remove": {gw: "GUILD_DELETE", params: []string{"guild"},
			filter: func(b *Bot, d map[string]any) bool { u, _ := d["unavailable"].(bool); return !u },
			build:  func(b *Bot, d map[string]any) []lang.Value { return []lang.Value{b.guildObj(str(d, "id"))} }},
		"component": {gw: "MIKU_COMPONENT", module: "components", params: []string{"inter"}},
		"modal":     {gw: "MIKU_MODAL", module: "components", params: []string{"inter"}},
		"voice_state_update": {gw: "VOICE_STATE_UPDATE", params: []string{"state"}, build: func(b *Bot, d map[string]any) []lang.Value {
			return []lang.Value{lang.FromGo(d)}
		}},
	}
}

func eventNames() string {
	names := make([]string, 0, len(eventDefs))
	for n := range eventDefs {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func New(in *lang.Interp, installed map[string]bool, log func(string, ...any)) *Bot {
	if log == nil {
		log = func(string, ...any) {}
	}
	b := &Bot{
		In:          in,
		Log:         log,
		Installed:   installed,
		prefix:      "!",
		commands:    map[string]*command{},
		listeners:   map[string][]*listener{},
		guilds:      map[string]map[string]any{},
		channels:    map[string]map[string]any{},
		startGuilds: map[string]bool{},
	}
	in.Host = b
	in.Define("bot", b.botObj())
	for _, m := range Modules {
		if installed[m.Name] {
			if m.Apply != nil {
				m.Apply(b)
			}
		} else {
			for _, name := range m.Provides {
				in.Hints[name] = "esse recurso vem do módulo '" + m.Name + "' — rode: miku install " + m.Name
			}
		}
	}
	return b
}

func (b *Bot) need(module, what string) error {
	if !b.Installed[module] {
		return fmt.Errorf("'%s' precisa do módulo '%s' — rode: miku install %s", what, module, module)
	}
	return nil
}

func (b *Bot) evalString(in *lang.Interp, e lang.Expr, env *lang.Env, what string) (string, error) {
	v, err := in.Eval(e, env)
	if err != nil {
		return "", err
	}
	if v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s precisa ser um texto, não %s", what, lang.Str(v))
	}
	return s, nil
}

var validTypes = map[string]bool{
	"": true, "str": true, "string": true, "int": true, "float": true, "number": true,
	"bool": true, "user": true, "member": true, "channel": true, "role": true, "any": true,
}

func (b *Bot) checkParams(params []lang.Param, slash bool) error {
	seenOptional := false
	for i, p := range params {
		if !validTypes[p.Type] {
			return fmt.Errorf("tipo '%s' desconhecido no parâmetro '%s' (use str, int, float, bool, user, channel ou role)", p.Type, p.Name)
		}
		if slash {
			if p.Kind != lang.PNormal {
				return fmt.Errorf("slash commands não aceitam *%s", p.Name)
			}
			if p.Default != nil {
				seenOptional = true
			} else if seenOptional {
				return fmt.Errorf("parâmetros obrigatórios precisam vir antes dos opcionais ('%s')", p.Name)
			}
		} else if (p.Kind == lang.PRest || p.Kind == lang.PVar) && i != len(params)-1 {
			return fmt.Errorf("'*%s' precisa ser o último parâmetro", p.Name)
		}
	}
	return nil
}

func (b *Bot) evalDefaults(in *lang.Interp, params []lang.Param, env *lang.Env) ([]lang.Value, error) {
	defs := make([]lang.Value, len(params))
	for i, p := range params {
		if p.Default != nil {
			v, err := in.Eval(p.Default, env)
			if err != nil {
				return nil, err
			}
			defs[i] = v
		}
	}
	return defs, nil
}

// Declare executa as diretivas de bot (lang.Host).
func (b *Bot) Declare(in *lang.Interp, s lang.Stmt, env *lang.Env) error {
	switch d := s.(type) {
	case *lang.TokenDecl:
		v, err := in.Eval(d.Val, env)
		if err != nil {
			return err
		}
		if v != nil {
			tok, ok := v.(string)
			if !ok {
				return errors.New("o token precisa ser um texto (ex.: token env(\"DISCORD_TOKEN\"))")
			}
			b.token = strings.TrimSpace(tok)
		}

	case *lang.PrefixDecl:
		p, err := b.evalString(in, d.Val, env, "o prefix")
		if err != nil {
			return err
		}
		if p == "" {
			return errors.New("o prefix não pode ser vazio")
		}
		b.prefix = p

	case *lang.IntentsDecl:
		bits := discord.DefaultIntents()
		if b.intentsSet {
			bits = b.intents // cumulativo: módulos .mk e o programa somam intents
		}
		for _, n := range d.Names {
			switch {
			case n == "all":
				bits = discord.AllIntents()
			case n == "default":
			case discord.IntentBits[n] != 0:
				bits |= discord.IntentBits[n]
			case len(discord.IntentGroups[n]) > 0:
				for _, g := range discord.IntentGroups[n] {
					bits |= discord.IntentBits[g]
				}
			default:
				return fmt.Errorf("intent '%s' desconhecida. Use: all, %s", n, intentNames())
			}
		}
		b.intents, b.intentsSet = bits, true

	case *lang.ShardsDecl:
		if err := b.need("shards", "shards"); err != nil {
			return err
		}
		b.useShards, b.shardCount = true, d.Count

	case *lang.StatusDecl:
		st, err := b.evalString(in, d.Val, env, "o status")
		if err != nil {
			return err
		}
		switch st {
		case "online", "idle", "dnd", "invisible":
			b.status = st
		default:
			return fmt.Errorf("status '%s' inválido (use online, idle, dnd ou invisible)", st)
		}

	case *lang.ActivityDecl:
		text, err := b.evalString(in, d.Val, env, "a atividade")
		if err != nil {
			return err
		}
		types := map[string]int{"playing": 0, "listening": 2, "watching": 3, "competing": 5}
		b.activity = map[string]any{"name": text, "type": types[d.Kind]}

	case *lang.CommandDecl:
		return b.addCommand(in, d, env)

	case *lang.OnDecl:
		return b.addListener(d)

	case *lang.SlashDecl:
		if err := b.need("slash", "slash"); err != nil {
			return err
		}
		return b.addSlash(in, d, env)
	}
	return nil
}

func intentNames() string {
	names := make([]string, 0)
	for n := range discord.IntentBits {
		names = append(names, n)
	}
	for n := range discord.IntentGroups {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func (b *Bot) addCommand(in *lang.Interp, d *lang.CommandDecl, env *lang.Env) error {
	if err := b.checkParams(d.Params, false); err != nil {
		return err
	}
	defs, err := b.evalDefaults(in, d.Params, env)
	if err != nil {
		return err
	}
	if _, err := parsePerms(d.Requires); err != nil {
		return err
	}
	c := &command{name: strings.ToLower(d.Name), requires: d.Requires, params: d.Params, defaults: defs, body: d.Body}
	for _, a := range d.Aliases {
		c.aliases = append(c.aliases, strings.ToLower(a))
	}
	for _, n := range append([]string{c.name}, c.aliases...) {
		if _, dup := b.commands[n]; dup {
			return fmt.Errorf("o comando '%s' já foi declarado", n)
		}
		b.commands[n] = c
	}
	return nil
}

func (b *Bot) addListener(d *lang.OnDecl) error {
	def, known := eventDefs[d.Event]
	gw, params := def.gw, def.params
	if known && def.module != "" {
		if err := b.need(def.module, "on "+d.Event); err != nil {
			return err
		}
	}
	if !known {
		if !d.HasParams {
			return fmt.Errorf("evento '%s' desconhecido. Eventos: %s (ou receba o evento cru do gateway: on %s(data):)", d.Event, eventNames(), d.Event)
		}
		gw, params = strings.ToUpper(d.Event), []string{"data"}
	}
	if d.HasParams {
		params = params[:0:0]
		for _, p := range d.Params {
			if p.Kind != lang.PNormal || p.Default != nil {
				return errors.New("os parâmetros de 'on' são só nomes (ex.: on member_join(membro):)")
			}
			params = append(params, p.Name)
		}
	}
	b.listeners[gw] = append(b.listeners[gw], &listener{params: params, body: d.Body})
	return nil
}

func (b *Bot) logHandlerErr(what string, err error) {
	if err == nil {
		return
	}
	if line := lang.ErrorLine(err); line > 0 {
		b.Log("erro em %s (linha %d): %s", what, line, lang.ErrorMessage(err))
	} else {
		b.Log("erro em %s: %s", what, lang.ErrorMessage(err))
	}
}

// ---------------- execução ----------------

func (b *Bot) Prefix() string { return b.prefix }

// HasHandlers diz se o programa declarou algo para ouvir (command, on ou slash).
func (b *Bot) HasHandlers() bool {
	return len(b.commands)+len(b.listeners)+len(b.slashes)+len(b.pending) > 0
}

// Run conecta no Discord e bloqueia até o contexto acabar ou um erro fatal.
func (b *Bot) Run(ctx context.Context) error {
	if b.token == "" {
		return errors.New("faltou o token do bot — use `token env(\"DISCORD_TOKEN\")` no .mk e defina DISCORD_TOKEN no ambiente ou num arquivo .env")
	}
	if b.Rest == nil {
		b.Rest = discord.NewClient(b.token)
	}
	b.mu.Lock()
	b.runCtx = ctx
	pend := b.pending
	b.pending = nil
	b.mu.Unlock()
	for _, f := range pend {
		go f(ctx)
	}
	b.Log("♪ conectando ao Discord…")
	intents := b.intents
	if !b.intentsSet {
		intents = discord.DefaultIntents()
	}
	var presence map[string]any
	if b.status != "" || b.activity != nil {
		status := b.status
		if status == "" {
			status = "online"
		}
		acts := []any{}
		if b.activity != nil {
			acts = append(acts, b.activity)
		}
		presence = map[string]any{"status": status, "activities": acts, "since": nil, "afk": false}
	}
	newGW := func() *discord.Gateway {
		return &discord.Gateway{
			Token: b.token, Intents: intents, Presence: presence,
			OnEvent: b.onEvent, Log: b.Log,
		}
	}
	if b.useShards {
		return discord.RunShards(ctx, b.Rest, newGW, b.shardCount, b.Log)
	}
	g := newGW()
	g.ShardID, g.ShardCount = 0, 1
	b.totalShards = 1
	return g.Run(ctx)
}

// ---------------- eventos ----------------

func (b *Bot) onEvent(ev discord.Event) {
	b.updateCache(ev)
	switch ev.Name {
	case "READY":
		b.handleReady(ev.Data)
		return
	case "MESSAGE_CREATE":
		b.dispatch(ev.Name, ev.Data)
		b.handleCommand(ev.Data)
		return
	case "INTERACTION_CREATE":
		go b.handleInteraction(ev.Data)
		return
	}
	b.dispatch(ev.Name, ev.Data)
}

func (b *Bot) handleReady(d map[string]any) {
	b.mu.Lock()
	b.self = sub(d, "user")
	b.appID = str(sub(d, "application"), "id")
	for _, g := range sl(d, "guilds") {
		if gm, ok := g.(map[string]any); ok {
			b.startGuilds[str(gm, "id")] = true
		}
	}
	first := !b.readyFired
	b.readyFired = true
	b.mu.Unlock()
	if !first {
		return
	}
	if b.Installed["slash"] {
		go b.syncSlash()
	}
	b.dispatch("READY", d)
}

func (b *Bot) dispatch(gw string, data map[string]any) {
	if len(b.listeners[gw]) == 0 {
		return
	}
	var args []lang.Value
	known := false
	for _, def := range eventDefs {
		if def.gw != gw {
			continue
		}
		known = true
		if def.filter != nil && !def.filter(b, data) {
			return
		}
		if def.build != nil {
			args = def.build(b, data)
		}
		break
	}
	if !known {
		args = []lang.Value{lang.FromGo(data)} // evento cru do gateway
	}
	b.runListeners(gw, args)
}

// runListeners roda todos os `on` registrados para o evento com os argumentos já prontos.
func (b *Bot) runListeners(gw string, args []lang.Value) *sync.WaitGroup {
	wg := &sync.WaitGroup{}
	for _, l := range b.listeners[gw] {
		vars := make(map[string]lang.Value, len(l.params)+1)
		for i, p := range l.params {
			if i < len(args) {
				vars[p] = args[i]
			} else {
				vars[p] = nil
			}
		}
		if len(args) > 0 {
			if o, ok := args[0].(*lang.Object); ok && o.Type == "Message" {
				if _, taken := vars["message"]; !taken {
					vars["message"] = o // `reply`/`send` dentro de on message
				}
			}
		}
		wg.Add(1)
		go func(l *listener, vars map[string]lang.Value) {
			defer wg.Done()
			b.logHandlerErr("on "+strings.ToLower(gw), b.In.RunHandler(l.body, vars))
		}(l, vars)
	}
	return wg
}

// startTask roda f em segundo plano enquanto o bot estiver ligado (adiado até o Run, se ainda não ligou).
func (b *Bot) startTask(f func(ctx context.Context)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.runCtx == nil {
		b.pending = append(b.pending, f)
		return
	}
	go f(b.runCtx)
}

// ---------------- cache ----------------

func (b *Bot) updateCache(ev discord.Event) {
	d := ev.Data
	b.mu.Lock()
	defer b.mu.Unlock()
	switch ev.Name {
	case "GUILD_CREATE", "GUILD_UPDATE":
		id := str(d, "id")
		if old, ok := b.guilds[id]; ok && ev.Name == "GUILD_UPDATE" {
			for k, v := range d {
				old[k] = v
			}
		} else {
			b.guilds[id] = d
		}
		for _, key := range []string{"channels", "threads"} {
			for _, c := range sl(d, key) {
				if cm, ok := c.(map[string]any); ok {
					cm["guild_id"] = id
					b.channels[str(cm, "id")] = cm
				}
			}
		}
	case "GUILD_DELETE":
		if u, _ := d["unavailable"].(bool); !u {
			delete(b.guilds, str(d, "id"))
		}
	case "CHANNEL_CREATE", "CHANNEL_UPDATE", "THREAD_CREATE", "THREAD_UPDATE":
		b.channels[str(d, "id")] = d
	case "CHANNEL_DELETE", "THREAD_DELETE":
		delete(b.channels, str(d, "id"))
	}
}

func (b *Bot) cachedGuild(id string) map[string]any {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.guilds[id]
}

func (b *Bot) cachedChannel(id string) map[string]any {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.channels[id]
}
