package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"miku/ws"
)

// Intents (bits do gateway).
var IntentBits = map[string]int{
	"guilds":           1 << 0,
	"members":          1 << 1,
	"moderation":       1 << 2,
	"emojis":           1 << 3,
	"integrations":     1 << 4,
	"webhooks":         1 << 5,
	"invites":          1 << 6,
	"voice_states":     1 << 7,
	"presences":        1 << 8,
	"guild_messages":   1 << 9,
	"guild_reactions":  1 << 10,
	"guild_typing":     1 << 11,
	"dm_messages":      1 << 12,
	"dm_reactions":     1 << 13,
	"dm_typing":        1 << 14,
	"message_content":  1 << 15,
	"scheduled_events": 1 << 16,
	"automod_config":   1 << 20,
	"automod_exec":     1 << 21,
}

// Atalhos que juntam várias intents.
var IntentGroups = map[string][]string{
	"messages":  {"guild_messages", "dm_messages"},
	"reactions": {"guild_reactions", "dm_reactions"},
	"typing":    {"guild_typing", "dm_typing"},
}

const privilegedMask = 1<<1 | 1<<8 | 1<<15

// DefaultIntents: tudo que não é privilegiado + message_content (comandos com prefixo).
func DefaultIntents() int {
	all := 0
	for _, b := range IntentBits {
		all |= b
	}
	return all&^privilegedMask | IntentBits["message_content"]
}

func AllIntents() int {
	all := 0
	for _, b := range IntentBits {
		all |= b
	}
	return all
}

type Event struct {
	Name  string
	Data  map[string]any
	Shard int
}

// FatalError: erro que não adianta tentar de novo (token inválido, intents recusadas...).
type FatalError struct{ Msg string }

func (e *FatalError) Error() string { return e.Msg }

type Gateway struct {
	Token      string
	Intents    int
	ShardID    int
	ShardCount int
	URL        string
	Presence   map[string]any
	OnEvent    func(Event)
	Log        func(format string, a ...any)

	mu        sync.Mutex
	sessionID string
	resumeURL string
	seq       int64
	hasSeq    bool
	acked     atomic.Bool
	ready     chan struct{}
	readyOnce sync.Once
}

type frame struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
	S  *int64          `json:"s"`
	T  string          `json:"t"`
}

func (g *Gateway) logf(format string, a ...any) {
	if g.Log != nil {
		g.Log(format, a...)
	}
}

func (g *Gateway) readyChan() chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ready == nil {
		g.ready = make(chan struct{})
	}
	return g.ready
}

// Ready é fechado quando o shard recebe o primeiro READY.
func (g *Gateway) Ready() <-chan struct{} { return g.readyChan() }

func (g *Gateway) markReady() {
	ch := g.readyChan()
	g.readyOnce.Do(func() { close(ch) })
}

// Run mantém o shard conectado (reconectando sozinho) até o contexto acabar ou um erro fatal.
func (g *Gateway) Run(ctx context.Context) error {
	if g.URL == "" {
		g.URL = GatewayURL
	}
	g.readyChan()
	backoff := time.Second
	for {
		started := time.Now()
		err := g.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		var fatal *FatalError
		if errors.As(err, &fatal) {
			return err
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		g.logf("shard %d: conexão perdida (%v); reconectando em %s", g.ShardID, err, backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (g *Gateway) send(conn *ws.Conn, op int, d any) error {
	b, err := json.Marshal(map[string]any{"op": op, "d": d})
	if err != nil {
		return err
	}
	return conn.WriteText(b)
}

func (g *Gateway) sendHeartbeat(conn *ws.Conn) error {
	g.mu.Lock()
	var d any
	if g.hasSeq {
		d = g.seq
	}
	g.mu.Unlock()
	return g.send(conn, 1, d)
}

func (g *Gateway) heartbeatLoop(conn *ws.Conn, every time.Duration, done <-chan struct{}) {
	if every <= 0 {
		every = 41250 * time.Millisecond
	}
	t := time.NewTimer(time.Duration(rand.Float64() * float64(every)))
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
		}
		if !g.acked.Load() { // o Discord parou de responder: conexão zumbi
			conn.Close()
			return
		}
		g.acked.Store(false)
		if err := g.sendHeartbeat(conn); err != nil {
			return
		}
		t.Reset(every)
	}
}

func (g *Gateway) session(ctx context.Context) error {
	g.mu.Lock()
	url := g.URL
	resuming := g.sessionID != ""
	if resuming && g.resumeURL != "" {
		url = g.resumeURL
	}
	g.mu.Unlock()

	conn, err := ws.Dial(url + "/?v=10&encoding=json")
	if err != nil {
		return err
	}
	defer conn.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()

	conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return g.mapClose(err)
	}
	var hello frame
	if err := json.Unmarshal(raw, &hello); err != nil || hello.Op != 10 {
		return fmt.Errorf("esperava HELLO do gateway, recebi: %.80s", raw)
	}
	var hd struct {
		Interval int `json:"heartbeat_interval"`
	}
	_ = json.Unmarshal(hello.D, &hd)
	conn.SetReadDeadline(time.Time{})

	g.acked.Store(true)
	go g.heartbeatLoop(conn, time.Duration(hd.Interval)*time.Millisecond, done)

	if resuming {
		g.mu.Lock()
		d := map[string]any{"token": g.Token, "session_id": g.sessionID, "seq": g.seq}
		g.mu.Unlock()
		err = g.send(conn, 6, d)
	} else {
		id := map[string]any{
			"token":   g.Token,
			"intents": g.Intents,
			"properties": map[string]string{
				"os": runtime.GOOS, "browser": "miku", "device": "miku",
			},
			"shard": []int{g.ShardID, max(g.ShardCount, 1)},
		}
		if g.Presence != nil {
			id["presence"] = g.Presence
		}
		err = g.send(conn, 2, id)
	}
	if err != nil {
		return err
	}

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return g.mapClose(err)
		}
		var f frame
		if err := json.Unmarshal(raw, &f); err != nil {
			continue
		}
		switch f.Op {
		case 0:
			if f.S != nil {
				g.mu.Lock()
				g.seq, g.hasSeq = *f.S, true
				g.mu.Unlock()
			}
			var data map[string]any
			_ = json.Unmarshal(f.D, &data)
			switch f.T {
			case "READY":
				g.mu.Lock()
				g.sessionID, _ = data["session_id"].(string)
				g.resumeURL, _ = data["resume_gateway_url"].(string)
				g.mu.Unlock()
				g.markReady()
			case "RESUMED":
				g.logf("shard %d: sessão retomada", g.ShardID)
				continue
			}
			if g.OnEvent != nil {
				g.OnEvent(Event{Name: f.T, Data: data, Shard: g.ShardID})
			}
		case 1:
			if err := g.sendHeartbeat(conn); err != nil {
				return err
			}
		case 7:
			return errors.New("o Discord pediu para reconectar")
		case 9:
			var resumable bool
			_ = json.Unmarshal(f.D, &resumable)
			if !resumable {
				g.resetSession()
			}
			time.Sleep(time.Second + time.Duration(rand.Intn(4000))*time.Millisecond)
			return errors.New("sessão inválida")
		case 11:
			g.acked.Store(true)
		}
	}
}

func (g *Gateway) resetSession() {
	g.mu.Lock()
	g.sessionID, g.resumeURL, g.hasSeq = "", "", false
	g.mu.Unlock()
}

func (g *Gateway) mapClose(err error) error {
	var ce *ws.CloseError
	if !errors.As(err, &ce) {
		return err
	}
	switch ce.Code {
	case 4004:
		return &FatalError{"token inválido — o Discord recusou o login"}
	case 4010, 4011:
		return &FatalError{"configuração de shards inválida (o bot precisa de mais shards)"}
	case 4012:
		return &FatalError{"versão do gateway não suportada"}
	case 4013:
		return &FatalError{"intents inválidas"}
	case 4014:
		return &FatalError{"intents privilegiadas desligadas — ative 'Message Content Intent' (e as outras que usar) em Developer Portal → Bot"}
	case 4007, 4009:
		g.resetSession()
	}
	return err
}
