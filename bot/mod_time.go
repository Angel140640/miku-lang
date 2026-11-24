package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // fusos horários embutidos: funciona sem nada instalado no sistema

	"miku/lang"
)

var ptMonths = []string{"janeiro", "fevereiro", "março", "abril", "maio", "junho", "julho", "agosto", "setembro", "outubro", "novembro", "dezembro"}
var ptDays = []string{"domingo", "segunda-feira", "terça-feira", "quarta-feira", "quinta-feira", "sexta-feira", "sábado"}

// strftime implementa um subconjunto do strftime do Python, com nomes em português (%B %A %b %a).
func strftime(t time.Time, f string) (string, error) {
	var sb strings.Builder
	rs := []rune(f)
	for i := 0; i < len(rs); i++ {
		if rs[i] != '%' {
			sb.WriteRune(rs[i])
			continue
		}
		i++
		if i >= len(rs) {
			return "", errors.New("format_time: '%' sobrando no fim do formato")
		}
		switch rs[i] {
		case 'Y':
			fmt.Fprintf(&sb, "%04d", t.Year())
		case 'y':
			fmt.Fprintf(&sb, "%02d", t.Year()%100)
		case 'm':
			fmt.Fprintf(&sb, "%02d", int(t.Month()))
		case 'd':
			fmt.Fprintf(&sb, "%02d", t.Day())
		case 'H':
			fmt.Fprintf(&sb, "%02d", t.Hour())
		case 'I':
			h := t.Hour() % 12
			if h == 0 {
				h = 12
			}
			fmt.Fprintf(&sb, "%02d", h)
		case 'M':
			fmt.Fprintf(&sb, "%02d", t.Minute())
		case 'S':
			fmt.Fprintf(&sb, "%02d", t.Second())
		case 'p':
			if t.Hour() < 12 {
				sb.WriteString("AM")
			} else {
				sb.WriteString("PM")
			}
		case 'B':
			sb.WriteString(ptMonths[t.Month()-1])
		case 'b':
			sb.WriteString(ptMonths[t.Month()-1][:3])
		case 'A':
			sb.WriteString(ptDays[t.Weekday()])
		case 'a':
			sb.WriteString(ptDays[t.Weekday()][:3])
		case 'j':
			fmt.Fprintf(&sb, "%03d", t.YearDay())
		case 'Z':
			n, _ := t.Zone()
			sb.WriteString(n)
		case 'z':
			sb.WriteString(t.Format("-0700"))
		case 's':
			sb.WriteString(strconv.FormatInt(t.Unix(), 10))
		case '%':
			sb.WriteByte('%')
		default:
			return "", fmt.Errorf("format_time: código '%%%c' não suportado (use Y m d H M S I p B b A a j Z z s %%)", rs[i])
		}
	}
	return sb.String(), nil
}

func numArg(name string, v lang.Value) (float64, error) {
	switch x := v.(type) {
	case int64:
		return float64(x), nil
	case float64:
		return x, nil
	}
	return 0, fmt.Errorf("%s: esperava um número, veio %s", name, lang.Str(v))
}

// parseDuration lê "1h30m", "45s", "2d", "1w" (ou só um número = segundos).
func parseDuration(s string) (int64, error) {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	if s == "" {
		return 0, errors.New("duração vazia")
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	units := map[byte]int64{'s': 1, 'm': 60, 'h': 3600, 'd': 86400, 'w': 604800}
	var total int64
	num := ""
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			num += string(c)
			continue
		}
		mult, ok := units[c]
		if !ok || num == "" {
			return 0, fmt.Errorf("duração inválida '%s' (exemplos: 30s, 10m, 2h, 1d, 1h30m)", s)
		}
		n, _ := strconv.ParseInt(num, 10, 64)
		total += n * mult
		num = ""
	}
	if num != "" {
		return 0, fmt.Errorf("duração inválida '%s' (faltou a unidade: s, m, h, d ou w)", s)
	}
	return total, nil
}

func humanDuration(sec int64) string {
	if sec <= 0 {
		return "0s"
	}
	parts := []string{}
	for _, u := range []struct {
		n    int64
		name string
	}{{604800, "sem"}, {86400, "d"}, {3600, "h"}, {60, "min"}, {1, "s"}} {
		if sec >= u.n {
			parts = append(parts, fmt.Sprintf("%d%s", sec/u.n, u.name))
			sec %= u.n
		}
	}
	if len(parts) > 3 {
		parts = parts[:3]
	}
	return strings.Join(parts, " ")
}

type timers struct {
	mu     sync.Mutex
	next   int64
	cancel map[int64]context.CancelFunc
}

func applyTime(b *Bot) {
	tm := &timers{cancel: map[int64]context.CancelFunc{}}
	in := b.In
	add := func(name string, repeat bool, a []lang.Value) (lang.Value, error) {
		if len(a) != 2 {
			return nil, fmt.Errorf("%s(segundos, função) precisa de 2 argumentos", name)
		}
		secs, err := numArg(name, a[0])
		if err != nil {
			return nil, err
		}
		if secs < 1 && repeat {
			return nil, errors.New("every: o intervalo mínimo é 1 segundo")
		}
		if secs < 0 {
			return nil, fmt.Errorf("%s: o tempo não pode ser negativo", name)
		}
		fnv := a[1]
		switch fnv.(type) {
		case *lang.Func, *lang.Builtin:
		default:
			return nil, fmt.Errorf("%s: o segundo argumento precisa ser uma função (def ou lambda)", name)
		}
		tm.mu.Lock()
		tm.next++
		id := tm.next
		tm.mu.Unlock()
		d := time.Duration(secs * float64(time.Second))
		run := func(ctx context.Context) {
			cctx, cancel := context.WithCancel(ctx)
			tm.mu.Lock()
			tm.cancel[id] = cancel
			tm.mu.Unlock()
			defer func() {
				cancel()
				tm.mu.Lock()
				delete(tm.cancel, id)
				tm.mu.Unlock()
			}()
			tick := func() {
				_, err := in.CallLocked(fnv, nil, nil)
				b.logHandlerErr(name, err)
			}
			if !repeat {
				select {
				case <-time.After(d):
					tick()
				case <-cctx.Done():
				}
				return
			}
			t := time.NewTicker(d)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					tick()
				case <-cctx.Done():
					return
				}
			}
		}
		b.startTask(run)
		return id, nil
	}
	in.DefineFunc("every", func(_ *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		return add("every", true, a)
	})
	in.DefineFunc("after", func(_ *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		return add("after", false, a)
	})
	in.DefineFunc("cancel", func(_ *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		if len(a) != 1 {
			return nil, errors.New("cancel(id) precisa do id devolvido por every/after")
		}
		id, ok := a[0].(int64)
		if !ok {
			return nil, errors.New("cancel(id): o id precisa ser um número")
		}
		tm.mu.Lock()
		c, found := tm.cancel[id]
		tm.mu.Unlock()
		if found {
			c()
		}
		return found, nil
	})
	in.DefineFunc("format_time", func(_ *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		ts := float64(time.Now().Unix())
		if len(a) > 0 && a[0] != nil {
			v, err := numArg("format_time", a[0])
			if err != nil {
				return nil, err
			}
			ts = v
		}
		f := "%d/%m/%Y %H:%M"
		if len(a) > 1 {
			f = lang.Str(a[1])
		}
		if v, ok := kw["fmt"].(string); ok {
			f = v
		}
		tz := "UTC"
		if v, ok := kw["tz"].(string); ok {
			tz = v
		} else if len(a) > 2 {
			tz = lang.Str(a[2])
		}
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return nil, fmt.Errorf("format_time: fuso '%s' desconhecido (ex.: \"America/Sao_Paulo\", \"UTC\")", tz)
		}
		sec := int64(ts)
		return strftime(time.Unix(sec, int64((ts-float64(sec))*1e9)).In(loc), f)
	})
	in.DefineFunc("parse_duration", func(_ *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		if len(a) != 1 {
			return nil, errors.New("parse_duration(texto) precisa de 1 argumento")
		}
		s, ok := a[0].(string)
		if !ok {
			return nil, errors.New("parse_duration: precisa de um texto como \"1h30m\"")
		}
		n, err := parseDuration(s)
		if err != nil {
			return nil, fmt.Errorf("parse_duration: %v", err)
		}
		return n, nil
	})
	in.DefineFunc("human_duration", func(_ *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		if len(a) != 1 {
			return nil, errors.New("human_duration(segundos) precisa de 1 argumento")
		}
		n, err := numArg("human_duration", a[0])
		if err != nil {
			return nil, err
		}
		return humanDuration(int64(n)), nil
	})
	in.DefineFunc("discord_time", func(_ *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		ts := float64(time.Now().Unix())
		if len(a) > 0 && a[0] != nil {
			v, err := numArg("discord_time", a[0])
			if err != nil {
				return nil, err
			}
			ts = v
		}
		style := "f"
		if len(a) > 1 {
			style = lang.Str(a[1])
		}
		if v, ok := kw["style"].(string); ok {
			style = v
		}
		if len(style) != 1 || !strings.Contains("tTdDfFR", style) {
			return nil, errors.New("discord_time: estilo precisa ser um de t T d D f F R")
		}
		return fmt.Sprintf("<t:%d:%s>", int64(ts), style), nil
	})
}
