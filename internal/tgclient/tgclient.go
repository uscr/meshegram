// Package tgclient builds an *http.Client with an optional proxy
// (http/https/socks5) for use with bot.WithHTTPClient.
package tgclient

import (
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const DefaultTimeout = 120 * time.Second

// New returns an http.Client optionally wired to the given proxy URL.
// Empty proxyURL falls back to HTTP_PROXY / HTTPS_PROXY from the environment.
// Supported schemes: http, https, socks5 (with optional user:pass).
func New(proxyURL string) (*http.Client, error) {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
	}
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL %q: %w", proxyURL, err)
		}
		transport.Proxy = http.ProxyURL(u)
	}
	return &http.Client{
		Timeout:   DefaultTimeout,
		Transport: transport,
	}, nil
}
