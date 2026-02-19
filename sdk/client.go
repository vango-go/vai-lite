// Package vai provides the Vai SDK for Go.
//
// This repo is intentionally trimmed down to focus on `Run` / `RunStream` and tools.
// The SDK runs in-process (direct mode) and calls providers directly.
package vai

import (
	"log/slog"
	"os"

	"github.com/vango-go/vai-lite/pkg/core"
	"github.com/vango-go/vai-lite/pkg/core/providers/anthropic"
	"github.com/vango-go/vai-lite/pkg/core/providers/cerebras"
	"github.com/vango-go/vai-lite/pkg/core/providers/gemini"
	"github.com/vango-go/vai-lite/pkg/core/providers/gemini_oauth"
	"github.com/vango-go/vai-lite/pkg/core/providers/groq"
	"github.com/vango-go/vai-lite/pkg/core/providers/oai_resp"
	"github.com/vango-go/vai-lite/pkg/core/providers/openai"
	"github.com/vango-go/vai-lite/pkg/core/providers/openrouter"
	"github.com/vango-go/vai-lite/pkg/core/voice"
	"github.com/vango-go/vai-lite/pkg/core/voice/stt"
	"github.com/vango-go/vai-lite/pkg/core/voice/tts"
)

// Client is the main entry point for the SDK.
type Client struct {
	Messages *MessagesService

	// Internal
	core          *core.Engine
	providerKeys  map[string]string
	logger        *slog.Logger
	voicePipeline *voice.Pipeline
}

// NewClient creates a new client (direct mode only).
// Provider keys are loaded from env by default (e.g. ANTHROPIC_API_KEY).
func NewClient(opts ...ClientOption) *Client {
	c := &Client{
		providerKeys: make(map[string]string),
		logger:       slog.Default(),
	}
	for _, opt := range opts {
		opt(c)
	}

	c.core = core.NewEngine(c.providerKeys)
	c.initProviders()
	c.initVoicePipeline()

	c.Messages = &MessagesService{client: c}
	return c
}

func (c *Client) initProviders() {
	// Anthropic
	if key := c.core.GetAPIKey("anthropic"); key != "" {
		c.core.RegisterProvider(newAnthropicAdapter(anthropic.New(key)))
	}

	// OpenAI Chat Completions + Responses (shared key)
	if key := c.core.GetAPIKey("openai"); key != "" {
		c.core.RegisterProvider(newOpenAIAdapter(openai.New(key)))
		c.core.RegisterProvider(newOaiRespAdapter(oai_resp.New(key)))
	}

	// Groq
	if key := c.core.GetAPIKey("groq"); key != "" {
		c.core.RegisterProvider(newGroqAdapter(groq.New(key)))
	}

	// Cerebras
	if key := c.core.GetAPIKey("cerebras"); key != "" {
		c.core.RegisterProvider(newCerebrasAdapter(cerebras.New(key)))
	}

	// OpenRouter
	if key := c.core.GetAPIKey("openrouter"); key != "" {
		c.core.RegisterProvider(newOpenRouterAdapter(openrouter.New(key)))
	}

	// Gemini OAuth (optional; uses ~/.config/vango/gemini-oauth-credentials.json)
	var geminiOAuthOpts []gemini_oauth.Option
	if projectID := os.Getenv("GEMINI_OAUTH_PROJECT_ID"); projectID != "" {
		geminiOAuthOpts = append(geminiOAuthOpts, gemini_oauth.WithProjectID(projectID))
	}
	if provider, err := gemini_oauth.New(geminiOAuthOpts...); err == nil {
		c.core.RegisterProvider(newGeminiOAuthAdapter(provider))
	} else {
		c.logger.Debug("gemini-oauth provider not initialized", "error", err)
	}

	// Gemini API key (also supports GOOGLE_API_KEY)
	geminiKey := c.core.GetAPIKey("gemini")
	if geminiKey == "" {
		geminiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if geminiKey != "" {
		c.core.RegisterProvider(newGeminiAdapter(gemini.New(geminiKey)))
	}
}

func (c *Client) initVoicePipeline() {
	cartesiaKey := c.getCartesiaAPIKey()
	if cartesiaKey != "" {
		c.voicePipeline = voice.NewPipeline(cartesiaKey)
	}
}

func (c *Client) getCartesiaAPIKey() string {
	if key, ok := c.providerKeys["cartesia"]; ok && key != "" {
		return key
	}
	return os.Getenv("CARTESIA_API_KEY")
}

func (c *Client) getSTTProvider() stt.Provider {
	if c.voicePipeline != nil {
		return c.voicePipeline.STTProvider()
	}
	cartesiaKey := c.getCartesiaAPIKey()
	if cartesiaKey == "" {
		return nil
	}
	return stt.NewCartesia(cartesiaKey)
}

func (c *Client) getTTSProvider() tts.Provider {
	if c.voicePipeline != nil {
		return c.voicePipeline.TTSProvider()
	}
	cartesiaKey := c.getCartesiaAPIKey()
	if cartesiaKey == "" {
		return nil
	}
	return tts.NewCartesia(cartesiaKey)
}

// VoicePipeline returns the voice pipeline when initialized.
func (c *Client) VoicePipeline() *voice.Pipeline {
	return c.voicePipeline
}

// Engine returns the core engine.
func (c *Client) Engine() *core.Engine {
	return c.core
}
