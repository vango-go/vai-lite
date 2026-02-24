package server

import (
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/vango-go/vai-lite/pkg/gateway/config"
	"github.com/vango-go/vai-lite/pkg/gateway/handlers"
	"github.com/vango-go/vai-lite/pkg/gateway/mw"
	"github.com/vango-go/vai-lite/pkg/gateway/ratelimit"
	"github.com/vango-go/vai-lite/pkg/gateway/upstream"
)

type Server struct {
	cfg    config.Config
	logger *slog.Logger
	mux    *http.ServeMux

	upstreams  upstream.Factory
	httpClient *http.Client
	limiter    *ratelimit.Limiter
}

func New(cfg config.Config, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout: 10 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: cfg.UpstreamResponseHeaderTimeout,
		},
	}

	s := &Server{
		cfg:    cfg,
		logger: logger,
		mux:    http.NewServeMux(),
		upstreams: upstream.Factory{
			HTTPClient: httpClient,
		},
		httpClient: httpClient,
		limiter: ratelimit.New(ratelimit.Config{
			RPS:                   cfg.LimitRPS,
			Burst:                 cfg.LimitBurst,
			MaxConcurrentRequests: cfg.LimitMaxConcurrentRequests,
			MaxConcurrentStreams:  cfg.LimitMaxConcurrentStreams,
		}),
	}

	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.Handle("/healthz", handlers.HealthHandler{})
	s.mux.Handle("/readyz", handlers.ReadyHandler{Config: s.cfg})

	s.mux.Handle("/v1/messages", handlers.MessagesHandler{
		Config:     s.cfg,
		Upstreams:  s.upstreams,
		HTTPClient: s.httpClient,
		Logger:     s.logger,
		Limiter:    s.limiter,
	})
}

func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = mw.RateLimit(s.cfg, s.limiter, h)
	h = mw.Auth(s.cfg, h)
	h = mw.CORS(s.cfg, h)
	h = mw.Recover(s.logger, h)
	h = mw.AccessLog(s.logger, h)
	h = mw.RequestID(h)
	return h
}
