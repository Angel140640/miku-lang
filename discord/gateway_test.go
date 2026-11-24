package discord

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"miku/ws"
)

func writeJSON(c *ws.Conn, v any) {
	b, _ := json.Marshal(v)
	_ = c.WriteText(b)
}

func TestReconnectResumesSession(t *testing.T) {
	var mu sync.Mutex
	var ops []string
	var resume map[string]any
	conns := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := ws.Accept(w, r)
		if err != nil {
			return
		}
		defer c.Close()
		mu.Lock()
		conns++
		n := conns
		mu.Unlock()
		writeJSON(c, map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 45000}})
		_, raw, err := c.ReadMessage()
		if err != nil {
			return
		}
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		mu.Lock()
		ops = append(ops, string(rune('0'+int(p["op"].(float64)))))
		if p["op"] == float64(6) {
			resume, _ = p["d"].(map[string]any)
		}
		mu.Unlock()
		if n == 1 {
			writeJSON(c, map[string]any{"op": 0, "s": 5, "t": "READY", "d": map[string]any{"session_id": "S1", "resume_gateway_url": "http://" + r.Host}})
			writeJSON(c, map[string]any{"op": 0, "s": 6, "t": "MESSAGE_CREATE", "d": map[string]any{"content": "a"}})
			time.Sleep(50 * time.Millisecond)
			writeJSON(c, map[string]any{"op": 7, "d": nil}) // "reconecte"
		} else {
			writeJSON(c, map[string]any{"op": 0, "s": 7, "t": "RESUMED", "d": map[string]any{}})
			writeJSON(c, map[string]any{"op": 0, "s": 8, "t": "MESSAGE_CREATE", "d": map[string]any{"content": "b"}})
		}
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	var emu sync.Mutex
	var got []string
	g := &Gateway{
		Token: "tok", Intents: 1, ShardCount: 1, URL: srv.URL,
		OnEvent: func(ev Event) {
			emu.Lock()
			defer emu.Unlock()
			got = append(got, ev.Name+":"+str(ev.Data, "content"))
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		emu.Lock()
		ok := strings.Contains(strings.Join(got, ","), "MESSAGE_CREATE:b")
		emu.Unlock()
		if ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if strings.Join(ops, "") != "26" {
		t.Fatalf("ops enviados = %v, queria identify(2) e depois resume(6)", ops)
	}
	if resume["session_id"] != "S1" || resume["seq"] != float64(6) || resume["token"] != "tok" {
		t.Errorf("resume errado: %v", resume)
	}
}

func str(m map[string]any, k string) string { v, _ := m[k].(string); return v }

func TestPrivilegedIntentsIsFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := ws.Accept(w, r)
		if err != nil {
			return
		}
		defer c.Close()
		writeJSON(c, map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 45000}})
		if _, _, err := c.ReadMessage(); err != nil {
			return
		}
		_ = c.WriteClose(4014, "Disallowed intent(s)")
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	g := &Gateway{Token: "tok", Intents: AllIntents(), ShardCount: 1, URL: srv.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := g.Run(ctx)
	var fatal *FatalError
	if !errors.As(err, &fatal) || !strings.Contains(err.Error(), "Message Content Intent") {
		t.Fatalf("erro = %v, queria FatalError sobre intents privilegiadas", err)
	}
}

func TestBadTokenIsFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := ws.Accept(w, r)
		if err != nil {
			return
		}
		defer c.Close()
		writeJSON(c, map[string]any{"op": 10, "d": map[string]any{"heartbeat_interval": 45000}})
		if _, _, err := c.ReadMessage(); err != nil {
			return
		}
		_ = c.WriteClose(4004, "Authentication failed")
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	g := &Gateway{Token: "ruim", Intents: 1, ShardCount: 1, URL: srv.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := g.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "token inválido") {
		t.Fatalf("erro = %v", err)
	}
}

func TestRESTRateLimitRetry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"retry_after":0.05,"message":"rate limited"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bot tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := NewClient("tok")
	c.Base = srv.URL
	res, err := c.Get("/x")
	if err != nil || calls != 2 {
		t.Fatalf("res=%v err=%v calls=%d", res, err, calls)
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"code":50013,"message":"Missing Permissions"}`))
	}))
	defer srv2.Close()
	c.Base = srv2.URL
	_, err = c.Post("/y", map[string]any{"a": 1})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != 50013 || ae.Status != 403 {
		t.Fatalf("erro = %v", err)
	}
}
