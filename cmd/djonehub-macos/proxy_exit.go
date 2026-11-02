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
	// prefix precedes the IP in the reply; empty means the body is the bare IP.
	prefix string
}

// Domestic providers answer over China Mobile in a few hundred ms; ifconfig.me
// covers the case where they are unreachable. All are queried in parallel.
var proxyExitProviders = []proxyExitProvider{
	{"https://myip.ipip.net", "myip.ipip.net", "当前 IP："},
	{"https://ip.3322.net", "ip.3322.net", ""},
	{"https://ddns.oray.com/checkip", "ddns.oray.com", "Current IP Address:"},
	{"https://ifconfig.me/ip", "ifconfig.me", ""},
}

const proxyExitTimeout = 6 * time.Second

func parseProxyExit(body string, ipip bool) (string, error) {
	prefix := ""
	if ipip {
		prefix = "当前 IP："
	}
	return parseProxyExitReply(body, prefix)
}

func parseProxyExitReply(body, prefix string) (string, error) {
	text := strings.TrimSpace(body)
	if prefix != "" {
		if !strings.HasPrefix(text, prefix) {
			return "", errors.New("unexpected exit provider format")
		}
		fields := strings.Fields(strings.TrimPrefix(text, prefix))
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

// probeProxyExit queries every provider at once and returns the first valid
// answer. When preferred names a provider (reconnect compares before/after IPs
// from the same source), its answer wins if it arrives at all; the others are
// only a fallback for when it fails.
func probeProxyExit(ctx context.Context, iface, preferred string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, proxyExitTimeout)
	defer cancel()
	type answer struct {
		ip, source string
		err        error
	}
	answers := make(chan answer, len(proxyExitProviders))
	waitPreferred := false
	for _, provider := range proxyExitProviders {
		if provider.source == preferred {
			waitPreferred = true
		}
		go func(provider proxyExitProvider) {
			ip, err := probeProxyExitProvider(ctx, iface, provider)
			answers <- answer{ip, provider.source, err}
		}(provider)
	}
	var fallback *answer
	failures := []string{}
	for range proxyExitProviders {
		a := <-answers
		if a.err != nil {
			failures = append(failures, a.source+": "+a.err.Error())
			if a.source == preferred {
				waitPreferred = false
			}
		} else if !waitPreferred || a.source == preferred {
			return a.ip, a.source, nil
		} else if fallback == nil {
			fallback = &a
		}
		if fallback != nil && !waitPreferred {
			return fallback.ip, fallback.source, nil
		}
	}
	if fallback != nil {
		return fallback.ip, fallback.source, nil
	}
	// An IPv6-only bearer without NAT64 has no IPv4 exit; its IPv6 one is
	// still the address sites see.
	if ip, source, err := probeProxyExitIPv6(context.WithoutCancel(ctx), iface); err == nil {
		return ip, source, nil
	}
	return "", "", errors.New(strings.Join(failures, "; "))
}

// IPv6-only echo services; asked only when the module provides IPv6.
var proxyExitIPv6Providers = []proxyExitProvider{
	{"https://api-ipv6.ip.sb/ip", "api-ipv6.ip.sb", ""},
	{"https://v6.ident.me", "v6.ident.me", ""},
	{"https://api6.ipify.org", "api6.ipify.org", ""},
}

func parseProxyExitIPv6(body string) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(body))
	if ip == nil || !isPublicIPv6(ip) {
		return "", errors.New("invalid public IPv6 response")
	}
	return ip.String(), nil
}

func probeProxyExitIPv6(ctx context.Context, iface string) (string, string, error) {
	if !cellularHasIPv6(iface) {
		return "", "", errors.New("module provides no IPv6")
	}
	ctx, cancel := context.WithTimeout(ctx, proxyExitTimeout)
	defer cancel()
	type answer struct {
		ip, source string
		err        error
	}
	answers := make(chan answer, len(proxyExitIPv6Providers))
	for _, provider := range proxyExitIPv6Providers {
		go func(provider proxyExitProvider) {
			body, err := fetchProxyExit(ctx, iface, provider.url, true)
			ip := ""
			if err == nil {
				ip, err = parseProxyExitIPv6(body)
			}
			answers <- answer{ip, provider.source, err}
		}(provider)
	}
	failures := []string{}
	for range proxyExitIPv6Providers {
		a := <-answers
		if a.err == nil {
			return a.ip, a.source, nil
		}
		failures = append(failures, a.source+": "+a.err.Error())
	}
	return "", "", errors.New(strings.Join(failures, "; "))
}

// refreshExitIPv6 records the IPv6 exit next to the IPv4 one currently shown.
func (p *cellularProxy) refreshExitIPv6(ctx context.Context, iface, ipv4 string) {
	if net.ParseIP(ipv4).To4() == nil {
		return
	}
	ip, _, err := probeProxyExitIPv6(ctx, iface)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state.PublicIP != ipv4 {
		return
	}
	if err != nil {
		ip = ""
	}
	p.state.PublicIPv6 = ip
}

func probeProxyExitProvider(ctx context.Context, iface string, provider proxyExitProvider) (string, error) {
	body, err := fetchProxyExit(ctx, iface, provider.url, false)
	if err != nil {
		return "", err
	}
	return parseProxyExitReply(body, provider.prefix)
}

func fetchProxyExit(ctx context.Context, iface, url string, ipv6Only bool) (string, error) {
	defer beginCellularActivity()()
	dial := func(ctx context.Context, _, addr string) (net.Conn, error) { return cellularDial(ctx, iface, addr) }
	if ipv6Only {
		dial = func(ctx context.Context, _, addr string) (net.Conn, error) { return cellularDialIPv6(ctx, iface, addr) }
	}
	transport := &http.Transport{Proxy: nil, DialContext: dial, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect rejected") }}
	request, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	request.Header.Set("User-Agent", "curl/8")
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
	return string(body), nil
}
