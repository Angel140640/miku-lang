package bot

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func msgFrom(content, user string, roles ...any) map[string]any {
	m := msg(content)
	m["author"] = map[string]any{"id": user, "username": "u" + user}
	m["member"] = map[string]any{"roles": roles}
	return m
}

func TestRequiresPermissions(t *testing.T) {
	f, _ := start(t, `
token "tok"
command ban(alvo: user) requires ban_members:
    reply "banido"
command ok:
    reply f"tem={ctx.member.has_permission('ban_members')}"
`)
	f.send(0, "MESSAGE_CREATE", msgFrom("!ban <@123456789012345678>", "U1"))
	f.waitMessage("C1", "você precisa da permissão: ban_members")
	f.send(0, "MESSAGE_CREATE", msgFrom("!ban <@123456789012345678>", "U2", "RBAN"))
	f.waitMessage("C1", "banido")
	f.send(0, "MESSAGE_CREATE", msgFrom("!ban <@123456789012345678>", "OWN"))
	f.send(0, "MESSAGE_CREATE", msgFrom("!ban <@123456789012345678>", "U3", "RADM"))
	waitFor(t, "dono e admin passam", func() bool {
		n := 0
		for _, r := range f.requests() {
			if c, _ := r.Body["content"].(string); c == "banido" {
				n++
			}
		}
		return n == 3
	})
	f.send(0, "MESSAGE_CREATE", msgFrom("!ok", "U1"))
	f.waitMessage("C1", "tem=False")
	f.send(0, "MESSAGE_CREATE", msgFrom("!ok", "U2", "RBAN"))
	f.waitMessage("C1", "tem=True")
}

func TestUnknownPermissionIsError(t *testing.T) {
	_, err := tryDeclare(`command x requires voar:
    reply "a"
`)
	if err == nil || !strings.Contains(err.Error(), "permissão 'voar' desconhecida") {
		t.Fatalf("esperava erro de permissão, veio %v", err)
	}
}

func TestSlashRequires(t *testing.T) {
	f, _ := start(t, `
token "tok"
slash faxina "Limpa" requires manage_messages:
    reply "limpo"
`, "slash")
	r := f.waitReq("registro dos slash", func(r recorded) bool { return r.Method == "PUT" && strings.HasSuffix(r.Path, "/commands") })
	_ = r
	cmds := f.lastPutBody()
	if !strings.Contains(cmds, `"default_member_permissions":"8192"`) {
		t.Errorf("faltou default_member_permissions: %s", cmds)
	}
	inter := func(perms string) map[string]any {
		return map[string]any{"id": "I1", "token": "T1", "type": 2, "guild_id": "G1", "channel_id": "C1",
			"data":   map[string]any{"name": "faxina"},
			"member": map[string]any{"permissions": perms, "user": map[string]any{"id": "U1", "username": "ana"}}}
	}
	f.send(0, "INTERACTION_CREATE", inter("0"))
	f.waitReq("negado", func(r recorded) bool {
		d, _ := r.Body["data"].(map[string]any)
		c, _ := d["content"].(string)
		return strings.HasSuffix(r.Path, "/callback") && strings.Contains(c, "manage_messages")
	})
	f.send(0, "INTERACTION_CREATE", inter("8192"))
	f.waitReq("liberado", func(r recorded) bool {
		d, _ := r.Body["data"].(map[string]any)
		return strings.HasSuffix(r.Path, "/callback") && d["content"] == "limpo"
	})
}

func TestIntentsAreCumulative(t *testing.T) {
	f, _ := start(t, `
token "tok"
intents members
intents presences
on ready:
    print("ok")
`)
	waitFor(t, "ready", func() bool { return strings.Contains(f.stdout(), "ok") })
	f.mu.Lock()
	got := int(f.identify["intents"].(float64))
	f.mu.Unlock()
	if got&(1<<1) == 0 || got&(1<<8) == 0 {
		t.Errorf("intents = %b, faltou members e/ou presences", got)
	}
}

func TestComponentsFlow(t *testing.T) {
	f, _ := start(t, `
token "tok"
command menu:
    reply "escolha", components=[button("Sim", "sim", style="success"), button("Site", url="https://miku.dev"), select("cor", ["azul", ("Verde", "verde")], placeholder="cor")]

on component(inter):
    if inter.custom_id == "sim":
        inter.update("clicou", components=[])
    elif inter.custom_id == "cor":
        inter.reply("cor=" + inter.values[0], ephemeral=True)
    elif inter.custom_id == "abrir":
        inter.show_modal(modal("form", "Formulário", text_input("nome", "Nome", style="paragraph")))

on modal(inter):
    inter.reply(f"oi {inter.fields['nome']}")
`, "components")
	f.send(0, "MESSAGE_CREATE", msgFrom("!menu", "U1"))
	r := f.waitMessage("C1", "escolha")
	rows, _ := r.Body["components"].([]any)
	if len(rows) != 2 {
		t.Fatalf("esperava 2 linhas (botões + menu), veio %v", r.Body["components"])
	}
	btns := rows[0].(map[string]any)["components"].([]any)
	if len(btns) != 2 || btns[0].(map[string]any)["custom_id"] != "sim" || btns[1].(map[string]any)["url"] != "https://miku.dev" {
		t.Errorf("botões errados: %v", btns)
	}
	click := func(id string, compType int, values ...any) {
		f.send(0, "INTERACTION_CREATE", map[string]any{"id": "I" + id, "token": "T" + id, "type": 3, "guild_id": "G1", "channel_id": "C1",
			"data":    map[string]any{"custom_id": id, "component_type": compType, "values": values},
			"member":  map[string]any{"user": map[string]any{"id": "U1", "username": "ana"}},
			"message": map[string]any{"id": "M9", "channel_id": "C1", "content": "escolha"}})
	}
	click("sim", 2)
	f.waitReq("update (type 7)", func(r recorded) bool {
		d, _ := r.Body["data"].(map[string]any)
		return strings.HasSuffix(r.Path, "/callback") && r.Body["type"] == float64(7) && d["content"] == "clicou"
	})
	click("cor", 3, "verde")
	f.waitReq("reply do menu", func(r recorded) bool {
		d, _ := r.Body["data"].(map[string]any)
		return d["content"] == "cor=verde" && d["flags"] == float64(64)
	})
	click("abrir", 2)
	f.waitReq("modal (type 9)", func(r recorded) bool {
		return r.Body["type"] == float64(9)
	})
	f.send(0, "INTERACTION_CREATE", map[string]any{"id": "Im", "token": "Tm", "type": 5, "guild_id": "G1", "channel_id": "C1",
		"data": map[string]any{"custom_id": "form", "components": []any{
			map[string]any{"type": 1, "components": []any{map[string]any{"type": 4, "custom_id": "nome", "value": "Miku"}}}}},
		"member": map[string]any{"user": map[string]any{"id": "U1", "username": "ana"}}})
	f.waitReq("resposta do modal", func(r recorded) bool {
		d, _ := r.Body["data"].(map[string]any)
		return d["content"] == "oi Miku"
	})
}

func TestComponentDefaultAck(t *testing.T) {
	f, _ := start(t, `
token "tok"
on component(inter):
    print("clique", inter.custom_id)
`, "components")
	f.send(0, "INTERACTION_CREATE", map[string]any{"id": "I1", "token": "T1", "type": 3, "guild_id": "G1", "channel_id": "C1",
		"data":   map[string]any{"custom_id": "x", "component_type": 2},
		"member": map[string]any{"user": map[string]any{"id": "U1", "username": "ana"}}})
	waitFor(t, "clique", func() bool { return strings.Contains(f.stdout(), "clique x") })
	f.waitReq("ack silencioso (type 6)", func(r recorded) bool { return r.Body["type"] == float64(6) })
}

func TestComponentsNeedModule(t *testing.T) {
	_, err := tryDeclare("on component(i):\n    print(i)\n")
	if err == nil || !strings.Contains(err.Error(), "miku install components") {
		t.Fatalf("esperava dica do módulo, veio %v", err)
	}
}

func TestTimeFunctions(t *testing.T) {
	f, _ := start(t, `
token "tok"
on ready:
    print("fmt:" + format_time(86400 * 365, "%d de %B de %Y, %A %H:%M", "America/Sao_Paulo"))
    print("dur:", parse_duration("1h30m"), parse_duration("2d"), parse_duration("45"))
    print("hum:" + human_duration(93784))
    print("dc:" + discord_time(100, "R"))
    try:
        parse_duration("abc")
    except Exception as e:
        print("erro:", e)
    id = every(1, lambda: print("tick"))
    after(0, lambda: print("depois"))
`, "time")
	waitFor(t, "saída", func() bool { return strings.Contains(f.stdout(), "tick") && strings.Contains(f.stdout(), "depois") })
	out := f.stdout()
	for _, want := range []string{"fmt:31 de dezembro de 1970, quinta-feira 21:00", "dur: 5400 172800 45", "hum:1d 2h 3min", "dc:<t:100:R>", "erro: parse_duration: duração inválida"} {
		if !strings.Contains(out, want) {
			t.Errorf("faltou %q na saída:\n%s", want, out)
		}
	}
}

func TestModModule(t *testing.T) {
	t.Setenv("MIKU_WARNS", t.TempDir()+"/w.json")
	f, _ := start(t, `
token "tok"
command faxina:
    n = purge(ctx.channel, 5)
    reply f"apagou {n}"
command trava:
    lock(ctx.channel)
    slowmode(ctx.channel, 10)
    unlock(ctx.channel)
    reply "ok"
command avisa:
    warn("G1", "U5", "spam")
    n = warn("G1", "U5", reason="flood")
    reply f"{n} avisos: {warns('G1', 'U5')[1]['reason']}"
`, "mod")
	f.send(0, "MESSAGE_CREATE", msgFrom("!faxina", "U1"))
	f.waitMessage("C1", "apagou 2")
	bulk := f.waitReq("bulk-delete", func(r recorded) bool { return strings.HasSuffix(r.Path, "/bulk-delete") })
	if ids, _ := bulk.Body["messages"].([]any); len(ids) != 2 {
		t.Errorf("devia apagar só as 2 mensagens recentes: %v", bulk.Body)
	}
	f.send(0, "MESSAGE_CREATE", msgFrom("!trava", "U1"))
	f.waitMessage("C1", "ok")
	var puts []recorded
	for _, r := range f.requests() {
		if r.Method == "PUT" && r.Path == "/channels/C1/permissions/G1" {
			puts = append(puts, r)
		}
	}
	if len(puts) != 2 || puts[0].Body["deny"] != "2112" || puts[1].Body["deny"] != "64" {
		t.Errorf("lock/unlock errados: %v", puts)
	}
	f.send(0, "MESSAGE_CREATE", msgFrom("!avisa", "U1"))
	f.waitMessage("C1", "2 avisos: flood")
}

func TestHTTPModule(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Teste", "sim")
		w.Write([]byte(`{"ola":"` + r.Method + r.URL.Query().Get("q") + `"}`))
	}))
	defer srv.Close()
	t.Setenv("MIKU_TEST_URL", srv.URL)
	f, _ := start(t, `
token "tok"
on ready:
    try:
        http_get(env("MIKU_TEST_URL"))
    except Exception as e:
        print("bloqueado:", e)
    r = http_post(env("MIKU_TEST_URL"), json={"a": 1}, params={"q": "!"}, allow_private=True)
    print("resp:", r["status"], r["ok"], r["json"]["ola"], r["headers"]["x-teste"])
`, "http")
	waitFor(t, "resp", func() bool { return strings.Contains(f.stdout(), "resp:") })
	out := f.stdout()
	if !strings.Contains(out, "bloqueado:") || !strings.Contains(out, "rede interna") {
		t.Errorf("devia bloquear rede interna: %s", out)
	}
	if !strings.Contains(out, "resp: 200 True POST! sim") {
		t.Errorf("resposta errada: %s", out)
	}
}

func TestInstallResolvesDependencies(t *testing.T) {
	t.Setenv("MIKU_HOME", t.TempDir())
	msg, err := Install("economia")
	if err != nil || !strings.Contains(msg, "vars") {
		t.Fatalf("install: %q %v", msg, err)
	}
	on := InstalledSet()
	for _, n := range []string{"economia", "vars", "embed", "time"} {
		if !on[n] {
			t.Errorf("%s devia estar instalado: %v", n, on)
		}
	}
	if len(InstalledMkModules()) != 1 {
		t.Errorf("devia haver 1 módulo .mk instalado")
	}
}

func modSrc(t *testing.T, name string) string {
	t.Helper()
	raw, err := mkFS.ReadFile("mkmods/" + name + ".mk")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestEconomiaModule(t *testing.T) {
	t.Setenv("MIKU_VARS", t.TempDir()+"/v.json")
	f, _ := start(t, "token \"tok\"\n"+modSrc(t, "economia"), "vars", "embed", "time", "economia")
	f.send(0, "MESSAGE_CREATE", msgFrom("!diario", "U1"))
	f.waitMessage("C1", "Você ganhou")
	f.send(0, "MESSAGE_CREATE", msgFrom("!diario", "U1"))
	f.waitMessage("C1", "Você já pegou o prêmio diário")
	f.send(0, "MESSAGE_CREATE", msgFrom("!pagar <@123456789012345678> 100000", "U1"))
	f.waitMessage("C1", "Saldo insuficiente")
	f.send(0, "MESSAGE_CREATE", msgFrom("!saldo", "U1"))
	f.waitReq("embed de saldo", func(r recorded) bool {
		em, _ := r.Body["embeds"].([]any)
		if len(em) == 0 {
			return false
		}
		d, _ := em[0].(map[string]any)["description"].(string)
		return strings.Contains(d, "<@U1>") && strings.Contains(d, "moedas")
	})
	f.send(0, "MESSAGE_CREATE", msgFrom("!top", "U1"))
	f.waitReq("top", func(r recorded) bool {
		em, _ := r.Body["embeds"].([]any)
		if len(em) == 0 {
			return false
		}
		d, _ := em[0].(map[string]any)["description"].(string)
		return strings.Contains(d, "**1.** <@U1>")
	})
}

func TestNiveisModule(t *testing.T) {
	t.Setenv("MIKU_VARS", t.TempDir()+"/v.json")
	f, _ := start(t, "token \"tok\"\n"+modSrc(t, "niveis"), "vars", "embed", "time", "niveis")
	f.send(0, "MESSAGE_CREATE", msgFrom("oi", "U1"))
	f.send(0, "MESSAGE_CREATE", msgFrom("!rank", "U1"))
	f.waitReq("rank", func(r recorded) bool {
		em, _ := r.Body["embeds"].([]any)
		if len(em) == 0 {
			return false
		}
		fs, _ := em[0].(map[string]any)["fields"].([]any)
		return len(fs) == 2
	})
}

func TestBoasVindasModule(t *testing.T) {
	t.Setenv("MIKU_VARS", t.TempDir()+"/v.json")
	f, _ := start(t, "token \"tok\"\n"+modSrc(t, "boasvindas"), "vars", "boasvindas")
	f.send(0, "MESSAGE_CREATE", msgFrom("!boasvindas <#123456789012345678>", "U1"))
	f.waitMessage("C1", "você precisa da permissão: manage_guild")
	f.send(0, "MESSAGE_CREATE", msgFrom("!boasvindas <#123456789012345678>", "OWN"))
	f.waitMessage("C1", "Boas-vindas ligadas")
	f.send(0, "GUILD_MEMBER_ADD", map[string]any{"guild_id": "G1", "user": map[string]any{"id": "U7", "username": "novo"}})
	f.waitMessage("123456789012345678", "Bem-vindo(a) <@U7> ao **Servidor**")
	f.mu.Lock()
	got := int(f.identify["intents"].(float64))
	f.mu.Unlock()
	if got&(1<<1) == 0 {
		t.Errorf("o módulo devia ligar a intent members (intents=%b)", got)
	}
}
