package session

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	localWebFetchTimeout        = 5 * time.Second
	localWebFetchMaxBodyBytes   = 64 * 1024
	localWebFetchMaxOutputLines = 200
	localWebFetchMaxRedirects   = 3
)

func isPublicIP(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsUnspecified() && !ip.IsMulticast()
}

var resolvePublicHostFunc = resolvePublicHost
var webFetchHTTPClientFunc = webFetchHTTPClient

func resolvePublicHost(ctx context.Context, host string) (net.IP, error) {
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var firstPublic net.IP
	for _, addr := range ips {
		ip := addr.IP
		if !isPublicIP(ip) {
			return nil, fmt.Errorf("blocked private/local address %s", ip.String())
		}
		if firstPublic == nil {
			firstPublic = ip
		}
	}
	if firstPublic == nil {
		return nil, fmt.Errorf("no public address for host")
	}
	return firstPublic, nil
}

func validateWebFetchURL(ctx context.Context, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("only https:// URLs are allowed")
	}
	if u.User != nil {
		return nil, fmt.Errorf("URL userinfo is not allowed")
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("missing host")
	}
	if _, err := resolvePublicHostFunc(ctx, u.Hostname()); err != nil {
		return nil, err
	}
	return u, nil
}

func webFetchHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: localWebFetchTimeout}
	transport := &http.Transport{
		Proxy:           nil,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ip, err := resolvePublicHostFunc(ctx, host)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		},
	}
	return &http.Client{
		Timeout:   localWebFetchTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= localWebFetchMaxRedirects {
				return fmt.Errorf("stopped after %d redirects", localWebFetchMaxRedirects)
			}
			_, err := validateWebFetchURL(req.Context(), req.URL.String())
			return err
		},
	}
}

func (s *Session) runLocalWebFetch(rawURL, queryKey, queryValue string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), localWebFetchTimeout)
	defer cancel()

	u, err := validateWebFetchURL(ctx, rawURL)
	if err != nil {
		return []string{fmt.Sprintf("#webfetch: %v", err)}
	}
	query := u.Query()
	query.Set(queryKey, queryValue)
	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return []string{fmt.Sprintf("#webfetch: %v", err)}
	}
	req.Header.Set("User-Agent", "RubyMUD-webfetch/1.0")

	resp, err := webFetchHTTPClientFunc().Do(req)
	if err != nil {
		return []string{fmt.Sprintf("#webfetch: %v", err)}
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, localWebFetchMaxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return []string{fmt.Sprintf("#webfetch: %v", err)}
	}
	truncated := len(body) > localWebFetchMaxBodyBytes
	if truncated {
		body = body[:localWebFetchMaxBodyBytes]
	}

	lines := splitExecOutputLines(string(body))
	if len(lines) > localWebFetchMaxOutputLines {
		lines = append(lines[:localWebFetchMaxOutputLines], fmt.Sprintf("#webfetch: output truncated after %d lines", localWebFetchMaxOutputLines))
	}
	if truncated {
		lines = append(lines, fmt.Sprintf("#webfetch: output truncated after %d bytes", localWebFetchMaxBodyBytes))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		lines = append([]string{fmt.Sprintf("#webfetch: HTTP %d", resp.StatusCode)}, lines...)
	}
	if len(lines) == 0 {
		return []string{"#webfetch: empty response"}
	}
	return lines
}

func webFetchArgs(args []string) (key string, value string) {
	if len(args) > 0 {
		key = strings.TrimSpace(args[0])
	}
	if len(args) > 1 {
		value = args[1]
	}
	return key, value
}
