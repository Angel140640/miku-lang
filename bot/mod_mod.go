package bot

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"miku/lang"
)

const discordEpoch = 1420070400000

func snowflakeTime(id string) time.Time {
	n, _ := strconv.ParseUint(id, 10, 64)
	return time.UnixMilli(int64(n>>22) + discordEpoch)
}

type warnEntry struct {
	Reason string `json:"reason"`
	At     int64  `json:"at"`
	By     string `json:"by,omitempty"`
}

type warnStore struct {
	mu   sync.Mutex
	path string
	data map[string][]warnEntry
}

func (w *warnStore) load() error {
	if w.data != nil {
		return nil
	}
	w.data = map[string][]warnEntry{}
	raw, err := os.ReadFile(w.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("não consegui ler %s: %v", w.path, err)
	}
	if err := json.Unmarshal(raw, &w.data); err != nil {
		w.data = nil
		return fmt.Errorf("o arquivo %s não é um JSON válido", w.path)
	}
	return nil
}

func (w *warnStore) save() error {
	out, err := json.MarshalIndent(w.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := w.path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, w.path)
}

func applyMod(b *Bot) {
	in := b.In
	path := os.Getenv("MIKU_WARNS")
	if path == "" {
		path = "miku_warns.json"
	}
	ws := &warnStore{path: path}

	def := func(name string, f func(a []lang.Value, kw map[string]lang.Value) (lang.Value, error)) {
		in.DefineFunc(name, func(_ *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
			return f(a, kw)
		})
	}

	def("purge", func(a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		cid, err := argStr("purge", a, 0)
		if err != nil {
			return nil, err
		}
		n := int64(10)
		if len(a) > 1 {
			v, ok := a[1].(int64)
			if !ok {
				return nil, errors.New("purge(canal, quantidade): a quantidade precisa ser um número inteiro")
			}
			n = v
		}
		if n < 1 || n > 100 {
			return nil, errors.New("purge: a quantidade vai de 1 a 100")
		}
		res, err := b.call(in, "GET", "/channels/"+cid+"/messages?limit="+strconv.FormatInt(n, 10), nil, "")
		if err != nil {
			return nil, err
		}
		list, _ := res.([]any)
		var ids []any
		limit := time.Now().Add(-14*24*time.Hour + time.Hour)
		for _, m := range list {
			mm, _ := m.(map[string]any)
			if id := str(mm, "id"); id != "" && snowflakeTime(id).After(limit) { // o Discord só apaga em massa mensagens com menos de 14 dias
				ids = append(ids, id)
			}
		}
		switch len(ids) {
		case 0:
			return int64(0), nil
		case 1:
			_, err = b.call(in, "DELETE", "/channels/"+cid+"/messages/"+ids[0].(string), nil, "")
		default:
			_, err = b.call(in, "POST", "/channels/"+cid+"/messages/bulk-delete", map[string]any{"messages": ids}, reasonArg(nil, kw))
		}
		if err != nil {
			return nil, err
		}
		return int64(len(ids)), nil
	})

	setSend := func(name string, allow bool) {
		def(name, func(a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
			cid, err := argStr(name, a, 0)
			if err != nil {
				return nil, err
			}
			res, err := b.call(in, "GET", "/channels/"+cid, nil, "")
			if err != nil {
				return nil, err
			}
			ch, _ := res.(map[string]any)
			gid := str(ch, "guild_id")
			if gid == "" {
				return nil, fmt.Errorf("%s só funciona em canais de servidor", name)
			}
			var allowBits, denyBits uint64
			for _, o := range sl(ch, "permission_overwrites") {
				om, _ := o.(map[string]any)
				if str(om, "id") == gid {
					allowBits, _ = strconv.ParseUint(str(om, "allow"), 10, 64)
					denyBits, _ = strconv.ParseUint(str(om, "deny"), 10, 64)
				}
			}
			bit := permBits["send_messages"]
			if allow {
				denyBits &^= bit // volta ao padrão (herda do cargo)
			} else {
				allowBits &^= bit
				denyBits |= bit
			}
			_, err = b.call(in, "PUT", "/channels/"+cid+"/permissions/"+gid, map[string]any{
				"type": 0, "allow": strconv.FormatUint(allowBits, 10), "deny": strconv.FormatUint(denyBits, 10),
			}, reasonArg(a[1:], kw))
			return nil, err
		})
	}
	setSend("lock", false)
	setSend("unlock", true)

	def("slowmode", func(a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		cid, err := argStr("slowmode", a, 0)
		if err != nil {
			return nil, err
		}
		if len(a) < 2 {
			return nil, errors.New("slowmode(canal, segundos) precisa dos segundos (0 desliga)")
		}
		n, ok := a[1].(int64)
		if !ok || n < 0 || n > 21600 {
			return nil, errors.New("slowmode: os segundos vão de 0 a 21600")
		}
		_, err = b.call(in, "PATCH", "/channels/"+cid, map[string]any{"rate_limit_per_user": n}, "")
		return nil, err
	})

	def("unban", func(a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		gid, err := argStr("unban", a, 0)
		if err != nil {
			return nil, err
		}
		uid, err := argStr("unban", a, 1)
		if err != nil {
			return nil, err
		}
		reason := ""
		if len(a) > 2 {
			reason = lang.Str(a[2])
		}
		if r, ok := kw["reason"].(string); ok {
			reason = r
		}
		_, err = b.call(in, "DELETE", "/guilds/"+gid+"/bans/"+url.PathEscape(uid), nil, reason)
		return nil, err
	})

	key := func(name string, a []lang.Value) (string, error) {
		g, err := argStr(name, a, 0)
		if err != nil {
			return "", err
		}
		u, err := argStr(name, a, 1)
		if err != nil {
			return "", err
		}
		return g + ":" + u, nil
	}
	def("warn", func(a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		k, err := key("warn", a)
		if err != nil {
			return nil, err
		}
		reason := "sem motivo"
		if len(a) > 2 {
			reason = lang.Str(a[2])
		}
		if r, ok := kw["reason"].(string); ok {
			reason = r
		}
		by := ""
		if v, ok := kw["by"]; ok && v != nil {
			by, _ = argStr("warn", []lang.Value{v}, 0)
		}
		ws.mu.Lock()
		defer ws.mu.Unlock()
		if err := ws.load(); err != nil {
			return nil, err
		}
		if len([]rune(reason)) > 300 {
			reason = string([]rune(reason)[:300])
		}
		ws.data[k] = append(ws.data[k], warnEntry{Reason: reason, At: time.Now().Unix(), By: by})
		if err := ws.save(); err != nil {
			return nil, err
		}
		return int64(len(ws.data[k])), nil
	})
	def("warns", func(a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		k, err := key("warns", a)
		if err != nil {
			return nil, err
		}
		ws.mu.Lock()
		defer ws.mu.Unlock()
		if err := ws.load(); err != nil {
			return nil, err
		}
		out := []lang.Value{}
		for _, w := range ws.data[k] {
			d := lang.NewDict()
			d.SetStr("reason", w.Reason)
			d.SetStr("at", w.At)
			d.SetStr("by", optStr(w.By))
			out = append(out, d)
		}
		return &lang.List{Items: out}, nil
	})
	def("clear_warns", func(a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		k, err := key("clear_warns", a)
		if err != nil {
			return nil, err
		}
		ws.mu.Lock()
		defer ws.mu.Unlock()
		if err := ws.load(); err != nil {
			return nil, err
		}
		n := len(ws.data[k])
		delete(ws.data, k)
		return int64(n), ws.save()
	})
}
