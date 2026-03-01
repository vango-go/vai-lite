package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/vango-go/vai-lite/internal/dotenv"
	"github.com/vango-go/vai-lite/pkg/core"
	vai "github.com/vango-go/vai-lite/sdk"
	"github.com/vango-go/vai-lite/sdk/adapters/tavily"
)

const (
	defaultBaseURL   = "http://127.0.0.1:8080"
	defaultModel     = "oai-resp/gpt-5-mini"
	defaultMaxTokens = 512
	defaultTimeout   = 90 * time.Second
)

type chatConfig struct {
	BaseURL       string
	Model         string
	MaxTokens     int
	Timeout       time.Duration
	SystemPrompt  string
	GatewayAPIKey string
	TavilyAPIKey  string
	ProviderKeys  map[string]string
}

func parseChatConfig(args []string, getenv func(string) string) (chatConfig, error) {
	if getenv == nil {
		getenv = os.Getenv
	}

	cfg := chatConfig{}
	fs := flag.NewFlagSet("proxy-chatbot", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.StringVar(&cfg.BaseURL, "base-url", defaultBaseURL, "VAI gateway base URL")
	fs.StringVar(&cfg.Model, "model", defaultModel, "provider/model to use")
	fs.IntVar(&cfg.MaxTokens, "max-tokens", defaultMaxTokens, "max output tokens per turn")
	fs.DurationVar(&cfg.Timeout, "timeout", defaultTimeout, "per-turn timeout (e.g. 90s)")
	fs.StringVar(&cfg.SystemPrompt, "system", "", "optional system prompt")
	fs.StringVar(&cfg.GatewayAPIKey, "gateway-api-key", strings.TrimSpace(getenv("VAI_GATEWAY_API_KEY")), "optional gateway api key (or VAI_GATEWAY_API_KEY)")

	if err := fs.Parse(args); err != nil {
		return chatConfig{}, err
	}

	cfg.TavilyAPIKey = strings.TrimSpace(getenv("TAVILY_API_KEY"))
	cfg.ProviderKeys = collectProviderKeys(getenv)

	if err := validateChatConfig(cfg); err != nil {
		return chatConfig{}, err
	}
	return cfg, nil
}

func collectProviderKeys(getenv func(string) string) map[string]string {
	keys := make(map[string]string)
	setIfPresent := func(provider, envName string) {
		if v := strings.TrimSpace(getenv(envName)); v != "" {
			keys[provider] = v
		}
	}

	setIfPresent("anthropic", "ANTHROPIC_API_KEY")
	setIfPresent("openai", "OPENAI_API_KEY")
	setIfPresent("groq", "GROQ_API_KEY")
	setIfPresent("cerebras", "CEREBRAS_API_KEY")
	setIfPresent("openrouter", "OPENROUTER_API_KEY")
	setIfPresent("gemini", "GEMINI_API_KEY")
	if _, ok := keys["gemini"]; !ok {
		setIfPresent("gemini", "GOOGLE_API_KEY")
	}

	return keys
}

func parseModelRef(model string) (provider string, modelName string, canonicalModel string, err error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", "", "", errors.New("model must not be empty")
	}

	provider, modelName, err = core.ParseModelString(model)
	if err != nil {
		return "", "", "", fmt.Errorf("invalid model %q: expected provider/model", model)
	}

	provider = strings.ToLower(strings.TrimSpace(provider))
	modelName = strings.TrimSpace(modelName)
	if provider == "" || modelName == "" {
		return "", "", "", fmt.Errorf("invalid model %q: provider and model must be non-empty", model)
	}

	return provider, modelName, provider + "/" + modelName, nil
}

func requiredKeySpec(provider string) (envHint string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "anthropic":
		return "ANTHROPIC_API_KEY", true
	case "openai", "oai-resp":
		return "OPENAI_API_KEY", true
	case "groq":
		return "GROQ_API_KEY", true
	case "cerebras":
		return "CEREBRAS_API_KEY", true
	case "openrouter":
		return "OPENROUTER_API_KEY", true
	case "gemini", "gemini-oauth":
		return "GEMINI_API_KEY (or GOOGLE_API_KEY)", true
	default:
		return "", false
	}
}

func hasKeyForProvider(provider string, keyStore map[string]string) bool {
	if keyStore == nil {
		return false
	}

	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai", "oai-resp":
		return strings.TrimSpace(keyStore["openai"]) != ""
	case "gemini", "gemini-oauth":
		return strings.TrimSpace(keyStore["gemini"]) != ""
	default:
		return strings.TrimSpace(keyStore[strings.ToLower(strings.TrimSpace(provider))]) != ""
	}
}

func validateModelWithKeys(model string, keys map[string]string) (string, error) {
	provider, _, canonicalModel, err := parseModelRef(model)
	if err != nil {
		return "", err
	}

	if envHint, ok := requiredKeySpec(provider); ok && !hasKeyForProvider(provider, keys) {
		return "", fmt.Errorf("missing provider key for %s (set %s)", provider, envHint)
	}
	return canonicalModel, nil
}

func validateChatConfig(cfg chatConfig) error {
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.GatewayAPIKey = strings.TrimSpace(cfg.GatewayAPIKey)

	if cfg.BaseURL == "" {
		return errors.New("base-url must not be empty")
	}
	baseURL, err := url.Parse(cfg.BaseURL)
	if err != nil || strings.TrimSpace(baseURL.Scheme) == "" || strings.TrimSpace(baseURL.Host) == "" {
		return errors.New("base-url must be a valid absolute URL")
	}
	if baseURL.User != nil {
		return errors.New("base-url must not include credentials")
	}
	canonicalModel, err := validateModelWithKeys(cfg.Model, cfg.ProviderKeys)
	if err != nil {
		return err
	}
	cfg.Model = canonicalModel
	if cfg.MaxTokens <= 0 {
		return errors.New("max-tokens must be > 0")
	}
	if cfg.Timeout <= 0 {
		return errors.New("timeout must be > 0")
	}
	if strings.TrimSpace(cfg.TavilyAPIKey) == "" {
		return errors.New("TAVILY_API_KEY is required to enable vai_web_search and vai_web_fetch")
	}
	return nil
}

func buildClientOptions(cfg chatConfig) []vai.ClientOption {
	opts := []vai.ClientOption{
		vai.WithBaseURL(cfg.BaseURL),
	}
	if cfg.GatewayAPIKey != "" {
		opts = append(opts, vai.WithGatewayAPIKey(cfg.GatewayAPIKey))
	}

	// Keep provider registration explicit and stable.
	for _, provider := range []string{"anthropic", "openai", "groq", "cerebras", "openrouter", "gemini"} {
		if key := strings.TrimSpace(cfg.ProviderKeys[provider]); key != "" {
			opts = append(opts, vai.WithProviderKey(provider, key))
		}
	}
	return opts
}

func buildWebTools(cfg chatConfig) []vai.ToolWithHandler {
	return []vai.ToolWithHandler{
		vai.VAIWebSearch(tavily.NewSearch(cfg.TavilyAPIKey)),
		vai.VAIWebFetch(tavily.NewExtract(cfg.TavilyAPIKey)),
	}
}

type chatRuntime struct {
	currentModel string
	history      []vai.Message
}

func handleSlashCommand(line string, state *chatRuntime, cfg chatConfig, out io.Writer, errOut io.Writer) (handled bool, err error) {
	if state == nil {
		return false, errors.New("chat state must not be nil")
	}
	if out == nil {
		out = os.Stdout
	}
	if errOut == nil {
		errOut = os.Stderr
	}

	switch {
	case line == "/model":
		fmt.Fprintf(out, "current model: %s\n", state.currentModel)
		return true, nil
	case strings.HasPrefix(line, "/model:"):
		target := strings.TrimSpace(strings.TrimPrefix(line, "/model:"))
		nextModel, validationErr := validateModelWithKeys(target, cfg.ProviderKeys)
		if validationErr != nil {
			fmt.Fprintf(errOut, "model switch error: %v\n", validationErr)
			return true, nil
		}
		prev := state.currentModel
		state.currentModel = nextModel
		fmt.Fprintf(out, "model switched: %s -> %s\n", prev, state.currentModel)
		return true, nil
	default:
		return false, nil
	}
}

func runChatbot(ctx context.Context, cfg chatConfig, in io.Reader, out io.Writer, errOut io.Writer) error {
	if err := validateChatConfig(cfg); err != nil {
		return err
	}
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	if errOut == nil {
		errOut = os.Stderr
	}

	provider, _, _, modelErr := parseModelRef(cfg.Model)
	if modelErr != nil {
		return modelErr
	}
	if envHint, ok := requiredKeySpec(provider); ok && !hasKeyForProvider(provider, cfg.ProviderKeys) {
		return fmt.Errorf("missing provider key for %s (set %s)", provider, envHint)
	}

	client := vai.NewClient(buildClientOptions(cfg)...)
	webTools := buildWebTools(cfg)

	fmt.Fprintf(out, "Proxy chatbot connected to %s using %s\n", cfg.BaseURL, cfg.Model)
	fmt.Fprintln(out, "Tavily web tools enabled: vai_web_search, vai_web_fetch")
	fmt.Fprintln(out, "Type /exit or /quit to stop. Use /model to view and /model:{provider}/{model} to switch.")

	scanner := bufio.NewScanner(in)
	state := chatRuntime{
		currentModel: cfg.Model,
		history:      make([]vai.Message, 0, 32),
	}
	for {
		fmt.Fprint(out, "> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("read input: %w", err)
			}
			fmt.Fprintln(out)
			return nil
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		switch line {
		case "/exit", "/quit":
			fmt.Fprintln(out, "bye")
			return nil
		}

		if handled, err := handleSlashCommand(line, &state, cfg, out, errOut); err != nil {
			return err
		} else if handled {
			continue
		}

		beforeLen := len(state.history)
		state.history = append(state.history, vai.Message{
			Role:    "user",
			Content: vai.Text(line),
		})

		req := &vai.MessageRequest{
			Model:     state.currentModel,
			Messages:  state.history,
			MaxTokens: cfg.MaxTokens,
		}
		if cfg.SystemPrompt != "" {
			req.System = cfg.SystemPrompt
		}

		turnCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
		stream, err := client.Messages.RunStream(turnCtx, req, vai.WithTools(webTools...))
		if err != nil {
			cancel()
			state.history = state.history[:beforeLen]
			fmt.Fprintf(errOut, "run stream setup error: %v\n", err)
			continue
		}

		sawDelta := false
		var streamedText strings.Builder
		for event := range stream.Events() {
			if text, ok := vai.TextDeltaFrom(event); ok {
				sawDelta = true
				fmt.Fprint(out, text)
				streamedText.WriteString(text)
			}
			if toolCall, ok := event.(vai.ToolCallStartEvent); ok {
				fmt.Fprintf(out, "\n[tool] %s\n", toolCall.Name)
			}
		}
		_ = stream.Close()

		if err := stream.Err(); err != nil {
			cancel()
			state.history = state.history[:beforeLen]
			fmt.Fprintln(out)
			fmt.Fprintf(errOut, "run stream error: %v\n", err)
			continue
		}

		result := stream.Result()
		assistantText := strings.TrimSpace(streamedText.String())
		if !sawDelta && result != nil && result.Response != nil {
			assistantText = strings.TrimSpace(result.Response.TextContent())
		}
		if assistantText != "" && !sawDelta {
			fmt.Fprint(out, assistantText)
		}
		fmt.Fprintln(out)
		cancel()
		if assistantText != "" {
			state.history = append(state.history, vai.Message{
				Role:    "assistant",
				Content: vai.Text(assistantText),
			})
		}
	}
}

func main() {
	if err := dotenv.LoadFile(".env"); err != nil {
		fmt.Fprintf(os.Stderr, "proxy-chatbot: %v\n", err)
		os.Exit(1)
	}

	cfg, err := parseChatConfig(os.Args[1:], os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "proxy-chatbot: %v\n", err)
		os.Exit(1)
	}

	if err := runChatbot(context.Background(), cfg, os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "proxy-chatbot: %v\n", err)
		os.Exit(1)
	}
}
