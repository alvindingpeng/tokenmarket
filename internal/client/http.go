package client

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"golang.org/x/net/proxy"
)

var (
	directClient *http.Client
	proxyClient  *http.Client
	clientLock   sync.RWMutex
)

// GetHTTPClient returns a cached http.Client or creates a new one.
func GetHTTPClient(useProxy bool) (*http.Client, error) {
	clientLock.RLock()
	if useProxy && proxyClient != nil {
		clientLock.RUnlock()
		return proxyClient, nil
	}
	if !useProxy && directClient != nil {
		clientLock.RUnlock()
		return directClient, nil
	}
	clientLock.RUnlock()

	clientLock.Lock()
	defer clientLock.Unlock()

	if useProxy {
		if proxyClient != nil {
			return proxyClient, nil
		}
		client, err := NewHTTPClient(true)
		if err != nil {
			return nil, err
		}
		proxyClient = client
		return proxyClient, nil
	}

	if directClient != nil {
		return directClient, nil
	}
	client, err := NewHTTPClient(false)
	if err != nil {
		return nil, err
	}
	directClient = client
	return directClient, nil
}

// ClearHTTPClientPool clears the cached clients (useful when proxy settings change).
func ClearHTTPClientPool() {
	clientLock.Lock()
	directClient = nil
	proxyClient = nil
	clientLock.Unlock()
}

// NewHTTPClient returns an http.Client that can optionally use a proxy based on the setting.
// When useProxy is false, the client bypasses any proxy configuration.
func NewHTTPClient(useProxy bool) (*http.Client, error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default transport is not *http.Transport")
	}
	cloned := transport.Clone()

	if !useProxy {
		cloned.Proxy = nil
		return &http.Client{Transport: cloned}, nil
	}

	proxyURLStr, err := op.SettingGetString(model.SettingKeyProxyURL)
	if err != nil {
		return nil, err
	}
	if proxyURLStr == "" {
		return nil, fmt.Errorf("proxy url is empty")
	}

	proxyURL, err := url.Parse(proxyURLStr)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy url: %w", err)
	}

	switch proxyURL.Scheme {
	case "http", "https":
		cloned.Proxy = http.ProxyURL(proxyURL)
	case "socks", "socks5":
		socksDialer, err := proxy.FromURL(proxyURL, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("invalid socks proxy: %w", err)
		}
		cloned.Proxy = nil
		cloned.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return socksDialer.Dial(network, addr)
		}
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s", proxyURL.Scheme)
	}

	return &http.Client{Transport: cloned}, nil
}
