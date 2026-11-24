// Package ws é um WebSocket mínimo (RFC 6455), cliente e servidor, só com a stdlib.
package ws

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	OpContinuation = 0x0
	OpText         = 0x1
	OpBinary       = 0x2
	OpClose        = 0x8
	OpPing         = 0x9
	OpPong         = 0xA
)

const (
	guid       = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	maxMessage = 32 << 20
)

// CloseError é devolvido por ReadMessage quando o outro lado fecha a conexão.
type CloseError struct {
	Code   int
	Reason string
}

func (e *CloseError) Error() string {
	return fmt.Sprintf("websocket fechado (%d) %s", e.Code, e.Reason)
}

type Conn struct {
	nc     net.Conn
	br     *bufio.Reader
	wmu    sync.Mutex
	client bool
}

func acceptKey(key string) string {
	sum := sha1.Sum([]byte(key + guid))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// Dial abre uma conexão ws:// ou wss://.
func Dial(rawurl string) (*Conn, error) {
	u, err := url.Parse(rawurl)
	if err != nil {
		return nil, err
	}
	var nc net.Conn
	switch u.Scheme {
	case "wss", "https":
		host := u.Host
		if u.Port() == "" {
			host = net.JoinHostPort(u.Hostname(), "443")
		}
		d := &net.Dialer{Timeout: 15 * time.Second}
		nc, err = tls.DialWithDialer(d, "tcp", host, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
	case "ws", "http":
		host := u.Host
		if u.Port() == "" {
			host = net.JoinHostPort(u.Hostname(), "80")
		}
		nc, err = net.DialTimeout("tcp", host, 15*time.Second)
	default:
		return nil, fmt.Errorf("esquema de URL inválido: %q", u.Scheme)
	}
	if err != nil {
		return nil, err
	}

	raw := make([]byte, 16)
	if _, err = rand.Read(raw); err != nil {
		nc.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(raw)
	req := "GET " + u.RequestURI() + " HTTP/1.1\r\n" +
		"Host: " + u.Host + "\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\nUser-Agent: MikuLang\r\n\r\n"

	nc.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err = io.WriteString(nc, req); err != nil {
		nc.Close()
		return nil, err
	}
	br := bufio.NewReader(nc)
	resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	if err != nil {
		nc.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		nc.Close()
		return nil, fmt.Errorf("handshake recusado: %s", resp.Status)
	}
	if resp.Header.Get("Sec-WebSocket-Accept") != acceptKey(key) {
		nc.Close()
		return nil, errors.New("handshake inválido (Sec-WebSocket-Accept não confere)")
	}
	nc.SetDeadline(time.Time{})
	return &Conn{nc: nc, br: br, client: true}, nil
}

// Accept faz o upgrade de uma requisição HTTP (usado nos testes com gateway falso).
func Accept(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || key == "" {
		http.Error(w, "upgrade para websocket requerido", http.StatusBadRequest)
		return nil, errors.New("requisição não é um upgrade websocket")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack não suportado", http.StatusInternalServerError)
		return nil, errors.New("ResponseWriter não suporta Hijack")
	}
	nc, brw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + acceptKey(key) + "\r\n\r\n"
	if _, err = brw.WriteString(resp); err == nil {
		err = brw.Flush()
	}
	if err != nil {
		nc.Close()
		return nil, err
	}
	return &Conn{nc: nc, br: brw.Reader}, nil
}

func (c *Conn) readFrame() (fin bool, op byte, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(c.br, h[:]); err != nil {
		return
	}
	fin = h[0]&0x80 != 0
	op = h[0] & 0x0f
	masked := h[1]&0x80 != 0
	n := uint64(h[1] & 0x7f)
	switch n {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > maxMessage {
		err = errors.New("mensagem websocket grande demais")
		return
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(c.br, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(c.br, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return
}

func (c *Conn) writeFrame(op byte, p []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	n := len(p)
	buf := make([]byte, 0, n+14)
	buf = append(buf, 0x80|op)
	var maskBit byte
	if c.client {
		maskBit = 0x80
	}
	switch {
	case n < 126:
		buf = append(buf, maskBit|byte(n))
	case n <= 0xffff:
		buf = append(buf, maskBit|126, byte(n>>8), byte(n))
	default:
		buf = append(buf, maskBit|127)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		buf = append(buf, b[:]...)
	}
	if c.client {
		var m [4]byte
		if _, err := rand.Read(m[:]); err != nil {
			return err
		}
		buf = append(buf, m[:]...)
		for i, b := range p {
			buf = append(buf, b^m[i%4])
		}
	} else {
		buf = append(buf, p...)
	}
	c.nc.SetWriteDeadline(time.Now().Add(15 * time.Second))
	_, err := c.nc.Write(buf)
	return err
}

// ReadMessage devolve a próxima mensagem de texto/binária, tratando ping/pong/close.
func (c *Conn) ReadMessage() (int, []byte, error) {
	var (
		msg     []byte
		msgOp   int
		started bool
	)
	for {
		fin, op, p, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case OpPing:
			c.writeFrame(OpPong, p)
		case OpPong:
		case OpClose:
			ce := &CloseError{Code: 1005}
			if len(p) >= 2 {
				ce.Code = int(binary.BigEndian.Uint16(p))
				ce.Reason = string(p[2:])
			}
			c.writeFrame(OpClose, p)
			return 0, nil, ce
		case OpText, OpBinary:
			msgOp, msg, started = int(op), p, true
			if fin {
				return msgOp, msg, nil
			}
		case OpContinuation:
			if !started {
				return 0, nil, errors.New("frame de continuação sem início")
			}
			msg = append(msg, p...)
			if len(msg) > maxMessage {
				return 0, nil, errors.New("mensagem websocket grande demais")
			}
			if fin {
				return msgOp, msg, nil
			}
		default:
			return 0, nil, fmt.Errorf("opcode websocket inválido: %d", op)
		}
	}
}

func (c *Conn) WriteText(p []byte) error { return c.writeFrame(OpText, p) }

func (c *Conn) WriteClose(code int, reason string) error {
	p := make([]byte, 2, 2+len(reason))
	binary.BigEndian.PutUint16(p, uint16(code))
	return c.writeFrame(OpClose, append(p, reason...))
}

func (c *Conn) SetReadDeadline(t time.Time) error { return c.nc.SetReadDeadline(t) }

func (c *Conn) Close() error { return c.nc.Close() }
