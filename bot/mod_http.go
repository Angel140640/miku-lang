package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"miku/lang"
)

// Version é preenchida pelo main.
var Version = "dev"

const httpMaxBody = 5 << 20 // 5 MB

// privateIP diz se o IP é de rede interna (loopback, privado, link-local...). Protege contra SSRF.
func privateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast()
}

func httpClient(timeout time.Duration, allowPrivate bool) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if !allowPrivate {
		d.Control = func(network, address string, c syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip != nil && privateIP(ip) {
				return errors.New("endereço de rede interna bloqueado (use allow_private=True se for de propósito)")
			}
			return nil
		}
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:               nil, // nunca usa proxy do ambiente
			DialContext:         d.DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("redirecionamentos demais")
			}
			return nil
		},
	}
}

func toStrMap(v lang.Value, what string) (map[string]string, error) {
	out := map[string]string{}
	if v == nil {
		return out, nil
	}
	d, ok := v.(*lang.Dict)
	if !ok {
		return nil, fmt.Errorf("%s precisa ser um dicionário", what)
	}
	for _, k := range d.Keys() {
		val, _, _ := d.Get(k)
		out[lang.Str(k)] = lang.Str(val)
	}
	return out, nil
}

func applyHTTP(b *Bot) {
	do := func(name, method string, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		if method == "" { // http_request(método, url, ...)
			if len(a) < 2 {
				return nil, errors.New("http_request(método, url, ...) precisa de método e url")
			}
			m, ok := a[0].(string)
			if !ok {
				return nil, errors.New("http_request: o método precisa ser um texto (\"GET\", \"POST\"...)")
			}
			method, a = strings.ToUpper(m), a[1:]
		}
		if len(a) < 1 {
			return nil, fmt.Errorf("%s(url) precisa de uma url", name)
		}
		raw, ok := a[0].(string)
		if !ok {
			return nil, fmt.Errorf("%s: a url precisa ser um texto", name)
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("%s: url inválida '%s' (precisa começar com http:// ou https://)", name, raw)
		}
		if q, ok := kw["params"]; ok && q != nil {
			pm, err := toStrMap(q, "params")
			if err != nil {
				return nil, err
			}
			vals := u.Query()
			for k, v := range pm {
				vals.Set(k, v)
			}
			u.RawQuery = vals.Encode()
		}
		headers, err := toStrMap(kw["headers"], "headers")
		if err != nil {
			return nil, err
		}
		var body io.Reader
		if j, ok := kw["json"]; ok && j != nil {
			g, err := lang.ToGo(j)
			if err != nil {
				return nil, err
			}
			txt, err := json.Marshal(g)
			if err != nil {
				return nil, err
			}
			body = bytes.NewReader(txt)
			if _, set := headers["Content-Type"]; !set {
				headers["Content-Type"] = "application/json"
			}
		} else if d, ok := kw["data"]; ok && d != nil {
			if s, isStr := d.(string); isStr {
				body = strings.NewReader(s)
			} else {
				fm, err := toStrMap(d, "data")
				if err != nil {
					return nil, err
				}
				vals := url.Values{}
				for k, v := range fm {
					vals.Set(k, v)
				}
				body = strings.NewReader(vals.Encode())
				if _, set := headers["Content-Type"]; !set {
					headers["Content-Type"] = "application/x-www-form-urlencoded"
				}
			}
		}
		timeout := 10.0
		switch t := kw["timeout"].(type) {
		case int64:
			timeout = float64(t)
		case float64:
			timeout = t
		}
		if timeout <= 0 || timeout > 60 {
			timeout = 60
		}
		allow, _ := kw["allow_private"].(bool)
		ctx := context.Background()
		req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "MikuLang/"+Version)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		var resp *http.Response
		var data []byte
		var ferr error
		b.In.Block(func() {
			resp, ferr = httpClient(time.Duration(timeout*float64(time.Second)), allow).Do(req)
			if ferr != nil {
				return
			}
			defer resp.Body.Close()
			data, ferr = io.ReadAll(io.LimitReader(resp.Body, httpMaxBody+1))
		})
		if ferr != nil {
			return nil, fmt.Errorf("%s falhou: %v", name, ferr)
		}
		if len(data) > httpMaxBody {
			return nil, fmt.Errorf("%s: a resposta passou de 5 MB", name)
		}
		out := lang.NewDict()
		out.SetStr("status", int64(resp.StatusCode))
		out.SetStr("ok", resp.StatusCode >= 200 && resp.StatusCode < 300)
		out.SetStr("text", string(data))
		var js lang.Value
		if v, err := lang.ParseJSON(string(data)); err == nil {
			js = v
		}
		out.SetStr("json", js)
		hd := lang.NewDict()
		for k, v := range resp.Header {
			hd.SetStr(strings.ToLower(k), strings.Join(v, ", "))
		}
		out.SetStr("headers", hd)
		return out, nil
	}
	b.In.DefineFunc("http_get", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		return do("http_get", "GET", a, kw)
	})
	b.In.DefineFunc("http_post", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		return do("http_post", "POST", a, kw)
	})
	b.In.DefineFunc("http_request", func(in *lang.Interp, a []lang.Value, kw map[string]lang.Value) (lang.Value, error) {
		return do("http_request", "", a, kw)
	})
}
