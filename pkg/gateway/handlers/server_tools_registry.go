package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/gateway/config"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/adapters/exa"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/adapters/firecrawl"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/adapters/tavily"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/safety"
	"github.com/vango-go/vai-lite/pkg/gateway/tools/servertools"
)

func newServerToolsRegistry(cfg config.Config, baseHTTPClient *http.Client, r *http.Request, enabled []string, rawConfig map[string]any) (*servertools.Registry, error) {
	enabledSet := make(map[string]struct{}, len(enabled))
	for i, name := range enabled {
		name = strings.TrimSpace(name)
		switch name {
		case servertools.ToolWebSearch, servertools.ToolWebFetch:
		default:
			return nil, &core.Error{
				Type:    core.ErrInvalidRequest,
				Message: fmt.Sprintf("unsupported server tool %q", name),
				Param:   fmt.Sprintf("server_tools[%d]", i),
				Code:    "unsupported_server_tool",
			}
		}
		enabledSet[name] = struct{}{}
	}
	for name := range rawConfig {
		if _, ok := enabledSet[name]; !ok {
			return nil, &core.Error{
				Type:    core.ErrInvalidRequest,
				Message: fmt.Sprintf("server tool config provided for disabled tool %q", name),
				Param:   "server_tool_config." + name,
				Code:    "run_validation_failed",
			}
		}
	}

	toolHTTPClient := safety.NewRestrictedHTTPClient(baseHTTPClient)
	executors := make([]servertools.Executor, 0, len(enabled))

	if _, ok := enabledSet[servertools.ToolWebSearch]; ok {
		searchConfig, err := servertools.DecodeWebSearchConfig(rawConfig[servertools.ToolWebSearch])
		if err != nil {
			if strings.Contains(err.Error(), "unsupported provider") {
				return nil, &core.Error{
					Type:    core.ErrInvalidRequest,
					Message: err.Error(),
					Param:   "server_tool_config.vai_web_search.provider",
					Code:    "unsupported_tool_provider",
				}
			}
			return nil, &core.Error{
				Type:    core.ErrInvalidRequest,
				Message: err.Error(),
				Param:   "server_tool_config.vai_web_search",
				Code:    "run_validation_failed",
			}
		}

		if searchConfig.Provider == "" {
			hasTavilyKey := strings.TrimSpace(r.Header.Get(servertools.HeaderProviderKeyTavily)) != ""
			hasExaKey := strings.TrimSpace(r.Header.Get(servertools.HeaderProviderKeyExa)) != ""
			if hasTavilyKey == hasExaKey {
				msg := "server_tool_config.vai_web_search.provider is required"
				if hasTavilyKey && hasExaKey {
					msg = "ambiguous web search provider; set server_tool_config.vai_web_search.provider"
				}
				return nil, &core.Error{
					Type:    core.ErrInvalidRequest,
					Message: msg,
					Param:   "server_tool_config.vai_web_search.provider",
					Code:    "tool_provider_missing",
				}
			}
			if hasTavilyKey {
				searchConfig.Provider = servertools.ProviderTavily
			} else {
				searchConfig.Provider = servertools.ProviderExa
			}
		}

		searchHeader := servertools.ProviderHeaderName(searchConfig.Provider)
		searchKey := strings.TrimSpace(r.Header.Get(searchHeader))
		if searchKey == "" {
			return nil, &core.Error{
				Type:    core.ErrAuthentication,
				Message: "missing server tool provider key header",
				Param:   searchHeader,
				Code:    "provider_key_missing",
			}
		}
		var tavilyClient *tavily.Client
		var exaClient *exa.Client
		switch searchConfig.Provider {
		case servertools.ProviderTavily:
			tavilyClient = tavily.NewClient(searchKey, cfg.TavilyBaseURL, toolHTTPClient)
		case servertools.ProviderExa:
			exaClient = exa.NewClient(searchKey, cfg.ExaBaseURL, toolHTTPClient)
		default:
			return nil, &core.Error{
				Type:    core.ErrInvalidRequest,
				Message: fmt.Sprintf("unsupported web search provider %q", searchConfig.Provider),
				Param:   "server_tool_config.vai_web_search.provider",
				Code:    "unsupported_tool_provider",
			}
		}
		executors = append(executors, servertools.NewWebSearchExecutor(searchConfig, tavilyClient, exaClient))
	}

	if _, ok := enabledSet[servertools.ToolWebFetch]; ok {
		fetchConfig, err := servertools.DecodeWebFetchConfig(rawConfig[servertools.ToolWebFetch])
		if err != nil {
			if strings.Contains(err.Error(), "unsupported provider") {
				return nil, &core.Error{
					Type:    core.ErrInvalidRequest,
					Message: err.Error(),
					Param:   "server_tool_config.vai_web_fetch.provider",
					Code:    "unsupported_tool_provider",
				}
			}
			return nil, &core.Error{
				Type:    core.ErrInvalidRequest,
				Message: err.Error(),
				Param:   "server_tool_config.vai_web_fetch",
				Code:    "run_validation_failed",
			}
		}
		if fetchConfig.Provider == "" {
			hasTavilyKey := strings.TrimSpace(r.Header.Get(servertools.HeaderProviderKeyTavily)) != ""
			hasFirecrawlKey := strings.TrimSpace(r.Header.Get(servertools.HeaderProviderKeyFirecrawl)) != ""
			if hasTavilyKey == hasFirecrawlKey {
				msg := "server_tool_config.vai_web_fetch.provider is required"
				if hasTavilyKey && hasFirecrawlKey {
					msg = "ambiguous web fetch provider; set server_tool_config.vai_web_fetch.provider"
				}
				return nil, &core.Error{
					Type:    core.ErrInvalidRequest,
					Message: msg,
					Param:   "server_tool_config.vai_web_fetch.provider",
					Code:    "tool_provider_missing",
				}
			}
			if hasTavilyKey {
				fetchConfig.Provider = servertools.ProviderTavily
			} else {
				fetchConfig.Provider = servertools.ProviderFirecrawl
			}
		}
		fetchHeader := servertools.ProviderHeaderName(fetchConfig.Provider)
		fetchKey := strings.TrimSpace(r.Header.Get(fetchHeader))
		if fetchKey == "" {
			return nil, &core.Error{
				Type:    core.ErrAuthentication,
				Message: "missing server tool provider key header",
				Param:   fetchHeader,
				Code:    "provider_key_missing",
			}
		}
		var firecrawlClient *firecrawl.Client
		var tavilyClient *tavily.Client
		switch fetchConfig.Provider {
		case servertools.ProviderFirecrawl:
			firecrawlClient = firecrawl.NewClient(fetchKey, cfg.FirecrawlBaseURL, toolHTTPClient)
		case servertools.ProviderTavily:
			tavilyClient = tavily.NewClient(fetchKey, cfg.TavilyBaseURL, toolHTTPClient)
		default:
			return nil, &core.Error{
				Type:    core.ErrInvalidRequest,
				Message: fmt.Sprintf("unsupported web fetch provider %q", fetchConfig.Provider),
				Param:   "server_tool_config.vai_web_fetch.provider",
				Code:    "unsupported_tool_provider",
			}
		}
		executors = append(executors, servertools.NewWebFetchExecutor(fetchConfig, firecrawlClient, tavilyClient))
	}

	return servertools.NewRegistry(executors...), nil
}
