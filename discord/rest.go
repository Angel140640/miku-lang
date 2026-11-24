// Package discord fala com o Discord: API REST e gateway (WebSocket), só com a stdlib.
package discord

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

var (
	APIBase    = "https://discord.com/api/v10"
	GatewayURL = "wss://gateway.discord.gg"
)

const userAgent = "DiscordBot (https://github.com/arc4ng3lane/miku, 0.2.0) MikuLang"

type APIError struct {
	Status  int
	Code    int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Discord API %d: %s (código %d)", e.Status, e.Message, e.Code)
}

type Client struct {
	Token string
	Base  string
	HTTP  *http.Client

	mu    sync.Mutex
	waits map[string]time.Time
}

func NewClient(token string) *Client {
	return &Client{
		Token: token,
		Base:  APIBase,
		HTTP:  &http.Client{Timeout: 30 * time.Second},
		waits: map[string]time.Time{},
	}
}

func (c *Client) Get(path string) (any, error)          { return c.Do("GET", path, nil) }
func (c *Client) Post(path string, b any) (any, error)  { return c.Do("POST", path, b) }
func (c *Client) Patch(path string, b any) (any, error) { return c.Do("PATCH", path, b) }
func (c *Client) Put(path string, b any) (any, error)   { return c.Do("PUT", path, b) }
func (c *Client) Delete(path string) (any, error)       { return c.Do("DELETE", path, nil) }

func (c *Client) wait(key string) {
	c.mu.Lock()
	until := c.waits[key]
	c.mu.Unlock()
	if d := time.Until(until); d > 0 {
		time.Sleep(d)
	}
}

func (c *Client) note(key string, h http.Header) {
	if h.Get("X-RateLimit-Remaining") != "0" {
		return
	}
	secs, err := strconv.ParseFloat(h.Get("X-RateLimit-Reset-After"), 64)
	if err != nil {
		return
	}
	c.mu.Lock()
	c.waits[key] = time.Now().Add(time.Duration(secs * float64(time.Second)))
	c.mu.Unlock()
}

// Do faz uma requisição JSON e devolve o corpo decodificado (map, slice ou nil).
func (c *Client) Do(method, path string, body any) (any, error) {
	return c.DoH(method, path, body, nil)
}

// DoH é o Do com cabeçalhos extras (ex.: X-Audit-Log-Reason).
func (c *Client) DoH(method, path string, body any, headers map[string]string) (any, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	key := method + " " + path
	for attempt := 0; attempt < 5; attempt++ {
		c.wait(key)
		req, err := http.NewRequest(method, c.Base+path, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bot "+c.Token)
		req.Header.Set("User-Agent", userAgent)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		c.note(key, resp.Header)

		var out any
		if len(data) > 0 {
			_ = json.Unmarshal(data, &out)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			retry := 1.0
			if m, ok := out.(map[string]any); ok {
				if f, ok := m["retry_after"].(float64); ok {
					retry = f
				}
			} else if f, err := strconv.ParseFloat(resp.Header.Get("Retry-After"), 64); err == nil {
				retry = f
			}
			time.Sleep(time.Duration(retry*float64(time.Second)) + 50*time.Millisecond)
			continue
		}
		if resp.StatusCode >= 400 {
			e := &APIError{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
			if m, ok := out.(map[string]any); ok {
				if s, ok := m["message"].(string); ok {
					e.Message = s
				}
				if f, ok := m["code"].(float64); ok {
					e.Code = int(f)
				}
			}
			return nil, e
		}
		return out, nil
	}
	return nil, errors.New("o Discord limitou as requisições (rate limit) várias vezes seguidas")
}
