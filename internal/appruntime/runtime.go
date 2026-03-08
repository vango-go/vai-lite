package appruntime

import (
	"log/slog"
	"net/http"
	"sync"

	"github.com/vango-go/vai-lite/internal/services"
)

type Config struct {
	BaseURL       string
	DefaultModel  string
	TopupOptions  []int64
	WorkOSEnabled bool
	DevMode       bool
}

type Deps struct {
	Services *services.AppServices
	Gateway  http.Handler
	Logger   *slog.Logger
	Config   Config
}

var (
	mu      sync.RWMutex
	current *Deps
)

func Set(deps *Deps) {
	if deps == nil {
		panic("appruntime: deps are required")
	}
	clone := *deps
	if deps.Config.TopupOptions != nil {
		clone.Config.TopupOptions = append([]int64(nil), deps.Config.TopupOptions...)
	}
	mu.Lock()
	current = &clone
	mu.Unlock()
}

func Get() *Deps {
	mu.RLock()
	deps := current
	mu.RUnlock()
	if deps == nil {
		panic("appruntime: deps are not configured")
	}
	return deps
}
