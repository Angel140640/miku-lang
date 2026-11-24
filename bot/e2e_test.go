package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"miku/discord"
	"miku/lang"
	"miku/ws"
)

type recorded struct {
	Method, Path string
	Body         map[string]any
	Raw          string
}

type fake struct {
	t        *testing.T
	mu       sync.Mutex
	reqs     []recorded
	identify map[string]any
	conn     *ws.Conn
	seq      int
	gwURL    string
	logs     []string
	out      bytes.Buffer
	outMu    sync.Mutex
}

func (f *fake) requests() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.reqs...)
}

func (f *fake) restHandler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	f.mu.Lock()
	f.reqs = append(f.reqs, recorded{r.Method, r.URL.Path, body, string(raw)})
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	p := r.URL.Path
	switch {
	case strings.HasSuffix(p, "/callback"):
		w.WriteHeader(http.StatusNoContent)
	case r.Method == "PUT" && strings.HasSuffix(p, "/commands"):
		w.Write([]byte("[]"))
	case r.Method == "GET" && strings.HasSuffix(p, "/messages"):
		id := strconv.FormatInt((time.Now().UnixMilli()-discordEpoch)<<22, 10)
		w.Write([]byte(`[{"id":"` + id + `"},{"id":"` + id + `1"},{"id":"1"}]`)) // o último é antigo (>14 dias)
	case r.Method == "GET" && p == "/channels/C1":
		w.Write([]byte(`{"id":"C1","guild_id":"G1","permission_overwrites":[{"id":"G1","type":0,"allow":"0","deny":"64"}]}`))
	case strings.HasPrefix(p, "/users/") && r.Method == "GET":
		w.Write([]byte(`{"id":"U9","username":"zed"}`))
	default:
		w.Write([]byte(`{"id":"R1","channel_id":"C1","content":"x"}`))
	}
}

func (f *fake) send(op int, name string, d any) {
	f.mu.Lock()
	c := f.conn
	f.seq++
	seq := f.seq
	f.mu.Unlock()
	msg := map[string]any{"op": op, "d": d}
	if name != "" {
		msg["t"], msg["s"] = name, seq
	}
	raw, _ := json.Marshal(msg)
	if c != nil {
		_ = c.WriteText(raw)
	}
}

func (f *fake) gatewayHandler(w http.ResponseWriter, r *http.Request) {
	c, err := ws.Accept(w, r)
	if err != nil {
		return
	}
	f.mu.Lock()
	f.conn = c
	f.mu.Unlock()
	f.send(10, "", map[string]any{"heartbeat_interval": 45000})
	for {
		_, raw, err := c.ReadMessage()
		if err != nil {
			return
		}
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		switch p["op"] {
		case float64(2):
			f.mu.Lock()
			f.identify, _ = p["d"].(map[string]any)
			f.mu.Unlock()
			f.send(0, "READY", map[string]any{
				"user":               map[string]any{"id": "B1", "username": "mikubot", "bot": true},
				"guilds":             []any{map[string]any{"id": "G1", "unavailable": true}},
				"session_id":         "sess",
				"resume_gateway_url": f.gwURL,
				"application":        map[string]any{"id": "APP"},
			})
			f.send(0, "GUILD_CREATE", map[string]any{
				"id": "G1", "name": "Servidor", "member_count": 3, "owner_id": "OWN",
				"roles": []any{
					map[string]any{"id": "G1", "permissions": "0"},
					map[string]any{"id": "RBAN", "permissions": "4"},
					map[string]any{"id": "RADM", "permissions": "8"},
				},
				"channels": []any{map[string]any{"id": "C1", "name": "geral", "type": 0}},
			})
		case float64(1):
			f.send(11, "", nil)
		}
	}
}

// start sobe os servidores falsos, carrega o programa e conecta o bot.
func start(t *testing.T, src string, modules ...string) (*fake, *Bot) {
	t.Helper()
	f := &fake{t: t}
	rest := httptest.NewServer(http.HandlerFunc(f.restHandler))
	gw := httptest.NewServer(http.HandlerFunc(f.gatewayHandler))
	f.gwURL = gw.URL
	oldAPI, oldGW := discord.APIBase, discord.GatewayURL
	discord.APIBase, discord.GatewayURL = rest.URL, gw.URL

	set := map[string]bool{}
	for _, m := range modules {
		set[m] = true
	}
	in := lang.New(&lockedWriter{f})
	b := New(in, set, func(format string, a ...any) {
		f.mu.Lock()
		f.logs = append(f.logs, strings.TrimSpace(sprintf(format, a...)))
		f.mu.Unlock()
	})
	prog, err := lang.Parse(src)
	if err != nil {
		t.Fatalf("sintaxe: %v", err)
	}
	if err := in.Exec(prog); err != nil {
		t.Fatalf("execução: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.Run(ctx) }()
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("logs do bot:\n%s\nsaída:\n%s", f.logText(), f.stdout())
		}
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("o bot não parou ao cancelar")
		}
		gw.CloseClientConnections()
		gw.Close()
		rest.Close()
		discord.APIBase, discord.GatewayURL = oldAPI, oldGW
	})
	waitFor(t, "GUILD_CREATE processado", func() bool { return b.cachedGuild("G1") != nil })
	return f, b
}

type lockedWriter struct{ f *fake }

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.f.outMu.Lock()
	defer w.f.outMu.Unlock()
	return w.f.out.Write(p)
}

func (f *fake) stdout() string {
	f.outMu.Lock()
	defer f.outMu.Unlock()
	return f.out.String()
}

func (f *fake) logText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.logs, "\n")
}

func sprintf(format string, a ...any) string { return strings.TrimSpace(fmtSprintf(format, a...)) }

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout esperando: %s", what)
}

// waitReq espera aparecer uma requisição que satisfaça o filtro.
func (f *fake) waitReq(what string, match func(r recorded) bool) recorded {
	f.t.Helper()
	var found recorded
	waitFor(f.t, what, func() bool {
		for _, r := range f.requests() {
			if match(r) {
				found = r
				return true
			}
		}
		return false
	})
	return found
}

func (f *fake) waitMessage(channel, contains string) recorded {
	f.t.Helper()
	return f.waitReq("mensagem em "+channel+" com '"+contains+"'", func(r recorded) bool {
		c, _ := r.Body["content"].(string)
		return r.Method == "POST" && r.Path == "/channels/"+channel+"/messages" && strings.Contains(c, contains)
	})
}

func msg(content string) map[string]any {
	return map[string]any{
		"id": "M1", "channel_id": "C1", "guild_id": "G1", "content": content,
		"author": map[string]any{"id": "U1", "username": "ana", "global_name": "Ana"},
		"member": map[string]any{"nick": "Aninha", "roles": []any{"R7"}},
	}
}

func TestPingCommandAndReady(t *testing.T) {
	f, _ := start(t, `
token "tok"
prefix "!"
status dnd
activity playing "Miku"

on ready:
    print("pronto", bot.user.name)

command ping:
    reply "Pong!"
`)
	waitFor(t, "on ready", func() bool { return strings.Contains(f.stdout(), "pronto mikubot") })
	f.send(0, "MESSAGE_CREATE", msg("!ping"))
	r := f.waitMessage("C1", "Pong!")
	ref, _ := r.Body["message_reference"].(map[string]any)
	if ref["message_id"] != "M1" {
		t.Errorf("a resposta devia citar a mensagem M1: %v", r.Body)
	}
	am, _ := r.Body["allowed_mentions"].(map[string]any)
	if parse, _ := am["parse"].([]any); len(parse) != 1 || parse[0] != "users" {
		t.Errorf("allowed_mentions devia bloquear @everyone/cargos: %v", am)
	}

	f.mu.Lock()
	id := f.identify
	f.mu.Unlock()
	if int(id["intents"].(float64)) != discord.DefaultIntents() {
		t.Errorf("intents = %v, queria %d", id["intents"], discord.DefaultIntents())
	}
	pres, _ := id["presence"].(map[string]any)
	acts, _ := pres["activities"].([]any)
	if pres["status"] != "dnd" || len(acts) != 1 || acts[0].(map[string]any)["name"] != "Miku" {
		t.Errorf("presença errada: %v", pres)
	}
	if sh, _ := id["shard"].([]any); len(sh) != 2 || sh[0] != float64(0) || sh[1] != float64(1) {
		t.Errorf("shard = %v", id["shard"])
	}
}

func TestCommandArguments(t *testing.T) {
	f, _ := start(t, `
token "tok"
command soma(a: int, b: int = 10):
    reply f"{a + b}"

command eco(*, texto):
    reply texto

command quem(alvo: user):
    reply f"{alvo.name}|{alvo.id}"

command todos(*nomes):
    reply "-".join(nomes)

command oi aliases ola, hello:
    reply f"oi {ctx.author.display_name} {ctx.member.display_name} {ctx.guild.name} {ctx.channel.name}"
`)
	cases := []struct{ in, want string }{
		{"!soma 2 3", "5"},
		{"!SOMA 4", "14"},
		{"!soma x 1", "precisa ser um número inteiro"},
		{"!soma", "faltou o argumento `a`. Uso: `!soma <a> [b]`"},
		{"!eco  olá   mundo  bonito ", "olá   mundo  bonito"},
		{`!todos a "b c" d`, "a-b c-d"},
		{"!quem <@123456789012345678>", "zed|U9"},
		{"!quem ninguém", "precisa ser uma menção"},
		{"!ola", "oi Ana Aninha Servidor geral"},
	}
	for i, c := range cases {
		m := msg(c.in)
		m["id"] = "M" + string(rune('A'+i))
		f.send(0, "MESSAGE_CREATE", m)
		f.waitMessage("C1", c.want)
	}
}

func TestIgnoresBotsButListenersSeeThem(t *testing.T) {
	f, _ := start(t, `
token "tok"
command ping:
    reply "Pong!"

on message:
    if message.content == "oi":
        reply "olá!"
    if message.author.bot:
        print("viu bot")
`)
	botMsg := msg("!ping")
	botMsg["author"] = map[string]any{"id": "B2", "username": "outrobot", "bot": true}
	f.send(0, "MESSAGE_CREATE", botMsg)
	waitFor(t, "listener viu a mensagem do bot", func() bool { return strings.Contains(f.stdout(), "viu bot") })
	f.send(0, "MESSAGE_CREATE", msg("oi"))
	f.waitMessage("C1", "olá!")
	for _, r := range f.requests() {
		if c, _ := r.Body["content"].(string); c == "Pong!" {
			t.Errorf("comando não devia rodar para mensagem de bot")
		}
	}
}

func TestSlashCommands(t *testing.T) {
	f, _ := start(t, `
token "tok"
slash dado "Rola um dado" (lados: int = 6, quem: user = None):
    reply f"d{lados} por {ctx.author.name}"

slash lento "Demora":
    sleep(2.8)
    reply "pronto"
`, "slash")
	put := f.waitReq("registro dos slash", func(r recorded) bool { return r.Method == "PUT" && r.Path == "/applications/APP/commands" })
	cmds, _ := put.Body, 0
	_ = cmds
	// o corpo do PUT é uma lista; confere pelo log
	waitFor(t, "log de registro", func() bool { return strings.Contains(f.logText(), "2 slash command(s)") })

	f.send(0, "INTERACTION_CREATE", map[string]any{
		"id": "I1", "token": "T1", "type": 2, "guild_id": "G1", "channel_id": "C1",
		"member": map[string]any{"user": map[string]any{"id": "U1", "username": "ana"}},
		"data":   map[string]any{"name": "dado", "options": []any{map[string]any{"name": "lados", "type": 4, "value": 20}}},
	})
	cb := f.waitReq("callback do slash", func(r recorded) bool { return r.Path == "/interactions/I1/T1/callback" })
	data, _ := cb.Body["data"].(map[string]any)
	if cb.Body["type"] != float64(4) || data["content"] != "d20 por ana" {
		t.Errorf("callback errado: %v", cb.Body)
	}

	// valor padrão quando a opção não é enviada
	f.send(0, "INTERACTION_CREATE", map[string]any{
		"id": "I2", "token": "T2", "type": 2, "guild_id": "G1", "channel_id": "C1",
		"member": map[string]any{"user": map[string]any{"id": "U1", "username": "ana"}},
		"data":   map[string]any{"name": "dado"},
	})
	cb = f.waitReq("callback 2", func(r recorded) bool { return r.Path == "/interactions/I2/T2/callback" })
	if d, _ := cb.Body["data"].(map[string]any); d["content"] != "d6 por ana" {
		t.Errorf("valor padrão errado: %v", cb.Body)
	}

	// handler lento: adia sozinho (type 5) e depois edita a resposta original
	f.send(0, "INTERACTION_CREATE", map[string]any{
		"id": "I3", "token": "T3", "type": 2, "guild_id": "G1", "channel_id": "C1",
		"member": map[string]any{"user": map[string]any{"id": "U1", "username": "ana"}},
		"data":   map[string]any{"name": "lento"},
	})
	cb = f.waitReq("defer automático", func(r recorded) bool { return r.Path == "/interactions/I3/T3/callback" })
	if cb.Body["type"] != float64(5) {
		t.Errorf("devia adiar (type 5): %v", cb.Body)
	}
	ed := f.waitReq("edição da resposta", func(r recorded) bool {
		return r.Method == "PATCH" && r.Path == "/webhooks/APP/T3/messages/@original"
	})
	if ed.Body["content"] != "pronto" {
		t.Errorf("edição errada: %v", ed.Body)
	}
}

func TestEmbedModule(t *testing.T) {
	f, _ := start(t, `
token "tok"
command e:
    reply embed(title="Oi", description="desc", color="red", fields={"a": 1, "b": "x"}, footer="rodapé")
    reply "texto", embed("só título")
`, "embed")
	f.send(0, "MESSAGE_CREATE", msg("!e"))
	r := f.waitReq("embed", func(r recorded) bool { _, ok := r.Body["embeds"]; return ok })
	em := r.Body["embeds"].([]any)[0].(map[string]any)
	if em["title"] != "Oi" || em["color"] != float64(0xE74C3C) || len(em["fields"].([]any)) != 2 {
		t.Errorf("embed errado: %v", em)
	}
	if em["footer"].(map[string]any)["text"] != "rodapé" {
		t.Errorf("footer errado: %v", em)
	}
	f.waitReq("mensagem com texto + embed", func(r recorded) bool { return r.Body["content"] == "texto" && r.Body["embeds"] != nil })
}

func TestModuleHintsWhenNotInstalled(t *testing.T) {
	f, _ := start(t, `
token "tok"
command e:
    reply embed(title="Oi")
`)
	f.send(0, "MESSAGE_CREATE", msg("!e"))
	f.waitMessage("C1", "Deu erro")
	waitFor(t, "log do erro", func() bool { return strings.Contains(f.logText(), "miku install embed") })
}

func TestDirectivesNeedModules(t *testing.T) {
	for _, src := range []string{"shards auto\n", "slash x \"d\":\n    reply \"a\"\n"} {
		in := lang.New(nil)
		New(in, map[string]bool{}, nil)
		prog, err := lang.Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		err = in.Exec(prog)
		if err == nil || !strings.Contains(err.Error(), "miku install") {
			t.Errorf("%q: erro = %v, queria pedir 'miku install'", src, err)
		}
	}
}

func TestDeclarationErrors(t *testing.T) {
	cases := []struct{ src, want string }{
		{"on mesage:\n    pass\n", "evento 'mesage' desconhecido"},
		{"command a:\n    pass\ncommand a:\n    pass\n", "já foi declarado"},
		{"command a(x: foo):\n    pass\n", "tipo 'foo' desconhecido"},
		{"command a(*r, b):\n    pass\n", "precisa ser o último"},
		{"intents banana\n", "intent 'banana' desconhecida"},
		{"prefix 5\n", "precisa ser um texto"},
		{"status verde\n", "variável 'verde'"},
	}
	for _, c := range cases {
		in := lang.New(nil)
		New(in, map[string]bool{"slash": true}, nil)
		prog, err := lang.Parse(c.src)
		if err != nil {
			t.Fatalf("%q: %v", c.src, err)
		}
		err = in.Exec(prog)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: erro = %v, queria conter %q", c.src, err, c.want)
		}
	}
	// slash: obrigatório depois de opcional
	in := lang.New(nil)
	New(in, map[string]bool{"slash": true}, nil)
	prog, _ := lang.Parse("slash x \"d\" (a: int = 1, b: int):\n    pass\n")
	if err := in.Exec(prog); err == nil || !strings.Contains(err.Error(), "obrigatórios precisam vir antes") {
		t.Errorf("erro = %v", err)
	}
}

func TestMemberJoinAndGuildJoinFilter(t *testing.T) {
	f, _ := start(t, `
token "tok"
on member_join(m):
    bot.send("C9", f"bem-vindo {m.mention} em {m.guild.name}")

on guild_join:
    print("entrei em", guild.name)

on channel_create(data):
    print("canal novo", data["name"])
`)
	f.send(0, "GUILD_CREATE", map[string]any{"id": "G1", "name": "Servidor"}) // já estava no READY: não é "entrar"
	f.send(0, "GUILD_CREATE", map[string]any{"id": "G2", "name": "Novo"})
	waitFor(t, "guild_join do G2", func() bool { return strings.Contains(f.stdout(), "entrei em Novo") })
	if strings.Contains(f.stdout(), "entrei em Servidor") {
		t.Errorf("GUILD_CREATE do início não pode disparar guild_join")
	}
	f.send(0, "GUILD_MEMBER_ADD", map[string]any{"guild_id": "G1", "user": map[string]any{"id": "U2", "username": "bob"}})
	f.waitMessage("C9", "bem-vindo <@U2> em Servidor")
	f.send(0, "CHANNEL_CREATE", map[string]any{"id": "C5", "name": "novo-canal"})
	waitFor(t, "evento cru", func() bool { return strings.Contains(f.stdout(), "canal novo novo-canal") })
}

func TestVarsModule(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MIKU_VARS", dir+"/v.json")
	f, _ := start(t, `
token "tok"
command conta:
    n = var_add(f"c:{ctx.author.id}")
    reply f"n={n} tem={var_has('c:U1')} outro={var_get('zz', 'padrão')}"
`, "vars")
	f.send(0, "MESSAGE_CREATE", msg("!conta"))
	f.waitMessage("C1", "n=1 tem=True outro=padrão")
	f.send(0, "MESSAGE_CREATE", msg("!conta"))
	f.waitMessage("C1", "n=2")
}

func TestMessageTooLong(t *testing.T) {
	f, _ := start(t, `
token "tok"
command longo:
    reply "a" * 2001
`)
	f.send(0, "MESSAGE_CREATE", msg("!longo"))
	f.waitMessage("C1", "Deu erro")
	waitFor(t, "log", func() bool { return strings.Contains(f.logText(), "2001 caracteres") })
}

func fmtSprintf(format string, a ...any) string { return fmt.Sprintf(format, a...) }

func TestReplyKeywordArgs(t *testing.T) {
	f, _ := start(t, `
token "tok"
command k:
    reply "com embed", embed=embed(title="T")

slash sussurro "Só você vê":
    reply "psiu", ephemeral=True
`, "embed", "slash")
	f.send(0, "MESSAGE_CREATE", msg("!k"))
	r := f.waitMessage("C1", "com embed")
	if em, _ := r.Body["embeds"].([]any); len(em) != 1 || em[0].(map[string]any)["title"] != "T" {
		t.Errorf("embed= não chegou: %v", r.Body)
	}
	f.send(0, "INTERACTION_CREATE", map[string]any{
		"id": "I9", "token": "T9", "type": 2, "guild_id": "G1", "channel_id": "C1",
		"member": map[string]any{"user": map[string]any{"id": "U1", "username": "ana"}},
		"data":   map[string]any{"name": "sussurro"},
	})
	cb := f.waitReq("callback ephemeral", func(r recorded) bool { return r.Path == "/interactions/I9/T9/callback" })
	if d, _ := cb.Body["data"].(map[string]any); d["flags"] != float64(64) || d["content"] != "psiu" {
		t.Errorf("ephemeral não chegou: %v", cb.Body)
	}
}

func (f *fake) lastPutBody() string {
	reqs := f.requests()
	for i := len(reqs) - 1; i >= 0; i-- {
		if reqs[i].Method == "PUT" {
			return reqs[i].Raw
		}
	}
	return ""
}

func tryDeclare(src string, modules ...string) (*Bot, error) {
	set := map[string]bool{}
	for _, m := range modules {
		set[m] = true
	}
	in := lang.New(nil)
	b := New(in, set, nil)
	prog, err := lang.Parse(src)
	if err != nil {
		return nil, err
	}
	return b, in.Exec(prog)
}
