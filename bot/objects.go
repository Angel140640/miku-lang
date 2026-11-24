package bot

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"miku/discord"
	"miku/lang"
)

// ---- helpers para ler JSON do gateway ----

func str(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	v, _ := m[k].(string)
	return v
}

func sub(m map[string]any, k string) map[string]any {
	if m == nil {
		return nil
	}
	v, _ := m[k].(map[string]any)
	return v
}

func sl(m map[string]any, k string) []any {
	if m == nil {
		return nil
	}
	v, _ := m[k].([]any)
	return v
}

func flag(m map[string]any, k string) bool {
	if m == nil {
		return false
	}
	v, _ := m[k].(bool)
	return v
}

func intv(m map[string]any, k string) int64 {
	if m == nil {
		return 0
	}
	f, _ := m[k].(float64)
	return int64(f)
}

func optStr(s string) lang.Value {
	if s == "" {
		return nil
	}
	return s
}

// ---- REST chamado de dentro de um builtin (solta o lock do interpretador) ----

var apiMessages = map[int]string{
	50013: "o bot não tem permissão para isso (Missing Permissions) — confira as permissões e a posição do cargo do bot",
	50001: "o bot não tem acesso a esse canal ou servidor (Missing Access)",
	10003: "canal não encontrado",
	10008: "mensagem não encontrada",
	10013: "usuário não encontrado",
	10007: "membro não encontrado no servidor",
	50007: "não consegui mandar DM: a pessoa bloqueou mensagens diretas",
	50006: "não dá para enviar uma mensagem vazia",
}

func friendly(err error) error {
	var ae *discord.APIError
	if errors.As(err, &ae) {
		if msg, ok := apiMessages[ae.Code]; ok {
			return fmt.Errorf("%s (código %d)", msg, ae.Code)
		}
	}
	return err
}

func (b *Bot) call(in *lang.Interp, method, path string, body any, reason string) (any, error) {
	if b.Rest == nil {
		return nil, errors.New("o bot ainda não conectou ao Discord")
	}
	var headers map[string]string
	if reason != "" {
		headers = map[string]string{"X-Audit-Log-Reason": url.PathEscape(reason)}
	}
	var res any
	var err error
	in.Block(func() { res, err = b.Rest.DoH(method, path, body, headers) })
	return res, friendly(err)
}

func fn(name string, f lang.BuiltinFn) *lang.Builtin { return &lang.Builtin{Name: name, Fn: f} }

func argStr(name string, a []lang.Value, i int) (string, error) {
	if i >= len(a) {
		return "", fmt.Errorf("%s() precisa de mais um argumento", name)
	}
	switch x := a[i].(type) {
	case string:
		return x, nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case *lang.Object:
		if id, ok := x.Attrs["id"].(string); ok {
			return id, nil // aceita usuário/canal/cargo no lugar do id
		}
	}
	return "", fmt.Errorf("%s() precisa de um texto ou id, não %s", name, lang.Str(a[i]))
}

func reasonArg(a []lang.Value, kw map[string]lang.Value) string {
	if r, ok := kw["reason"].(string); ok {
		return r
	}
	if len(a) > 0 {
		if r, ok := a[0].(string); ok {
			return r
		}
	}
	return ""
}

// ---- montar mensagens ----

var embedColors = map[string]int64{
	"red": 0xE74C3C, "green": 0x2ECC71, "blue": 0x3498DB, "yellow": 0xF1C40F, "orange": 0xE67E22,
	"purple": 0x9B59B6, "pink": 0xE2539A, "teal": 0x39C5BB, "white": 0xFFFFFF, "black": 0x000000,
	"gray": 0x95A5A6, "grey": 0x95A5A6, "miku": 0x39C5BB,
}

func buildPayload(args []lang.Value, kw map[string]lang.Value) (map[string]any, error) {
	var texts []string
	var embeds []any
	addEmbed := func(v lang.Value) error {
		switch x := v.(type) {
		case *lang.Object:
			if x.Type == "Embed" && x.Native != nil {
				embeds = append(embeds, x.Native)
				return nil
			}
		case *lang.Dict:
			g, err := lang.ToGo(x)
			if err != nil {
				return err
			}
			embeds = append(embeds, g)
			return nil
		}
		return fmt.Errorf("embed inválido: %s (crie com embed(...))", lang.Str(v))
	}
	for _, a := range args {
		switch x := a.(type) {
		case nil:
		case *lang.Object:
			if x.Type == "Embed" {
				if err := addEmbed(x); err != nil {
					return nil, err
				}
			} else {
				texts = append(texts, lang.Str(a))
			}
		case *lang.Dict:
			if err := addEmbed(x); err != nil {
				return nil, err
			}
		default:
			texts = append(texts, lang.Str(a))
		}
	}
	if e, ok := kw["embed"]; ok && e != nil {
		if err := addEmbed(e); err != nil {
			return nil, err
		}
	}
	if es, ok := kw["embeds"].(*lang.List); ok {
		for _, e := range es.Items {
			if err := addEmbed(e); err != nil {
				return nil, err
			}
		}
	}
	content := strings.Join(texts, "\n")
	if content == "" && len(embeds) == 0 {
		return nil, errors.New("não há nada para enviar (texto vazio)")
	}
	if n := len([]rune(content)); n > 2000 {
		return nil, fmt.Errorf("a mensagem tem %d caracteres e o Discord só aceita 2000", n)
	}
	if len(embeds) > 10 {
		return nil, errors.New("o Discord aceita no máximo 10 embeds por mensagem")
	}
	p := map[string]any{
		// por padrão não deixa o bot marcar @everyone/@here nem cargos
		"allowed_mentions": map[string]any{"parse": []string{"users"}, "replied_user": true},
	}
	if content != "" {
		p["content"] = content
	}
	if len(embeds) > 0 {
		p["embeds"] = embeds
	}
	return p, nil
}

func (b *Bot) sendTo(in *lang.Interp, channelID string, a []lang.Value, kw map[string]lang.Value, replyTo string) (lang.Value, error) {
	p, err := buildPayload(a, kw)
	if err != nil {
		return nil, err
	}
	if replyTo != "" {
		p["message_reference"] = map[string]any{"message_id": replyTo, "fail_if_not_exists": false}
	}
	res, err := b.call(in, "POST", "/channels/"+channelID+"/messages", p, "")
	if err != nil {
		return nil, err
	}
	if m, ok := res.(map[string]any); ok {
		return b.messageObj(m), nil
	}
	return nil, nil
}

// sendPlain manda texto simples FORA do interpretador (mensagens de erro de uso).
func (b *Bot) sendPlain(channelID, text string) {
	if b.Rest == nil || channelID == "" {
		return
	}
	_, err := b.Rest.Post("/channels/"+channelID+"/messages", map[string]any{
		"content":          text,
		"allowed_mentions": map[string]any{"parse": []string{}},
	})
	if err != nil {
		b.Log("não consegui enviar mensagem no canal %s: %v", channelID, err)
	}
}

func (b *Bot) sendDM(in *lang.Interp, userID string, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
	res, err := b.call(in, "POST", "/users/@me/channels", map[string]any{"recipient_id": userID}, "")
	if err != nil {
		return nil, err
	}
	ch, _ := res.(map[string]any)
	id := str(ch, "id")
	if id == "" {
		return nil, errors.New("o Discord não devolveu o canal de DM")
	}
	return b.sendTo(in, id, a, kw, "")
}

// ---- objetos ----

func avatarURL(u map[string]any) string {
	id, hash := str(u, "id"), str(u, "avatar")
	if hash != "" {
		ext := "png"
		if strings.HasPrefix(hash, "a_") {
			ext = "gif"
		}
		return fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.%s", id, hash, ext)
	}
	n, _ := strconv.ParseUint(id, 10, 64)
	return fmt.Sprintf("https://cdn.discordapp.com/embed/avatars/%d.png", (n>>22)%6)
}

func (b *Bot) userObj(u map[string]any) lang.Value {
	if u == nil {
		return nil
	}
	return b.newUser(u)
}

func (b *Bot) newUser(u map[string]any) *lang.Object {
	o := lang.NewObject("User")
	id := str(u, "id")
	name := str(u, "global_name")
	if name == "" {
		name = str(u, "username")
	}
	o.Attrs["id"] = id
	o.Attrs["username"] = str(u, "username")
	o.Attrs["name"] = name
	o.Attrs["display_name"] = name
	o.Attrs["mention"] = "<@" + id + ">"
	o.Attrs["bot"] = flag(u, "bot")
	o.Attrs["avatar_url"] = avatarURL(u)
	o.Repr = func() string { return name }
	o.Attrs["send"] = fn("send", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		return b.sendDM(in, id, a, kw)
	})
	return o
}

func (b *Bot) memberObj(m map[string]any, guildID string) lang.Value {
	if m == nil {
		return nil
	}
	return b.newMember(m, guildID)
}

func (b *Bot) newMember(m map[string]any, guildID string) *lang.Object {
	u := sub(m, "user")
	if u == nil {
		u = map[string]any{}
	}
	user := b.newUser(u)
	o := lang.NewObject("Member")
	for k, v := range user.Attrs {
		o.Attrs[k] = v
	}
	nick := str(m, "nick")
	if nick != "" {
		o.Attrs["display_name"] = nick
	}
	id := str(u, "id")
	display, _ := o.Attrs["display_name"].(string)
	o.Attrs["nick"] = optStr(nick)
	o.Attrs["user"] = user
	o.Attrs["guild_id"] = optStr(guildID)
	o.Attrs["guild"] = b.guildObj(guildID)
	o.Attrs["joined_at"] = optStr(str(m, "joined_at"))
	roles := []lang.Value{}
	for _, r := range sl(m, "roles") {
		if s, ok := r.(string); ok {
			roles = append(roles, s)
		}
	}
	o.Attrs["roles"] = &lang.List{Items: roles}
	o.Repr = func() string { return display }

	needGuild := func(name string) error {
		if guildID == "" {
			return fmt.Errorf("%s() só funciona em membros de um servidor", name)
		}
		return nil
	}
	o.Attrs["kick"] = fn("kick", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		if err := needGuild("kick"); err != nil {
			return nil, err
		}
		_, err := b.call(in, "DELETE", "/guilds/"+guildID+"/members/"+id, nil, reasonArg(a, kw))
		return nil, err
	})
	o.Attrs["ban"] = fn("ban", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		if err := needGuild("ban"); err != nil {
			return nil, err
		}
		body := map[string]any{}
		if d, ok := kw["delete_days"].(int64); ok && d > 0 {
			body["delete_message_seconds"] = min(d, 7) * 86400
		}
		_, err := b.call(in, "PUT", "/guilds/"+guildID+"/bans/"+id, body, reasonArg(a, kw))
		return nil, err
	})
	role := func(name, method string) *lang.Builtin {
		return fn(name, func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
			if err := needGuild(name); err != nil {
				return nil, err
			}
			rid, err := argStr(name, a, 0)
			if err != nil {
				return nil, err
			}
			_, err = b.call(in, method, "/guilds/"+guildID+"/members/"+id+"/roles/"+rid, nil, "")
			return nil, err
		})
	}
	o.Attrs["add_role"] = role("add_role", "PUT")
	o.Attrs["remove_role"] = role("remove_role", "DELETE")
	o.Attrs["timeout"] = fn("timeout", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		if err := needGuild("timeout"); err != nil {
			return nil, err
		}
		if len(a) == 0 {
			return nil, errors.New("timeout() precisa de uma duração em segundos (0 remove o castigo)")
		}
		secs, ok := a[0].(int64)
		if !ok {
			return nil, errors.New("timeout() precisa de um número inteiro de segundos")
		}
		var until any
		if secs > 0 {
			if secs > 28*86400 {
				return nil, errors.New("o Discord só permite castigos de até 28 dias")
			}
			until = time.Now().UTC().Add(time.Duration(secs) * time.Second).Format(time.RFC3339)
		}
		_, err := b.call(in, "PATCH", "/guilds/"+guildID+"/members/"+id,
			map[string]any{"communication_disabled_until": until}, reasonArg(a[1:], kw))
		return nil, err
	})
	return o
}

func (b *Bot) channelObj(id string) lang.Value {
	if id == "" {
		return nil
	}
	return b.newChannel(id)
}

func (b *Bot) newChannel(id string) *lang.Object {
	c := b.cachedChannel(id)
	o := lang.NewObject("Channel")
	o.Attrs["id"] = id
	o.Attrs["mention"] = "<#" + id + ">"
	o.Attrs["name"] = optStr(str(c, "name"))
	o.Attrs["guild_id"] = optStr(str(c, "guild_id"))
	o.Attrs["type"] = intv(c, "type")
	o.Repr = func() string {
		if n := str(c, "name"); n != "" {
			return "#" + n
		}
		return "<Channel " + id + ">"
	}
	o.Attrs["send"] = fn("send", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		return b.sendTo(in, id, a, kw, "")
	})
	o.Attrs["typing"] = fn("typing", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		_, err := b.call(in, "POST", "/channels/"+id+"/typing", nil, "")
		return nil, err
	})
	return o
}

func (b *Bot) guildObj(id string) lang.Value {
	if id == "" {
		return nil
	}
	g := b.cachedGuild(id)
	o := lang.NewObject("Guild")
	o.Attrs["id"] = id
	o.Attrs["name"] = optStr(str(g, "name"))
	o.Attrs["owner_id"] = optStr(str(g, "owner_id"))
	o.Attrs["member_count"] = intv(g, "member_count")
	if icon := str(g, "icon"); icon != "" {
		o.Attrs["icon_url"] = fmt.Sprintf("https://cdn.discordapp.com/icons/%s/%s.png", id, icon)
	} else {
		o.Attrs["icon_url"] = nil
	}
	name := str(g, "name")
	o.Repr = func() string {
		if name != "" {
			return name
		}
		return "<Guild " + id + ">"
	}
	o.Get = func(attr string) (lang.Value, bool) {
		if attr != "channels" {
			return nil, false
		}
		items := []lang.Value{}
		b.mu.RLock()
		var ids []string
		for cid, c := range b.channels {
			if str(c, "guild_id") == id {
				ids = append(ids, cid)
			}
		}
		b.mu.RUnlock()
		for _, cid := range ids {
			items = append(items, b.newChannel(cid))
		}
		return &lang.List{Items: items}, true
	}
	o.Attrs["get_member"] = fn("get_member", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		uid, err := argStr("get_member", a, 0)
		if err != nil {
			return nil, err
		}
		res, err := b.call(in, "GET", "/guilds/"+id+"/members/"+uid, nil, "")
		if err != nil {
			return nil, err
		}
		m, _ := res.(map[string]any)
		return b.memberObj(m, id), nil
	})
	o.Attrs["leave"] = fn("leave", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		_, err := b.call(in, "DELETE", "/users/@me/guilds/"+id, nil, "")
		return nil, err
	})
	return o
}

func (b *Bot) messageObj(m map[string]any) lang.Value {
	if m == nil {
		return nil
	}
	return b.newMessage(m)
}

func (b *Bot) newMessage(m map[string]any) *lang.Object {
	id, cid, gid := str(m, "id"), str(m, "channel_id"), str(m, "guild_id")
	o := lang.NewObject("Message")
	o.Attrs["id"] = id
	o.Attrs["content"] = str(m, "content")
	o.Attrs["channel_id"] = optStr(cid)
	o.Attrs["guild_id"] = optStr(gid)
	o.Attrs["channel"] = b.channelObj(cid)
	o.Attrs["guild"] = b.guildObj(gid)
	o.Attrs["timestamp"] = optStr(str(m, "timestamp"))
	author := sub(m, "author")
	o.Attrs["author"] = b.userObj(author)
	if mem := sub(m, "member"); mem != nil && author != nil {
		cp := make(map[string]any, len(mem)+1)
		for k, v := range mem {
			cp[k] = v
		}
		cp["user"] = author
		o.Attrs["member"] = b.memberObj(cp, gid)
	} else {
		o.Attrs["member"] = nil
	}
	mentions := []lang.Value{}
	for _, u := range sl(m, "mentions") {
		if um, ok := u.(map[string]any); ok {
			mentions = append(mentions, b.newUser(um))
		}
	}
	o.Attrs["mentions"] = &lang.List{Items: mentions}
	atts := []lang.Value{}
	for _, a := range sl(m, "attachments") {
		atts = append(atts, lang.FromGo(a))
	}
	o.Attrs["attachments"] = &lang.List{Items: atts}
	where := gid
	if where == "" {
		where = "@me"
	}
	o.Attrs["jump_url"] = fmt.Sprintf("https://discord.com/channels/%s/%s/%s", where, cid, id)
	o.Repr = func() string { return "<Message " + id + ">" }

	o.Attrs["reply"] = fn("reply", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		return b.sendTo(in, cid, a, kw, id)
	})
	o.Attrs["send"] = fn("send", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		return b.sendTo(in, cid, a, kw, "")
	})
	o.Attrs["delete"] = fn("delete", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		_, err := b.call(in, "DELETE", "/channels/"+cid+"/messages/"+id, nil, "")
		return nil, err
	})
	o.Attrs["react"] = fn("react", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		emoji, ok := firstString(a)
		if !ok || emoji == "" {
			return nil, errors.New("react() precisa de um emoji (ex.: \"👍\" ou \"nome:id\")")
		}
		_, err := b.call(in, "PUT", "/channels/"+cid+"/messages/"+id+"/reactions/"+url.PathEscape(emoji)+"/@me", nil, "")
		return nil, err
	})
	o.Attrs["edit"] = fn("edit", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		p, err := buildPayload(a, kw)
		if err != nil {
			return nil, err
		}
		delete(p, "allowed_mentions")
		res, err := b.call(in, "PATCH", "/channels/"+cid+"/messages/"+id, p, "")
		if err != nil {
			return nil, err
		}
		mm, _ := res.(map[string]any)
		return b.messageObj(mm), nil
	})
	return o
}

func firstString(a []lang.Value) (string, bool) {
	if len(a) == 0 {
		return "", false
	}
	s, ok := a[0].(string)
	return s, ok
}

func reactionArgs(b *Bot, d map[string]any) []lang.Value {
	emoji := sub(d, "emoji")
	o := lang.NewObject("Reaction")
	name := str(emoji, "name")
	o.Attrs["emoji"] = name
	o.Attrs["emoji_id"] = optStr(str(emoji, "id"))
	o.Attrs["user_id"] = str(d, "user_id")
	o.Attrs["message_id"] = str(d, "message_id")
	o.Attrs["channel_id"] = str(d, "channel_id")
	o.Attrs["channel"] = b.channelObj(str(d, "channel_id"))
	o.Attrs["guild_id"] = optStr(str(d, "guild_id"))
	o.Attrs["guild"] = b.guildObj(str(d, "guild_id"))
	if mem := sub(d, "member"); mem != nil {
		o.Attrs["member"] = b.memberObj(mem, str(d, "guild_id"))
	} else {
		o.Attrs["member"] = nil
	}
	o.Repr = func() string { return name }
	return []lang.Value{o}
}

// ---- o objeto global `bot` ----

func (b *Bot) botObj() *lang.Object {
	o := lang.NewObject("Bot")
	o.Repr = func() string { return "<Bot>" }
	o.Get = func(attr string) (lang.Value, bool) {
		switch attr {
		case "user":
			b.mu.RLock()
			u := b.self
			b.mu.RUnlock()
			return b.userObj(u), true
		case "guilds":
			b.mu.RLock()
			ids := make([]string, 0, len(b.guilds))
			for id := range b.guilds {
				ids = append(ids, id)
			}
			b.mu.RUnlock()
			items := make([]lang.Value, 0, len(ids))
			for _, id := range ids {
				items = append(items, b.guildObj(id))
			}
			return &lang.List{Items: items}, true
		case "guild_count":
			b.mu.RLock()
			defer b.mu.RUnlock()
			return int64(len(b.guilds)), true
		case "prefix":
			return b.prefix, true
		case "shard_count":
			return int64(max(b.totalShards, 1)), true
		}
		return nil, false
	}
	o.Attrs["send"] = fn("send", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		cid, err := argStr("bot.send", a, 0)
		if err != nil {
			return nil, err
		}
		return b.sendTo(in, cid, a[1:], kw, "")
	})
	o.Attrs["get_user"] = fn("get_user", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		id, err := argStr("get_user", a, 0)
		if err != nil {
			return nil, err
		}
		res, err := b.call(in, "GET", "/users/"+id, nil, "")
		if err != nil {
			return nil, err
		}
		u, _ := res.(map[string]any)
		return b.userObj(u), nil
	})
	o.Attrs["get_channel"] = fn("get_channel", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		id, err := argStr("get_channel", a, 0)
		if err != nil {
			return nil, err
		}
		return b.channelObj(id), nil
	})
	o.Attrs["get_guild"] = fn("get_guild", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		id, err := argStr("get_guild", a, 0)
		if err != nil {
			return nil, err
		}
		if b.cachedGuild(id) == nil {
			return nil, nil
		}
		return b.guildObj(id), nil
	})
	return o
}
