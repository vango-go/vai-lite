package vai

import (
	"net"
	"net/http"
	"time"
)

// newDefaultHTTPClient configures sane transport-level timeouts while keeping
// the overall request lifetime controlled by context deadlines.
//
// We intentionally do not set http.Client.Timeout because streaming APIs are
// long-lived; callers should use per-request context deadlines for non-streaming.
func newDefaultHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &http.Client{Transport: transport}
}
