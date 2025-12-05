package network

import (
	"net"
	"net/http"
	"net/url"
	"time"

	"ctdbtools/internal/version"
)

// HTTPClient provides a configurable HTTP client for network operations.
type HTTPClient struct {
	client        *http.Client
	userAgent     string
	connectTimeout time.Duration
	socketTimeout  time.Duration
}

// Option configures an HTTPClient.
type Option func(*HTTPClient)

// WithTimeout sets connect and socket timeouts.
func WithTimeout(connect, socket time.Duration) Option {
	return func(c *HTTPClient) {
		c.connectTimeout = connect
		c.socketTimeout = socket
	}
}

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option {
	return func(c *HTTPClient) {
		c.userAgent = ua
	}
}

// WithProxy sets a proxy URL for requests.
func WithProxy(proxyURL string) Option {
	return func(c *HTTPClient) {
		if proxyURL == "" {
			return
		}
		u, err := url.Parse(proxyURL)
		if err != nil {
			return
		}
		if transport, ok := c.client.Transport.(*http.Transport); ok {
			transport.Proxy = http.ProxyURL(u)
		}
	}
}

// NewHTTPClient creates a new HTTP client with the given options.
func NewHTTPClient(opts ...Option) *HTTPClient {
	c := &HTTPClient{
		connectTimeout: 15 * time.Second,
		socketTimeout:  30 * time.Second,
		userAgent:      "ctdbtools/" + version.Version,
	}

	for _, opt := range opts {
		opt(c)
	}

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   c.connectTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ResponseHeaderTimeout: c.socketTimeout,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		DisableCompression:    false,
	}

	c.client = &http.Client{
		Transport: transport,
		Timeout:   c.connectTimeout + c.socketTimeout,
	}

	return c
}

// Do executes an HTTP request with the configured user-agent.
func (c *HTTPClient) Do(req *http.Request) (*http.Response, error) {
	if c.userAgent != "" && req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	return c.client.Do(req)
}

// Get performs an HTTP GET request.
func (c *HTTPClient) Get(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}
