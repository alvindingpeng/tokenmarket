package rhttp

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alvindingpeng/switchyard/internal/model"
	"github.com/alvindingpeng/switchyard/internal/op"
	"golang.org/x/net/proxy"
)

// 连接池参数。
// Go 默认 MaxIdleConnsPerHost 只有 2: 同一上游并发超过 2 时, 多出来的连接用完即被关闭,
// 下一个请求又要重新建连握手 —— 高并发下等于持续付 RTT。中转场景上游就那么几个域名,
// 每个域名留足空闲连接, 比省那点内存重要得多。
const (
	maxIdleConns        = 256
	maxIdleConnsPerHost = 64
	idleConnTimeout     = 90 * time.Second
)

// 共享客户端的回收参数: 渠道代理地址被改掉后旧条目不会再用, 留着只会把连接池钉在内存里。
const (
	sharedClientTTL     = 15 * time.Minute
	sharedJanitorPeriod = 5 * time.Minute
)

var (
	directClient   *http.Client // directClient 不经代理的共享客户端。
	proxyClient    *http.Client // proxyClient 按应用设置代理地址构建的共享客户端。
	proxyClientURL string       // proxyClientURL proxyClient 当前所用的代理地址, 设置变更时据此重建。
	clientLock     sync.RWMutex
)

// sharedClient 是共享客户端缓存条目; lastUsed 供后台回收判断是否还在使用。
type sharedClient struct {
	client   *http.Client
	lastUsed time.Time
}

var (
	sharedMu      sync.Mutex
	sharedClients = map[string]*sharedClient{}
	sharedOnce    sync.Once
)

// Shared 返回按代理地址共享的客户端: 相同地址复用同一个连接池。
// 地址支持 http, https, socks5, socks5h。
//
// 此前这里是 New: 每个请求新建 client, 用完还要 CloseIdleConnections,
// 等于每个请求都重新 TCP 建连 + TLS 握手到代理和上游, 多付 1-3 个 RTT。
// 平时没感觉是因为几乎没有渠道配专用代理; 一旦为了加速给渠道加出口, 这笔开销会随流量线性放大 ——
// 越"加速"越慢。
func Shared(proxyURL string) (*http.Client, error) {
	if proxyURL == "" {
		return nil, fmt.Errorf("proxy url is empty")
	}

	sharedMu.Lock()
	defer sharedMu.Unlock()

	if entry, ok := sharedClients[proxyURL]; ok {
		entry.lastUsed = time.Now()
		return entry.client, nil
	}
	client, err := newCustomProxyClient(proxyURL)
	if err != nil {
		return nil, err
	}
	sharedClients[proxyURL] = &sharedClient{client: client, lastUsed: time.Now()}
	sharedOnce.Do(func() { go sweepSharedClients() })
	return client, nil
}

// sweepSharedClients 定期回收长时间未使用的共享客户端并关闭其空闲连接。
// 不回收的话, 管理员每次改代理地址都会留下一个永不释放的连接池。
func sweepSharedClients() {
	ticker := time.NewTicker(sharedJanitorPeriod)
	defer ticker.Stop()
	for range ticker.C {
		sharedMu.Lock()
		for key, entry := range sharedClients {
			if time.Since(entry.lastUsed) < sharedClientTTL {
				continue
			}
			delete(sharedClients, key)
			entry.client.CloseIdleConnections()
		}
		sharedMu.Unlock()
	}
}

// Proxy 返回按应用设置(setting key: proxy_url)代理地址构建的共享客户端。
// 设置未填代理地址时报错而不静默直连: 调用方要的就是走代理, 直连需求由 Direct 表达。
func Proxy() (*http.Client, error) {
	currentProxyURL, err := op.SettingGetString(model.SettingKeyProxyURL)
	if err != nil {
		return nil, err
	}
	if currentProxyURL == "" {
		return nil, fmt.Errorf("proxy url is empty")
	}

	clientLock.RLock()
	if proxyClient != nil && proxyClientURL == currentProxyURL {
		clientLock.RUnlock()
		return proxyClient, nil
	}
	clientLock.RUnlock()

	clientLock.Lock()
	defer clientLock.Unlock()

	// 取写锁期间设置可能已被其他协程重建, 故再核对一次地址。
	if proxyClient != nil && proxyClientURL == currentProxyURL {
		return proxyClient, nil
	}

	client, err := newCustomProxyClient(currentProxyURL)
	if err != nil {
		return nil, err
	}
	// 代理地址变更后旧客户端不再被引用, 关掉空闲连接免得连接池残留。
	if proxyClient != nil {
		proxyClient.CloseIdleConnections()
	}
	proxyClient = client
	proxyClientURL = currentProxyURL
	return proxyClient, nil
}

// Direct 返回不经任何代理的共享客户端。
func Direct() (*http.Client, error) {
	clientLock.RLock()
	if directClient != nil {
		clientLock.RUnlock()
		return directClient, nil
	}
	clientLock.RUnlock()

	clientLock.Lock()
	defer clientLock.Unlock()

	if directClient != nil {
		return directClient, nil
	}
	cloned, err := clonedDefaultTransport()
	if err != nil {
		return nil, err
	}
	// 克隆自 http.DefaultTransport 会带上 ProxyFromEnvironment, 直连须显式清掉。
	cloned.Proxy = nil
	directClient = &http.Client{Transport: cloned, CheckRedirect: rejectRedirects}
	return directClient, nil
}

// ForChannel 按渠道配置返回探测用的客户端: 直连 / 应用代理 / 渠道专用代理。
//
// 探测(拉 /models)与转发走同一套出口选择, 否则「渠道配了专用代理但探测直连」会让
// 只能经代理访问的上游在探测时表现为不可达, 自动添加永远发现不了模型。
// 三档都走共享连接池: 探测是低频动作, 但同样没必要为它单独建连接。
func ForChannel(target model.ChannelConfig) (*http.Client, error) {
	switch {
	case !target.Proxy:
		return Direct()
	case strings.TrimSpace(target.ChannelProxy) == "":
		return Proxy()
	default:
		return Shared(strings.TrimSpace(target.ChannelProxy))
	}
}

// clonedDefaultTransport 克隆默认 Transport 并调好连接池参数。
// 所有客户端都从这里出发, 参数只需在这一处维护。
func clonedDefaultTransport() (*http.Transport, error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default transport is not *http.Transport")
	}
	cloned := transport.Clone()
	cloned.MaxIdleConns = maxIdleConns
	cloned.MaxIdleConnsPerHost = maxIdleConnsPerHost
	cloned.IdleConnTimeout = idleConnTimeout
	// 上游多为 HTTPS, 显式开启让 HTTP/2 的多路复用不被旧版行为挡住。
	cloned.ForceAttemptHTTP2 = true
	return cloned, nil
}

func newCustomProxyClient(proxyURLStr string) (*http.Client, error) {
	cloned, err := clonedDefaultTransport()
	if err != nil {
		return nil, err
	}

	proxyURL, err := url.Parse(proxyURLStr)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy url: %w", err)
	}

	switch proxyURL.Scheme {
	case "http", "https":
		cloned.Proxy = http.ProxyURL(proxyURL)
	case "socks5", "socks5h":
		// forward 必须显式给出带 Timeout 的 Dialer: proxy.Direct 用的是零值 net.Dialer, 拨到 SOCKS 服务器这一跳没有超时,
		// 而替换 DialContext 又会覆盖掉 DefaultTransport 自带的 30s 拨号超时。
		socksDialer, err := proxy.FromURL(proxyURL, &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second})
		if err != nil {
			return nil, fmt.Errorf("invalid socks proxy: %w", err)
		}
		// SOCKS 握手要走 ctx 才能被上层的超时和取消打断; Dialer.Dial 内部把 ctx 写死成 Background 且已被标记弃用。
		contextDialer, ok := socksDialer.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("socks proxy dialer does not support context")
		}
		// SOCKS 代理无法通过 Transport.Proxy 表达, 只能改拨号入口。
		cloned.Proxy = nil
		cloned.DialContext = contextDialer.DialContext
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s", proxyURL.Scheme)
	}

	return &http.Client{Transport: cloned, CheckRedirect: rejectRedirects}, nil
}

func rejectRedirects(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}
