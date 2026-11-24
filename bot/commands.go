package bot

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"miku/lang"
)

type argTok struct {
	text  string
	start int
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// tokenize separa argumentos por espaço; "texto entre aspas" vira um argumento só.
func tokenize(s string) []argTok {
	var toks []argTok
	i := 0
	for i < len(s) {
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i >= len(s) {
			break
		}
		start := i
		if s[i] == '"' {
			if j := strings.IndexByte(s[i+1:], '"'); j >= 0 {
				toks = append(toks, argTok{s[i+1 : i+1+j], start})
				i += j + 2
				continue
			}
		}
		j := i
		for j < len(s) && !isSpace(s[j]) {
			j++
		}
		toks = append(toks, argTok{s[i:j], start})
		i = j
	}
	return toks
}

var idRe = regexp.MustCompile(`^(?:<(?:@!?|#|@&)(\d{15,25})>|(\d{15,25}))$`)

func parseID(raw string) string {
	m := idRe.FindStringSubmatch(raw)
	if m == nil {
		return ""
	}
	if m[1] != "" {
		return m[1]
	}
	return m[2]
}

func (b *Bot) usage(c *command, used string) string {
	var parts []string
	for _, p := range c.params {
		switch {
		case p.Kind == lang.PVar:
			parts = append(parts, "["+p.Name+"...]")
		case p.Kind == lang.PRest:
			parts = append(parts, "<"+p.Name+"...>")
		case p.Default != nil:
			parts = append(parts, "["+p.Name+"]")
		default:
			parts = append(parts, "<"+p.Name+">")
		}
	}
	return strings.TrimSpace(b.prefix + strings.ToLower(used) + " " + strings.Join(parts, " "))
}

func (b *Bot) convertArg(p lang.Param, raw string, m map[string]any) (lang.Value, error) {
	switch p.Type {
	case "", "str", "string", "any":
		return raw, nil
	case "int":
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("`%s` precisa ser um número inteiro (você mandou `%s`).", p.Name, raw)
		}
		return v, nil
	case "float", "number":
		v, err := strconv.ParseFloat(strings.Replace(raw, ",", ".", 1), 64)
		if err != nil {
			return nil, fmt.Errorf("`%s` precisa ser um número (você mandou `%s`).", p.Name, raw)
		}
		return v, nil
	case "bool":
		switch strings.ToLower(raw) {
		case "true", "sim", "yes", "s", "y", "1", "on", "ligado":
			return true, nil
		case "false", "nao", "não", "no", "n", "0", "off", "desligado":
			return false, nil
		}
		return nil, fmt.Errorf("`%s` precisa ser sim ou não (você mandou `%s`).", p.Name, raw)
	case "user", "member":
		id := parseID(raw)
		if id == "" {
			return nil, fmt.Errorf("`%s` precisa ser uma menção (@pessoa) ou um id (você mandou `%s`).", p.Name, raw)
		}
		for _, u := range sl(m, "mentions") {
			if um, ok := u.(map[string]any); ok && str(um, "id") == id {
				return b.newUser(um), nil
			}
		}
		if b.Rest == nil {
			return nil, fmt.Errorf("não consegui buscar o usuário `%s`.", raw)
		}
		res, err := b.Rest.Get("/users/" + id)
		if err != nil {
			return nil, fmt.Errorf("não achei o usuário `%s`.", raw)
		}
		u, _ := res.(map[string]any)
		return b.userObj(u), nil
	case "channel":
		id := parseID(raw)
		if id == "" {
			return nil, fmt.Errorf("`%s` precisa ser um canal (#canal) ou um id (você mandou `%s`).", p.Name, raw)
		}
		return b.channelObj(id), nil
	case "role":
		id := parseID(raw)
		if id == "" {
			return nil, fmt.Errorf("`%s` precisa ser um cargo (@cargo) ou um id (você mandou `%s`).", p.Name, raw)
		}
		return id, nil
	}
	return raw, nil
}

func (b *Bot) bindArgs(c *command, used, args string, m map[string]any) (map[string]lang.Value, error) {
	toks := tokenize(args)
	vars := make(map[string]lang.Value, len(c.params))
	ti := 0
	for pi, p := range c.params {
		switch {
		case p.Kind == lang.PVar:
			rest := []lang.Value{}
			for ; ti < len(toks); ti++ {
				v, err := b.convertArg(p, toks[ti].text, m)
				if err != nil {
					return nil, err
				}
				rest = append(rest, v)
			}
			vars[p.Name] = &lang.List{Items: rest, Tuple: true}
		case p.Kind == lang.PRest:
			if ti >= len(toks) {
				return nil, fmt.Errorf("faltou o argumento `%s`. Uso: `%s`", p.Name, b.usage(c, used))
			}
			v, err := b.convertArg(p, strings.TrimSpace(args[toks[ti].start:]), m)
			if err != nil {
				return nil, err
			}
			vars[p.Name] = v
			ti = len(toks)
		case ti < len(toks):
			v, err := b.convertArg(p, toks[ti].text, m)
			if err != nil {
				return nil, err
			}
			vars[p.Name] = v
			ti++
		case p.Default != nil:
			vars[p.Name] = c.defaults[pi]
		default:
			return nil, fmt.Errorf("faltou o argumento `%s`. Uso: `%s`", p.Name, b.usage(c, used))
		}
	}
	return vars, nil
}

func (b *Bot) handleCommand(m map[string]any) {
	if flag(sub(m, "author"), "bot") {
		return
	}
	content := str(m, "content")
	if !strings.HasPrefix(content, b.prefix) {
		return
	}
	rest := strings.TrimSpace(content[len(b.prefix):])
	if rest == "" {
		return
	}
	name, args := rest, ""
	if i := strings.IndexAny(rest, " \t\n"); i >= 0 {
		name, args = rest[:i], strings.TrimSpace(rest[i:])
	}
	cmd := b.commands[strings.ToLower(name)]
	if cmd == nil {
		return
	}
	go b.runCommand(cmd, name, m, args)
}

func (b *Bot) runCommand(c *command, used string, m map[string]any, args string) {
	cid := str(m, "channel_id")
	vars, err := b.bindArgs(c, used, args, m)
	if err != nil {
		b.sendPlain(cid, err.Error())
		return
	}
	ctx := b.newMessage(m)
	ctx.Type = "Context"
	ctx.Attrs["message"] = ctx
	ctx.Attrs["prefix"] = b.prefix
	ctx.Attrs["command"] = c.name
	vars["ctx"] = ctx
	vars["message"] = ctx
	if err := b.In.RunHandler(c.body, vars); err != nil {
		b.logHandlerErr("comando "+c.name, err)
		b.sendPlain(cid, "⚠️ Deu erro nesse comando. Quem cuida do bot pode ver os detalhes no console.")
	}
}
