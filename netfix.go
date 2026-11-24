package main

import (
	"context"
	"errors"
	"net"
	"os"
	"runtime"
	"time"
)

// platformFix ajusta DNS e certificados em sistemas fora do padrão (Android/Termux),
// para o binário funcionar "de cara", sem configurar nada.
func platformFix() {
	if runtime.GOOS == "linux" || runtime.GOOS == "android" {
		// Android não tem /etc/resolv.conf: o resolvedor do Go cairia em [::1]:53 e falharia.
		if _, err := os.Stat("/etc/resolv.conf"); err != nil {
			net.DefaultResolver = &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
					d := net.Dialer{Timeout: 5 * time.Second}
					for _, server := range []string{"1.1.1.1:53", "8.8.8.8:53"} {
						if c, err := d.DialContext(ctx, network, server); err == nil {
							return c, nil
						}
					}
					return nil, errors.New("não consegui falar com nenhum servidor DNS (1.1.1.1 / 8.8.8.8)")
				},
			}
		}
		if os.Getenv("SSL_CERT_FILE") == "" && os.Getenv("SSL_CERT_DIR") == "" {
			candidates := []string{
				os.Getenv("PREFIX") + "/etc/tls/cert.pem", // Termux
				"/data/data/com.termux/files/usr/etc/tls/cert.pem",
			}
			for _, f := range candidates {
				if st, err := os.Stat(f); err == nil && !st.IsDir() {
					os.Setenv("SSL_CERT_FILE", f)
					return
				}
			}
			if st, err := os.Stat("/system/etc/security/cacerts"); err == nil && st.IsDir() {
				os.Setenv("SSL_CERT_DIR", "/system/etc/security/cacerts")
			}
		}
	}
}
