package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type proxyExitProvider struct {
	url, source string
	ipip        bool
}

var proxyExitProviders = []proxyExitProvider{
	{"https://myip.ipip.net", "myip.ipip.net", true},
	{"https://api.ipify.org", "api.ipify.org", false},
}

func parseProxyExit(body string, ipip bool) (string, error) {
	text := strings.TrimSpace(body)
	if ipip {
		if !strings.HasPrefix(text, "当前 IP：") {
			return "", errors.New("unexpected exit provider format")
		}
		fields := strings.Fields(strings.TrimPrefix(text, "当前 IP："))
		if len(fields) == 0 {
			return "", errors.New("empty exit provider reply")
		}
		text = fields[0]
	}
	ip := net.ParseIP(text)
	if ip == nil || ip.To4() == nil || ip.IsPrivate() || !ip.IsGlobalUnicast() {
		return "", errors.New("invalid public IPv4 response")
	}
	return ip.String(), nil
}
func probeProxyExit(ctx context.Context, iface, preferred string) (string, string, error) {
	providers := append([]proxyExitProvider(nil), proxyExitProviders...)
	if preferred != "" {
		for i, p := range providers {
			if p.source == preferred {
				providers[0], providers[i] = providers[i], providers[0]
				break
			}
		}
	}
	failures := []string{}
	for _, provider := range providers {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		ip, err := probeProxyExitProvider(ctx, iface, provider)
		if err == nil {
			return ip, provider.source, nil
		}
		failures = append(failures, provider.source+": "+err.Error())
	}
	return "", "", errors.New(strings.Join(failures, "; "))
}
func probeProxyExitProvider(ctx context.Context, iface string, provider proxyExitProvider) (string, error) {
	attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) { return cellularDial(ctx, iface, addr) }, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect rejected") }}
	request, _ := http.NewRequestWithContext(attempt, "GET", provider.url, nil)
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", fmt.Errorf("exit lookup HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1025))
	if err != nil {
		return "", err
	}
	if len(body) > 1024 {
		return "", errors.New("oversized exit reply")
	}
	return parseProxyExit(string(body), provider.ipip)
}
