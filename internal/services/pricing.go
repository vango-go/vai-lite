package services

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

type PricingInputModality string

const (
	PricingInputModalityStandard PricingInputModality = "standard"
	PricingInputModalityAudio    PricingInputModality = "audio"
)

type PricingOutputModality string

const (
	PricingOutputModalityText  PricingOutputModality = "text"
	PricingOutputModalityImage PricingOutputModality = "image"
)

type PricingPromptTier string

const (
	PricingPromptTierStandard PricingPromptTier = "standard"
	PricingPromptTierLong     PricingPromptTier = "long"
)

type PricingContext struct {
	InputModality  PricingInputModality
	OutputModality PricingOutputModality
}

type HostedModelPrice struct {
	Model                                string
	DisplayName                          string
	Provider                             string
	PromptTierThresholdTokens            *int
	RetailInputMicrosUSDPer1M            int64
	RetailLongInputMicrosUSDPer1M        *int64
	RetailAudioInputMicrosUSDPer1M       *int64
	RetailCachedInputMicrosUSDPer1M      *int64
	RetailLongCachedInputMicrosUSDPer1M  *int64
	RetailAudioCachedInputMicrosUSDPer1M *int64
	RetailCacheWrite5mMicrosUSDPer1M     *int64
	RetailCacheWrite1hMicrosUSDPer1M     *int64
	RetailCacheStorageMicrosUSDPer1MHour *int64
	RetailOutputMicrosUSDPer1M           int64
	RetailLongOutputMicrosUSDPer1M       *int64
	RetailImageOutputMicrosUSDPer1M      *int64
	MinimumChargeCents                   int64
}

type PricingSnapshot struct {
	CatalogVersion                       string                `json:"catalog_version"`
	Model                                string                `json:"model"`
	Provider                             string                `json:"provider"`
	InputModality                        PricingInputModality  `json:"input_modality,omitempty"`
	OutputModality                       PricingOutputModality `json:"output_modality,omitempty"`
	PromptTier                           PricingPromptTier     `json:"prompt_tier,omitempty"`
	AppliedInputMicrosUSDPer1M           int64                 `json:"applied_input_micros_usd_per_1m"`
	AppliedCachedInputMicrosUSDPer1M     *int64                `json:"applied_cached_input_micros_usd_per_1m,omitempty"`
	AppliedCacheWriteMicrosUSDPer1M      *int64                `json:"applied_cache_write_micros_usd_per_1m,omitempty"`
	AppliedOutputMicrosUSDPer1M          int64                 `json:"applied_output_micros_usd_per_1m"`
	RetailInputMicrosUSDPer1M            int64                 `json:"retail_input_micros_usd_per_1m"`
	RetailLongInputMicrosUSDPer1M        *int64                `json:"retail_long_input_micros_usd_per_1m,omitempty"`
	RetailAudioInputMicrosUSDPer1M       *int64                `json:"retail_audio_input_micros_usd_per_1m,omitempty"`
	RetailCachedInputMicrosUSDPer1M      *int64                `json:"retail_cached_input_micros_usd_per_1m,omitempty"`
	RetailLongCachedInputMicrosUSDPer1M  *int64                `json:"retail_long_cached_input_micros_usd_per_1m,omitempty"`
	RetailAudioCachedInputMicrosUSDPer1M *int64                `json:"retail_audio_cached_input_micros_usd_per_1m,omitempty"`
	RetailCacheWrite5mMicrosUSDPer1M     *int64                `json:"retail_cache_write_5m_micros_usd_per_1m,omitempty"`
	RetailCacheWrite1hMicrosUSDPer1M     *int64                `json:"retail_cache_write_1h_micros_usd_per_1m,omitempty"`
	RetailCacheStorageMicrosUSDPer1MHour *int64                `json:"retail_cache_storage_micros_usd_per_1m_per_hour,omitempty"`
	RetailOutputMicrosUSDPer1M           int64                 `json:"retail_output_micros_usd_per_1m"`
	RetailLongOutputMicrosUSDPer1M       *int64                `json:"retail_long_output_micros_usd_per_1m,omitempty"`
	RetailImageOutputMicrosUSDPer1M      *int64                `json:"retail_image_output_micros_usd_per_1m,omitempty"`
	PromptTierThresholdTokens            *int                  `json:"prompt_tier_threshold_tokens,omitempty"`
	MinimumChargeCents                   int64                 `json:"minimum_charge_cents"`
	BilledCents                          int64                 `json:"billed_cents"`
}

type PricingEstimate struct {
	BilledCents int64
	Snapshot    PricingSnapshot
}

type PricingCatalog struct {
	Version string
	ByModel map[string]HostedModelPrice
}

func DefaultPricingCatalog() *PricingCatalog {
	specs := []HostedModelPrice{
		anthropicPrice("anthropic/claude-opus-4.6", "Claude Opus 4.6", "5.00", "6.25", "10.00", "0.50", "25.00"),
		anthropicPrice("anthropic/claude-opus-4.5", "Claude Opus 4.5", "5.00", "6.25", "10.00", "0.50", "25.00"),
		anthropicPrice("anthropic/claude-opus-4.1", "Claude Opus 4.1", "15.00", "18.75", "30.00", "1.50", "75.00"),
		anthropicPrice("anthropic/claude-opus-4", "Claude Opus 4", "15.00", "18.75", "30.00", "1.50", "75.00"),
		anthropicPrice("anthropic/claude-sonnet-4.6", "Claude Sonnet 4.6", "3.00", "3.75", "6.00", "0.30", "15.00"),
		anthropicPrice("anthropic/claude-sonnet-4.5", "Claude Sonnet 4.5", "3.00", "3.75", "6.00", "0.30", "15.00"),
		anthropicPrice("anthropic/claude-sonnet-4", "Claude Sonnet 4", "3.00", "3.75", "6.00", "0.30", "15.00"),
		anthropicPrice("anthropic/claude-sonnet-3.7", "Claude Sonnet 3.7", "3.00", "3.75", "6.00", "0.30", "15.00"),
		anthropicPrice("anthropic/claude-haiku-4.5", "Claude Haiku 4.5", "1.00", "1.25", "2.00", "0.10", "5.00"),
		anthropicPrice("anthropic/claude-haiku-3.5", "Claude Haiku 3.5", "0.80", "1.00", "1.60", "0.08", "4.00"),
		anthropicPrice("anthropic/claude-opus-3", "Claude Opus 3", "15.00", "18.75", "30.00", "1.50", "75.00"),
		anthropicPrice("anthropic/claude-haiku-3", "Claude Haiku 3", "0.25", "0.30", "0.50", "0.03", "1.25"),
		anthropicPrice("anthropic/claude-sonnet-4-6", "Claude Sonnet 4.6", "3.00", "3.75", "6.00", "0.30", "15.00"),
		anthropicPrice("anthropic/claude-sonnet-4-5", "Claude Sonnet 4.5", "3.00", "3.75", "6.00", "0.30", "15.00"),
		anthropicPrice("anthropic/claude-haiku-4-5", "Claude Haiku 4.5", "1.00", "1.25", "2.00", "0.10", "5.00"),
		anthropicPrice("anthropic/claude-haiku-3-5", "Claude Haiku 3.5", "0.80", "1.00", "1.60", "0.08", "4.00"),
		{Model: "groq/llama-3.3-70b-versatile", DisplayName: "Llama 3.3 70B Versatile", Provider: "groq", RetailInputMicrosUSDPer1M: mustUSDToMicros("0.59"), RetailOutputMicrosUSDPer1M: mustUSDToMicros("0.79"), MinimumChargeCents: 1},

		openAIPrice("oai-resp/gpt-5.4", "GPT-5.4 (<272K)", "2.50", "0.25", "15.00"),
		openAIPrice("oai-resp/gpt-5.4-long", "GPT-5.4 (>272K)", "5.00", "0.50", "22.50"),
		openAIPrice("oai-resp/gpt-5.4-pro", "GPT-5.4 Pro (<272K)", "30.00", "", "180.00"),
		openAIPrice("oai-resp/gpt-5.4-pro-long", "GPT-5.4 Pro (>272K)", "60.00", "", "270.00"),
		openAIPrice("oai-resp/gpt-5.3-chat-latest", "GPT-5.3 Chat Latest", "1.75", "0.175", "14.00"),
		openAIPrice("oai-resp/gpt-5.3-codex", "GPT-5.3 Codex", "1.75", "0.175", "14.00"),
		openAIPrice("oai-resp/gpt-5.2", "GPT-5.2", "1.75", "0.175", "14.00"),
		openAIPrice("oai-resp/gpt-5.2-chat-latest", "GPT-5.2 Chat Latest", "1.75", "0.175", "14.00"),
		openAIPrice("oai-resp/gpt-5.2-codex", "GPT-5.2 Codex", "1.75", "0.175", "14.00"),
		openAIPrice("oai-resp/gpt-5.2-pro", "GPT-5.2 Pro", "21.00", "", "168.00"),
		openAIPrice("oai-resp/gpt-5.1", "GPT-5.1", "1.25", "0.125", "10.00"),
		openAIPrice("oai-resp/gpt-5.1-chat-latest", "GPT-5.1 Chat Latest", "1.25", "0.125", "10.00"),
		openAIPrice("oai-resp/gpt-5.1-codex", "GPT-5.1 Codex", "1.25", "0.125", "10.00"),
		openAIPrice("oai-resp/gpt-5.1-codex-max", "GPT-5.1 Codex Max", "1.25", "0.125", "10.00"),
		openAIPrice("oai-resp/gpt-5.1-codex-mini", "GPT-5.1 Codex Mini", "0.25", "0.025", "2.00"),
		openAIPrice("oai-resp/gpt-5", "GPT-5", "1.25", "0.125", "10.00"),
		openAIPrice("oai-resp/gpt-5-chat-latest", "GPT-5 Chat Latest", "1.25", "0.125", "10.00"),
		openAIPrice("oai-resp/gpt-5-codex", "GPT-5 Codex", "1.25", "0.125", "10.00"),
		openAIPrice("oai-resp/gpt-5-mini", "GPT-5 Mini", "0.25", "0.025", "2.00"),
		openAIPrice("oai-resp/gpt-5-nano", "GPT-5 Nano", "0.05", "0.005", "0.40"),
		openAIPrice("oai-resp/gpt-5-pro", "GPT-5 Pro", "15.00", "", "120.00"),
		openAIPrice("oai-resp/gpt-5-search-api", "GPT-5 Search API", "1.25", "0.125", "10.00"),
		openAIPrice("oai-resp/gpt-4.1", "GPT-4.1", "2.00", "0.50", "8.00"),
		openAIPrice("oai-resp/gpt-4.1-mini", "GPT-4.1 Mini", "0.40", "0.10", "1.60"),
		openAIPrice("oai-resp/gpt-4.1-nano", "GPT-4.1 Nano", "0.10", "0.025", "0.40"),
		openAIPrice("oai-resp/gpt-4o", "GPT-4o", "2.50", "1.25", "10.00"),
		openAIPrice("oai-resp/gpt-4o-2024-05-13", "GPT-4o 2024-05-13", "5.00", "", "15.00"),
		openAIPrice("oai-resp/gpt-4o-mini", "GPT-4o Mini", "0.15", "0.075", "0.60"),
		openAIPrice("oai-resp/gpt-realtime", "GPT Realtime", "4.00", "0.40", "16.00"),
		openAIPrice("oai-resp/gpt-realtime-1.5", "GPT Realtime 1.5", "4.00", "0.40", "16.00"),
		openAIPrice("oai-resp/gpt-realtime-mini", "GPT Realtime Mini", "0.60", "0.06", "2.40"),
		openAIPrice("oai-resp/gpt-4o-realtime-preview", "GPT-4o Realtime Preview", "5.00", "2.50", "20.00"),
		openAIPrice("oai-resp/gpt-4o-mini-realtime-preview", "GPT-4o Mini Realtime Preview", "0.60", "0.30", "2.40"),
		openAIPrice("oai-resp/gpt-audio", "GPT Audio", "2.50", "", "10.00"),
		openAIPrice("oai-resp/gpt-audio-1.5", "GPT Audio 1.5", "2.50", "", "10.00"),
		openAIPrice("oai-resp/gpt-audio-mini", "GPT Audio Mini", "0.60", "", "2.40"),
		openAIPrice("oai-resp/gpt-4o-audio-preview", "GPT-4o Audio Preview", "2.50", "", "10.00"),
		openAIPrice("oai-resp/gpt-4o-mini-audio-preview", "GPT-4o Mini Audio Preview", "0.15", "", "0.60"),
		openAIPrice("oai-resp/o1", "o1", "15.00", "7.50", "60.00"),
		openAIPrice("oai-resp/o1-pro", "o1 Pro", "150.00", "", "600.00"),
		openAIPrice("oai-resp/o3-pro", "o3 Pro", "20.00", "", "80.00"),
		openAIPrice("oai-resp/o3", "o3", "2.00", "0.50", "8.00"),
		openAIPrice("oai-resp/o3-deep-research", "o3 Deep Research", "10.00", "2.50", "40.00"),
		openAIPrice("oai-resp/o4-mini", "o4 Mini", "1.10", "0.275", "4.40"),
		openAIPrice("oai-resp/o4-mini-deep-research", "o4 Mini Deep Research", "2.00", "0.50", "8.00"),
		openAIPrice("oai-resp/o3-mini", "o3 Mini", "1.10", "0.55", "4.40"),
		openAIPrice("oai-resp/o1-mini", "o1 Mini", "1.10", "0.55", "4.40"),
		openAIPrice("oai-resp/codex-mini-latest", "Codex Mini Latest", "1.50", "0.375", "6.00"),
		openAIPrice("oai-resp/gpt-4o-mini-search-preview", "GPT-4o Mini Search Preview", "0.15", "", "0.60"),
		openAIPrice("oai-resp/gpt-4o-search-preview", "GPT-4o Search Preview", "2.50", "", "10.00"),
		openAIPrice("oai-resp/computer-use-preview", "Computer Use Preview", "3.00", "", "12.00"),
		openAIPrice("oai-resp/gpt-image-1.5", "GPT Image 1.5", "5.00", "1.25", "10.00"),
		openAIPrice("oai-resp/chatgpt-image-latest", "ChatGPT Image Latest", "5.00", "1.25", "10.00"),

		openAIPrice("openai/gpt-4.1", "GPT-4.1", "2.00", "0.50", "8.00"),
		openAIPrice("openai/gpt-4.1-mini", "GPT-4.1 Mini", "0.40", "0.10", "1.60"),
		openAIPrice("openai/gpt-4.1-nano", "GPT-4.1 Nano", "0.10", "0.025", "0.40"),
		openAIPrice("openai/gpt-4o", "GPT-4o", "2.50", "1.25", "10.00"),
		openAIPrice("openai/gpt-4o-mini", "GPT-4o Mini", "0.15", "0.075", "0.60"),

		openRouterPrice("openrouter/anthracite-org/magnum-v4-72b", "Magnum v4 72B", "3.00", "5.00", "", "", 1),
		openRouterPrice("openrouter/anthropic/claude-3-haiku", "Anthropic: Claude 3 Haiku", "0.25", "1.25", "0.03", "0.30", 1),
		openRouterPrice("openrouter/anthropic/claude-3.5-haiku", "Anthropic: Claude 3.5 Haiku", "0.80", "4.00", "0.08", "1.00", 1),
		openRouterPrice("openrouter/anthropic/claude-3.5-sonnet", "Anthropic: Claude 3.5 Sonnet", "6.00", "30.00", "0.60", "7.50", 1),
		openRouterPrice("openrouter/anthropic/claude-3.7-sonnet", "Anthropic: Claude 3.7 Sonnet", "3.00", "15.00", "0.30", "3.75", 1),
		openRouterPrice("openrouter/anthropic/claude-3.7-sonnet:thinking", "Anthropic: Claude 3.7 Sonnet (thinking)", "3.00", "15.00", "0.30", "3.75", 1),
		openRouterPrice("openrouter/anthropic/claude-haiku-4.5", "Anthropic: Claude Haiku 4.5", "1.00", "5.00", "0.10", "1.25", 1),
		openRouterPrice("openrouter/anthropic/claude-opus-4", "Anthropic: Claude Opus 4", "15.00", "75.00", "1.50", "18.75", 1),
		openRouterPrice("openrouter/anthropic/claude-opus-4.1", "Anthropic: Claude Opus 4.1", "15.00", "75.00", "1.50", "18.75", 1),
		openRouterPrice("openrouter/anthropic/claude-opus-4.5", "Anthropic: Claude Opus 4.5", "5.00", "25.00", "0.50", "6.25", 1),
		openRouterPrice("openrouter/anthropic/claude-opus-4.6", "Anthropic: Claude Opus 4.6", "5.00", "25.00", "0.50", "6.25", 1),
		openRouterPrice("openrouter/anthropic/claude-sonnet-4", "Anthropic: Claude Sonnet 4", "3.00", "15.00", "0.30", "3.75", 1),
		openRouterPrice("openrouter/anthropic/claude-sonnet-4.5", "Anthropic: Claude Sonnet 4.5", "3.00", "15.00", "0.30", "3.75", 1),
		openRouterPrice("openrouter/anthropic/claude-sonnet-4.6", "Anthropic: Claude Sonnet 4.6", "3.00", "15.00", "0.30", "3.75", 1),
		openRouterPrice("openrouter/arcee-ai/coder-large", "Arcee AI: Coder Large", "0.50", "0.80", "", "", 1),
		openRouterPrice("openrouter/arcee-ai/maestro-reasoning", "Arcee AI: Maestro Reasoning", "0.90", "3.30", "", "", 1),
		openRouterPrice("openrouter/baidu/ernie-4.5-vl-424b-a47b", "Baidu: ERNIE 4.5 VL 424B A47B", "0.42", "1.25", "", "", 1),
		openRouterPrice("openrouter/bytedance-seed/seed-1.6", "ByteDance Seed: Seed 1.6", "0.25", "2.00", "", "", 1),
		openRouterPrice("openrouter/bytedance-seed/seed-1.6-flash", "ByteDance Seed: Seed 1.6 Flash", "0.07", "0.30", "", "", 1),
		openRouterPrice("openrouter/bytedance-seed/seed-2.0-mini", "ByteDance Seed: Seed-2.0-Mini", "0.10", "0.40", "", "", 1),
		openRouterPrice("openrouter/bytedance/ui-tars-1.5-7b", "ByteDance: UI-TARS 7B", "0.10", "0.20", "", "", 1),
		openRouterPrice("openrouter/cognitivecomputations/dolphin-mistral-24b-venice-edition:free", "Venice: Uncensored (free)", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/cohere/command-a", "Cohere: Command A", "2.50", "10.00", "", "", 1),
		openRouterPrice("openrouter/cohere/command-r-08-2024", "Cohere: Command R (08-2024)", "0.15", "0.60", "", "", 1),
		openRouterPrice("openrouter/cohere/command-r-plus-08-2024", "Cohere: Command R+ (08-2024)", "2.50", "10.00", "", "", 1),
		openRouterPrice("openrouter/cohere/command-r7b-12-2024", "Cohere: Command R7B (12-2024)", "0.04", "0.15", "", "", 1),
		openRouterPrice("openrouter/deepcogito/cogito-v2.1-671b", "Deep Cogito: Cogito v2.1 671B", "1.25", "1.25", "", "", 1),
		openRouterPrice("openrouter/deepseek/deepseek-chat", "DeepSeek: DeepSeek V3", "0.32", "0.89", "", "", 1),
		openRouterPrice("openrouter/deepseek/deepseek-chat-v3-0324", "DeepSeek: DeepSeek V3 0324", "0.20", "0.77", "0.13", "", 1),
		openRouterPrice("openrouter/deepseek/deepseek-chat-v3.1", "DeepSeek: DeepSeek V3.1", "0.15", "0.75", "", "", 1),
		openRouterPrice("openrouter/deepseek/deepseek-r1", "DeepSeek: R1", "0.70", "2.50", "", "", 1),
		openRouterPrice("openrouter/deepseek/deepseek-r1-0528", "DeepSeek: R1 0528", "0.45", "2.15", "0.22", "", 1),
		openRouterPrice("openrouter/deepseek/deepseek-r1-distill-llama-70b", "DeepSeek: R1 Distill Llama 70B", "0.70", "0.80", "", "", 1),
		openRouterPrice("openrouter/deepseek/deepseek-r1-distill-qwen-32b", "DeepSeek: R1 Distill Qwen 32B", "0.29", "0.29", "", "", 1),
		openRouterPrice("openrouter/deepseek/deepseek-v3.1-terminus", "DeepSeek: DeepSeek V3.1 Terminus", "0.21", "0.79", "0.13", "", 1),
		openRouterPrice("openrouter/deepseek/deepseek-v3.1-terminus:exacto", "DeepSeek: DeepSeek V3.1 Terminus (exacto)", "0.21", "0.79", "0.17", "", 1),
		openRouterPrice("openrouter/deepseek/deepseek-v3.2", "DeepSeek: DeepSeek V3.2", "0.25", "0.40", "", "", 1),
		openRouterPrice("openrouter/deepseek/deepseek-v3.2-exp", "DeepSeek: DeepSeek V3.2 Exp", "0.27", "0.41", "", "", 1),
		openRouterPrice("openrouter/deepseek/deepseek-v3.2-speciale", "DeepSeek: DeepSeek V3.2 Speciale", "0.40", "1.20", "0.20", "", 1),
		openRouterPrice("openrouter/eleutherai/llemma_7b", "EleutherAI: Llemma 7b", "0.80", "1.20", "", "", 1),
		openRouterPrice("openrouter/essentialai/rnj-1-instruct", "EssentialAI: Rnj 1 Instruct", "0.15", "0.15", "", "", 1),
		openRouterPrice("openrouter/google/gemma-2-27b-it", "Google: Gemma 2 27B", "0.65", "0.65", "", "", 1),
		openRouterPrice("openrouter/google/gemma-2-9b-it", "Google: Gemma 2 9B", "0.03", "0.09", "", "", 1),
		openRouterPrice("openrouter/google/gemma-3-12b-it", "Google: Gemma 3 12B", "0.04", "0.13", "", "", 1),
		openRouterPrice("openrouter/google/gemma-3-12b-it:free", "Google: Gemma 3 12B (free)", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/google/gemma-3-27b-it", "Google: Gemma 3 27B", "0.04", "0.15", "0.02", "", 1),
		openRouterPrice("openrouter/google/gemma-3-27b-it:free", "Google: Gemma 3 27B (free)", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/google/gemma-3-4b-it", "Google: Gemma 3 4B", "0.04", "0.08", "", "", 1),
		openRouterPrice("openrouter/google/gemma-3-4b-it:free", "Google: Gemma 3 4B (free)", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/google/gemma-3n-e2b-it:free", "Google: Gemma 3n 2B (free)", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/google/gemma-3n-e4b-it", "Google: Gemma 3n 4B", "0.02", "0.04", "", "", 1),
		openRouterPrice("openrouter/google/gemma-3n-e4b-it:free", "Google: Gemma 3n 4B (free)", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/gryphe/mythomax-l2-13b", "MythoMax 13B", "0.06", "0.06", "", "", 1),
		openRouterPrice("openrouter/ibm-granite/granite-4.0-h-micro", "IBM: Granite 4.0 Micro", "0.02", "0.11", "", "", 1),
		openRouterPrice("openrouter/inception/mercury", "Inception: Mercury", "0.25", "0.75", "0.02", "", 1),
		openRouterPrice("openrouter/inception/mercury-2", "Inception: Mercury 2", "0.25", "0.75", "0.02", "", 1),
		openRouterPrice("openrouter/inception/mercury-coder", "Inception: Mercury Coder", "0.25", "0.75", "0.02", "", 1),
		openRouterPrice("openrouter/inflection/inflection-3-pi", "Inflection: Inflection 3 Pi", "2.50", "10.00", "", "", 1),
		openRouterPrice("openrouter/inflection/inflection-3-productivity", "Inflection: Inflection 3 Productivity", "2.50", "10.00", "", "", 1),
		openRouterPrice("openrouter/kwaipilot/kat-coder-pro", "Kwaipilot: KAT-Coder-Pro V1", "0.21", "0.83", "0.04", "", 1),
		openRouterPrice("openrouter/liquid/lfm-2.5-1.2b-instruct:free", "LiquidAI: LFM2.5-1.2B-Instruct (free)", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/liquid/lfm-2.5-1.2b-thinking:free", "LiquidAI: LFM2.5-1.2B-Thinking (free)", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/liquid/lfm2-8b-a1b", "LiquidAI: LFM2-8B-A1B", "0.01", "0.02", "", "", 1),
		openRouterPrice("openrouter/mancer/weaver", "Mancer: Weaver (alpha)", "0.75", "1.00", "", "", 1),
		openRouterPrice("openrouter/meituan/longcat-flash-chat", "Meituan: LongCat Flash Chat", "0.20", "0.80", "0.20", "", 1),
		openRouterPrice("openrouter/meta-llama/llama-3.2-3b-instruct:free", "Meta: Llama 3.2 3B Instruct (free)", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/meta-llama/llama-3.3-70b-instruct", "Meta: Llama 3.3 70B Instruct", "0.10", "0.32", "", "", 1),
		openRouterPrice("openrouter/meta-llama/llama-3.3-70b-instruct:free", "Meta: Llama 3.3 70B Instruct (free)", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/meta-llama/llama-4-maverick", "Meta: Llama 4 Maverick", "0.15", "0.60", "", "", 1),
		openRouterPrice("openrouter/meta-llama/llama-4-scout", "Meta: Llama 4 Scout", "0.08", "0.30", "", "", 1),
		openRouterPrice("openrouter/meta-llama/llama-guard-2-8b", "Meta: LlamaGuard 2 8B", "0.20", "0.20", "", "", 1),
		openRouterPrice("openrouter/meta-llama/llama-guard-3-8b", "Llama Guard 3 8B", "0.02", "0.06", "", "", 1),
		openRouterPrice("openrouter/meta-llama/llama-guard-4-12b", "Meta: Llama Guard 4 12B", "0.18", "0.18", "", "", 1),
		openRouterPrice("openrouter/microsoft/phi-4", "Microsoft: Phi 4", "0.06", "0.14", "", "", 1),
		openRouterPrice("openrouter/microsoft/wizardlm-2-8x22b", "WizardLM-2 8x22B", "0.62", "0.62", "", "", 1),
		openRouterPrice("openrouter/minimax/minimax-m2", "MiniMax: MiniMax M2", "0.26", "1.00", "0.03", "", 1),
		openRouterPrice("openrouter/minimax/minimax-m2-her", "MiniMax: MiniMax M2-her", "0.30", "1.20", "0.03", "", 1),
		openRouterPrice("openrouter/minimax/minimax-m2.1", "MiniMax: MiniMax M2.1", "0.27", "0.95", "0.03", "", 1),
		openRouterPrice("openrouter/minimax/minimax-m2.5", "MiniMax: MiniMax M2.5", "0.29", "1.20", "0.03", "", 1),
		openRouterPrice("openrouter/mistralai/mistral-small-creative", "Mistral: Mistral Small Creative", "0.10", "0.30", "", "", 1),
		openRouterPrice("openrouter/mistralai/mixtral-8x22b-instruct", "Mistral: Mixtral 8x22B Instruct", "2.00", "6.00", "", "", 1),
		openRouterPrice("openrouter/mistralai/mixtral-8x7b-instruct", "Mistral: Mixtral 8x7B Instruct", "0.54", "0.54", "", "", 1),
		openRouterPrice("openrouter/mistralai/pixtral-large-2411", "Mistral: Pixtral Large 2411", "2.00", "6.00", "", "", 1),
		openRouterPrice("openrouter/mistralai/voxtral-small-24b-2507", "Mistral: Voxtral Small 24B 2507", "0.10", "0.30", "", "", 1),
		openRouterPrice("openrouter/moonshotai/kimi-k2", "MoonshotAI: Kimi K2 0711", "0.55", "2.20", "", "", 1),
		openRouterPrice("openrouter/moonshotai/kimi-k2-0905", "MoonshotAI: Kimi K2 0905", "0.40", "2.00", "0.15", "", 1),
		openRouterPrice("openrouter/moonshotai/kimi-k2-0905:exacto", "MoonshotAI: Kimi K2 0905 (exacto)", "0.60", "2.50", "", "", 1),
		openRouterPrice("openrouter/moonshotai/kimi-k2-thinking", "MoonshotAI: Kimi K2 Thinking", "0.47", "2.00", "0.14", "", 1),
		openRouterPrice("openrouter/moonshotai/kimi-k2.5", "MoonshotAI: Kimi K2.5", "0.45", "2.20", "0.22", "", 1),
		openRouterPrice("openrouter/morph/morph-v3-fast", "Morph: Morph V3 Fast", "0.80", "1.20", "", "", 1),
		openRouterPrice("openrouter/morph/morph-v3-large", "Morph: Morph V3 Large", "0.90", "1.90", "", "", 1),
		openRouterPrice("openrouter/nvidia/nemotron-nano-9b-v2:free", "NVIDIA: Nemotron Nano 9B V2 (free)", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/openai/gpt-oss-120b", "OpenAI: gpt-oss-120b", "0.04", "0.19", "", "", 1),
		openRouterPrice("openrouter/openai/gpt-oss-120b:exacto", "OpenAI: gpt-oss-120b (exacto)", "0.04", "0.19", "", "", 1),
		openRouterPrice("openrouter/openai/gpt-oss-120b:free", "OpenAI: gpt-oss-120b (free)", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/openai/gpt-oss-20b", "OpenAI: gpt-oss-20b", "0.03", "0.14", "", "", 1),
		openRouterPrice("openrouter/openai/gpt-oss-20b:free", "OpenAI: gpt-oss-20b (free)", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/openai/gpt-oss-safeguard-20b", "OpenAI: gpt-oss-safeguard-20b", "0.07", "0.30", "0.04", "", 1),
		openRouterPrice("openrouter/openrouter/auto", "Auto Router", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/openrouter/bodybuilder", "Body Builder (beta)", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/openrouter/free", "Free Models Router", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/perplexity/sonar", "Perplexity: Sonar", "1.00", "1.00", "", "", 1),
		openRouterPrice("openrouter/perplexity/sonar-deep-research", "Perplexity: Sonar Deep Research", "2.00", "8.00", "", "", 1),
		openRouterPrice("openrouter/perplexity/sonar-pro", "Perplexity: Sonar Pro", "3.00", "15.00", "", "", 1),
		openRouterPrice("openrouter/perplexity/sonar-pro-search", "Perplexity: Sonar Pro Search", "3.00", "15.00", "", "", 1),
		openRouterPrice("openrouter/perplexity/sonar-reasoning-pro", "Perplexity: Sonar Reasoning Pro", "2.00", "8.00", "", "", 1),
		openRouterPrice("openrouter/prime-intellect/intellect-3", "Prime Intellect: INTELLECT-3", "0.20", "1.10", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-235b-a22b", "Qwen: Qwen3 235B A22B", "0.45", "1.82", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-235b-a22b-2507", "Qwen: Qwen3 235B A22B Instruct 2507", "0.07", "0.10", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-235b-a22b-thinking-2507", "Qwen: Qwen3 235B A22B Thinking 2507", "0.11", "0.60", "0.06", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-30b-a3b", "Qwen: Qwen3 30B A3B", "0.08", "0.28", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-30b-a3b-instruct-2507", "Qwen: Qwen3 30B A3B Instruct 2507", "0.09", "0.30", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-30b-a3b-thinking-2507", "Qwen: Qwen3 30B A3B Thinking 2507", "0.05", "0.34", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-32b", "Qwen: Qwen3 32B", "0.08", "0.24", "0.04", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-4b:free", "Qwen: Qwen3 4B (free)", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/qwen/qwen3-8b", "Qwen: Qwen3 8B", "0.05", "0.40", "0.05", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-coder", "Qwen: Qwen3 Coder 480B A35B", "0.22", "1.00", "0.02", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-coder-30b-a3b-instruct", "Qwen: Qwen3 Coder 30B A3B Instruct", "0.07", "0.27", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-coder-flash", "Qwen: Qwen3 Coder Flash", "0.20", "0.97", "0.04", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-coder-next", "Qwen: Qwen3 Coder Next", "0.12", "0.75", "0.06", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-coder-plus", "Qwen: Qwen3 Coder Plus", "0.65", "3.25", "0.13", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-coder:exacto", "Qwen: Qwen3 Coder 480B A35B (exacto)", "0.22", "1.80", "0.02", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-coder:free", "Qwen: Qwen3 Coder 480B A35B (free)", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/qwen/qwen3-max", "Qwen: Qwen3 Max", "1.20", "6.00", "0.24", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-max-thinking", "Qwen: Qwen3 Max Thinking", "0.78", "3.90", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-next-80b-a3b-instruct", "Qwen: Qwen3 Next 80B A3B Instruct", "0.09", "1.10", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-next-80b-a3b-instruct:free", "Qwen: Qwen3 Next 80B A3B Instruct (free)", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/qwen/qwen3-next-80b-a3b-thinking", "Qwen: Qwen3 Next 80B A3B Thinking", "0.15", "1.20", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-vl-235b-a22b-instruct", "Qwen: Qwen3 VL 235B A22B Instruct", "0.20", "0.88", "0.11", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-vl-235b-a22b-thinking", "Qwen: Qwen3 VL 235B A22B Thinking", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/qwen/qwen3-vl-30b-a3b-instruct", "Qwen: Qwen3 VL 30B A3B Instruct", "0.13", "0.52", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-vl-30b-a3b-thinking", "Qwen: Qwen3 VL 30B A3B Thinking", "0.00", "0.00", "", "", 0),
		openRouterPrice("openrouter/qwen/qwen3-vl-32b-instruct", "Qwen: Qwen3 VL 32B Instruct", "0.10", "0.42", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-vl-8b-instruct", "Qwen: Qwen3 VL 8B Instruct", "0.08", "0.50", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3-vl-8b-thinking", "Qwen: Qwen3 VL 8B Thinking", "0.12", "1.36", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3.5-122b-a10b", "Qwen: Qwen3.5-122B-A10B", "0.26", "2.08", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3.5-27b", "Qwen: Qwen3.5-27B", "0.20", "1.56", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3.5-35b-a3b", "Qwen: Qwen3.5-35B-A3B", "0.16", "1.30", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3.5-397b-a17b", "Qwen: Qwen3.5 397B A17B", "0.39", "2.34", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3.5-flash-02-23", "Qwen: Qwen3.5-Flash", "0.10", "0.40", "", "", 1),
		openRouterPrice("openrouter/qwen/qwen3.5-plus-02-15", "Qwen: Qwen3.5 Plus 2026-02-15", "0.26", "1.56", "", "", 1),
		openRouterPrice("openrouter/qwen/qwq-32b", "Qwen: QwQ 32B", "0.15", "0.40", "", "", 1),
		openRouterPrice("openrouter/x-ai/grok-3", "xAI: Grok 3", "3.00", "15.00", "0.75", "", 1),
		openRouterPrice("openrouter/x-ai/grok-3-beta", "xAI: Grok 3 Beta", "3.00", "15.00", "0.75", "", 1),
		openRouterPrice("openrouter/x-ai/grok-3-mini", "xAI: Grok 3 Mini", "0.30", "0.50", "0.07", "", 1),
		openRouterPrice("openrouter/x-ai/grok-3-mini-beta", "xAI: Grok 3 Mini Beta", "0.30", "0.50", "0.07", "", 1),
		openRouterPrice("openrouter/x-ai/grok-4", "xAI: Grok 4", "3.00", "15.00", "0.75", "", 1),
		openRouterPrice("openrouter/x-ai/grok-4-fast", "xAI: Grok 4 Fast", "0.20", "0.50", "0.05", "", 1),
		openRouterPrice("openrouter/x-ai/grok-4.1-fast", "xAI: Grok 4.1 Fast", "0.20", "0.50", "0.05", "", 1),
		openRouterPrice("openrouter/x-ai/grok-code-fast-1", "xAI: Grok Code Fast 1", "0.20", "1.50", "0.02", "", 1),
		openRouterPrice("openrouter/z-ai/glm-4.6", "Z.ai: GLM 4.6", "0.39", "1.90", "", "", 1),
		openRouterPrice("openrouter/z-ai/glm-4.6:exacto", "Z.ai: GLM 4.6 (exacto)", "0.44", "1.76", "0.11", "", 1),
		openRouterPrice("openrouter/z-ai/glm-4.6v", "Z.ai: GLM 4.6V", "0.30", "0.90", "", "", 1),
		openRouterPrice("openrouter/z-ai/glm-4.7", "Z.ai: GLM 4.7", "0.38", "1.98", "0.19", "", 1),
		openRouterPrice("openrouter/z-ai/glm-4.7-flash", "Z.ai: GLM 4.7 Flash", "0.06", "0.40", "0.01", "", 1),
		openRouterPrice("openrouter/z-ai/glm-5", "Z.ai: GLM 5", "0.80", "2.56", "0.16", "", 1),
	}

	specs = append(specs,
		geminiPrices("gemini-3.1-pro-preview", "Gemini 3.1 Pro Preview", geminiPriceSpec{
			InputUSDPer1M:            "2.00",
			LongInputUSDPer1M:        "4.00",
			CachedInputUSDPer1M:      "0.20",
			LongCachedInputUSDPer1M:  "0.40",
			CacheStorageUSDPer1MHour: "4.50",
			OutputUSDPer1M:           "12.00",
			LongOutputUSDPer1M:       "18.00",
		})...,
	)
	specs = append(specs,
		geminiPrices("gemini-3.1-flash-lite-preview", "Gemini 3.1 Flash-Lite Preview", geminiPriceSpec{
			InputUSDPer1M:            "0.25",
			AudioInputUSDPer1M:       "0.50",
			CachedInputUSDPer1M:      "0.025",
			AudioCachedInputUSDPer1M: "0.05",
			CacheStorageUSDPer1MHour: "1.00",
			OutputUSDPer1M:           "1.50",
		})...,
	)
	specs = append(specs,
		geminiPrices("gemini-3-pro-preview", "Gemini 3 Pro Preview", geminiPriceSpec{
			InputUSDPer1M:            "2.00",
			LongInputUSDPer1M:        "4.00",
			CachedInputUSDPer1M:      "0.20",
			LongCachedInputUSDPer1M:  "0.40",
			CacheStorageUSDPer1MHour: "4.50",
			OutputUSDPer1M:           "12.00",
			LongOutputUSDPer1M:       "18.00",
		})...,
	)
	specs = append(specs,
		geminiPrices("gemini-3-flash-preview", "Gemini 3 Flash Preview", geminiPriceSpec{
			InputUSDPer1M:            "0.50",
			AudioInputUSDPer1M:       "1.00",
			CachedInputUSDPer1M:      "0.05",
			AudioCachedInputUSDPer1M: "0.10",
			CacheStorageUSDPer1MHour: "1.00",
			OutputUSDPer1M:           "3.00",
		})...,
	)
	specs = append(specs,
		geminiPrices("gemini-2.5-pro", "Gemini 2.5 Pro", geminiPriceSpec{
			InputUSDPer1M:            "1.25",
			LongInputUSDPer1M:        "2.50",
			CachedInputUSDPer1M:      "0.125",
			LongCachedInputUSDPer1M:  "0.25",
			CacheStorageUSDPer1MHour: "4.50",
			OutputUSDPer1M:           "10.00",
			LongOutputUSDPer1M:       "15.00",
		})...,
	)
	specs = append(specs,
		geminiPrices("gemini-2.5-flash", "Gemini 2.5 Flash", geminiPriceSpec{
			InputUSDPer1M:            "0.30",
			AudioInputUSDPer1M:       "1.00",
			CachedInputUSDPer1M:      "0.03",
			AudioCachedInputUSDPer1M: "0.10",
			CacheStorageUSDPer1MHour: "1.00",
			OutputUSDPer1M:           "2.50",
		})...,
	)
	specs = append(specs,
		geminiPrices("gemini-2.5-flash-lite", "Gemini 2.5 Flash-Lite", geminiPriceSpec{
			InputUSDPer1M:            "0.10",
			AudioInputUSDPer1M:       "0.30",
			CachedInputUSDPer1M:      "0.01",
			AudioCachedInputUSDPer1M: "0.03",
			CacheStorageUSDPer1MHour: "1.00",
			OutputUSDPer1M:           "0.40",
		})...,
	)
	specs = append(specs,
		geminiPrices("gemini-2.5-flash-lite-preview", "Gemini 2.5 Flash-Lite Preview", geminiPriceSpec{
			InputUSDPer1M:            "0.10",
			AudioInputUSDPer1M:       "0.30",
			CachedInputUSDPer1M:      "0.01",
			AudioCachedInputUSDPer1M: "0.03",
			CacheStorageUSDPer1MHour: "1.00",
			OutputUSDPer1M:           "0.40",
		})...,
	)
	specs = append(specs,
		geminiPrices("gemini-2.0-flash", "Gemini 2.0 Flash", geminiPriceSpec{
			InputUSDPer1M:            "0.10",
			AudioInputUSDPer1M:       "0.70",
			CachedInputUSDPer1M:      "0.025",
			AudioCachedInputUSDPer1M: "0.175",
			CacheStorageUSDPer1MHour: "1.00",
			OutputUSDPer1M:           "0.40",
		})...,
	)
	specs = append(specs,
		geminiPrices("gemini-2.0-flash-lite", "Gemini 2.0 Flash-Lite", geminiPriceSpec{
			InputUSDPer1M:  "0.075",
			OutputUSDPer1M: "0.30",
		})...,
	)
	specs = append(specs,
		geminiPrices("gemma-3", "Gemma 3", geminiPriceSpec{
			InputUSDPer1M:      "0.00",
			OutputUSDPer1M:     "0.00",
			MinimumChargeCents: 0,
		})...,
	)
	specs = append(specs,
		geminiPrices("gemma-3n", "Gemma 3n", geminiPriceSpec{
			InputUSDPer1M:      "0.00",
			OutputUSDPer1M:     "0.00",
			MinimumChargeCents: 0,
		})...,
	)
	specs = append(specs,
		geminiPrices("gemini-3.1-flash-image-preview", "Gemini 3.1 Flash Image Preview", geminiPriceSpec{
			InputUSDPer1M:       "0.50",
			OutputUSDPer1M:      "3.00",
			ImageOutputUSDPer1M: "60.00",
		})...,
	)
	specs = append(specs,
		geminiPrices("gemini-3-pro-image-preview", "Gemini 3 Pro Image Preview", geminiPriceSpec{
			InputUSDPer1M:       "2.00",
			OutputUSDPer1M:      "12.00",
			ImageOutputUSDPer1M: "120.00",
		})...,
	)
	specs = append(specs,
		geminiPrices("gemini-2.5-flash-image", "Gemini 2.5 Flash Image", geminiPriceSpec{
			InputUSDPer1M:       "0.30",
			OutputUSDPer1M:      "0.00",
			ImageOutputUSDPer1M: "30.00",
		})...,
	)

	byModel := make(map[string]HostedModelPrice, len(specs))
	for _, spec := range specs {
		byModel[strings.TrimSpace(spec.Model)] = spec
	}
	return &PricingCatalog{
		Version: "beta-2026-03-06-hosted-retail-catalog",
		ByModel: byModel,
	}
}

type geminiPriceSpec struct {
	InputUSDPer1M            string
	LongInputUSDPer1M        string
	AudioInputUSDPer1M       string
	CachedInputUSDPer1M      string
	LongCachedInputUSDPer1M  string
	AudioCachedInputUSDPer1M string
	CacheStorageUSDPer1MHour string
	OutputUSDPer1M           string
	LongOutputUSDPer1M       string
	ImageOutputUSDPer1M      string
	MinimumChargeCents       int64
}

func geminiPrices(model, displayName string, spec geminiPriceSpec) []HostedModelPrice {
	out := make([]HostedModelPrice, 0, 2)
	for _, provider := range []string{"gem-dev", "gem-vert"} {
		price := HostedModelPrice{
			Model:                                provider + "/" + model,
			DisplayName:                          displayName,
			Provider:                             provider,
			RetailInputMicrosUSDPer1M:            mustUSDToMicros(spec.InputUSDPer1M),
			RetailLongInputMicrosUSDPer1M:        optionalUSDToMicros(spec.LongInputUSDPer1M),
			RetailAudioInputMicrosUSDPer1M:       optionalUSDToMicros(spec.AudioInputUSDPer1M),
			RetailCachedInputMicrosUSDPer1M:      optionalUSDToMicros(spec.CachedInputUSDPer1M),
			RetailLongCachedInputMicrosUSDPer1M:  optionalUSDToMicros(spec.LongCachedInputUSDPer1M),
			RetailAudioCachedInputMicrosUSDPer1M: optionalUSDToMicros(spec.AudioCachedInputUSDPer1M),
			RetailCacheStorageMicrosUSDPer1MHour: optionalUSDToMicros(spec.CacheStorageUSDPer1MHour),
			RetailOutputMicrosUSDPer1M:           mustUSDToMicros(spec.OutputUSDPer1M),
			RetailLongOutputMicrosUSDPer1M:       optionalUSDToMicros(spec.LongOutputUSDPer1M),
			RetailImageOutputMicrosUSDPer1M:      optionalUSDToMicros(spec.ImageOutputUSDPer1M),
			MinimumChargeCents:                   spec.MinimumChargeCents,
		}
		if price.MinimumChargeCents == 0 && strings.TrimSpace(spec.InputUSDPer1M) != "0.00" {
			price.MinimumChargeCents = 1
		}
		if price.RetailLongInputMicrosUSDPer1M != nil || price.RetailLongCachedInputMicrosUSDPer1M != nil || price.RetailLongOutputMicrosUSDPer1M != nil {
			threshold := 200_000
			price.PromptTierThresholdTokens = &threshold
		}
		out = append(out, price)
	}
	return out
}

func openAIPrice(model, displayName, inputUSDPer1M, cachedInputUSDPer1M, outputUSDPer1M string) HostedModelPrice {
	return HostedModelPrice{
		Model:                           model,
		DisplayName:                     displayName,
		Provider:                        "openai",
		RetailInputMicrosUSDPer1M:       mustUSDToMicros(inputUSDPer1M),
		RetailCachedInputMicrosUSDPer1M: optionalUSDToMicros(cachedInputUSDPer1M),
		RetailOutputMicrosUSDPer1M:      mustUSDToMicros(outputUSDPer1M),
		MinimumChargeCents:              1,
	}
}

func anthropicPrice(model, displayName, inputUSDPer1M, cacheWrite5mUSDPer1M, cacheWrite1hUSDPer1M, cacheReadUSDPer1M, outputUSDPer1M string) HostedModelPrice {
	return HostedModelPrice{
		Model:                            model,
		DisplayName:                      displayName,
		Provider:                         "anthropic",
		RetailInputMicrosUSDPer1M:        mustUSDToMicros(inputUSDPer1M),
		RetailCachedInputMicrosUSDPer1M:  optionalUSDToMicros(cacheReadUSDPer1M),
		RetailCacheWrite5mMicrosUSDPer1M: optionalUSDToMicros(cacheWrite5mUSDPer1M),
		RetailCacheWrite1hMicrosUSDPer1M: optionalUSDToMicros(cacheWrite1hUSDPer1M),
		RetailOutputMicrosUSDPer1M:       mustUSDToMicros(outputUSDPer1M),
		MinimumChargeCents:               1,
	}
}

func openRouterPrice(model, displayName, inputUSDPer1M, outputUSDPer1M, cacheReadUSDPer1M, cacheWriteUSDPer1M string, minimumChargeCents int64) HostedModelPrice {
	price := HostedModelPrice{
		Model:                            model,
		DisplayName:                      displayName,
		Provider:                         "openrouter",
		RetailInputMicrosUSDPer1M:        mustUSDToMicros(inputUSDPer1M),
		RetailCachedInputMicrosUSDPer1M:  optionalUSDToMicros(cacheReadUSDPer1M),
		RetailCacheWrite5mMicrosUSDPer1M: optionalUSDToMicros(cacheWriteUSDPer1M),
		RetailOutputMicrosUSDPer1M:       mustUSDToMicros(outputUSDPer1M),
		MinimumChargeCents:               minimumChargeCents,
	}
	if price.MinimumChargeCents == 0 && (price.RetailInputMicrosUSDPer1M > 0 || price.RetailOutputMicrosUSDPer1M > 0) {
		price.MinimumChargeCents = 1
	}
	return price
}

func (c *PricingCatalog) HostedModel(model string) (HostedModelPrice, bool) {
	if c == nil {
		return HostedModelPrice{}, false
	}
	spec, ok := c.ByModel[strings.TrimSpace(model)]
	return spec, ok
}

func (c *PricingCatalog) HostedModels() []HostedModelPrice {
	if c == nil {
		return nil
	}
	out := make([]HostedModelPrice, 0, len(c.ByModel))
	for _, spec := range c.ByModel {
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Model < out[j].Model
	})
	return out
}

func (c *PricingCatalog) Estimate(model string, usage types.Usage, pricingCtx PricingContext) (*PricingEstimate, error) {
	if c == nil {
		return nil, fmt.Errorf("pricing catalog is not configured")
	}
	spec, ok := c.ByModel[strings.TrimSpace(model)]
	if !ok {
		return nil, fmt.Errorf("no hosted pricing configured for model %q", model)
	}

	totalTokens := usage.TotalTokens
	if totalTokens == 0 {
		totalTokens = usage.InputTokens + usage.OutputTokens
	}

	selected := selectPricingTiers(spec, usage, pricingCtx)
	cacheReadTokens := 0
	if usage.CacheReadTokens != nil && *usage.CacheReadTokens > 0 && selected.CachedInputMicrosUSDPer1M != nil {
		cacheReadTokens = *usage.CacheReadTokens
	}
	uncachedInputTokens := usage.InputTokens - cacheReadTokens
	if uncachedInputTokens < 0 {
		uncachedInputTokens = 0
	}

	totalMicrosUSD := ceilDiv(int64(uncachedInputTokens)*selected.InputMicrosUSDPer1M, 1_000_000)
	if selected.CachedInputMicrosUSDPer1M != nil && cacheReadTokens > 0 {
		totalMicrosUSD += ceilDiv(int64(cacheReadTokens)*(*selected.CachedInputMicrosUSDPer1M), 1_000_000)
	}
	if usage.CacheWriteTokens != nil && *usage.CacheWriteTokens > 0 && selected.CacheWriteMicrosUSDPer1M != nil {
		totalMicrosUSD += ceilDiv(int64(*usage.CacheWriteTokens)*(*selected.CacheWriteMicrosUSDPer1M), 1_000_000)
	}
	totalMicrosUSD += ceilDiv(int64(usage.OutputTokens)*selected.OutputMicrosUSDPer1M, 1_000_000)

	cents := ceilDiv(totalMicrosUSD, 10_000)
	if cents == 0 && totalTokens > 0 {
		cents = spec.MinimumChargeCents
	}
	if cents < spec.MinimumChargeCents && totalTokens > 0 {
		cents = spec.MinimumChargeCents
	}

	return &PricingEstimate{
		BilledCents: cents,
		Snapshot: PricingSnapshot{
			CatalogVersion:                       c.Version,
			Model:                                spec.Model,
			Provider:                             spec.Provider,
			InputModality:                        selected.InputModality,
			OutputModality:                       selected.OutputModality,
			PromptTier:                           selected.PromptTier,
			AppliedInputMicrosUSDPer1M:           selected.InputMicrosUSDPer1M,
			AppliedCachedInputMicrosUSDPer1M:     selected.CachedInputMicrosUSDPer1M,
			AppliedCacheWriteMicrosUSDPer1M:      selected.CacheWriteMicrosUSDPer1M,
			AppliedOutputMicrosUSDPer1M:          selected.OutputMicrosUSDPer1M,
			RetailInputMicrosUSDPer1M:            spec.RetailInputMicrosUSDPer1M,
			RetailLongInputMicrosUSDPer1M:        spec.RetailLongInputMicrosUSDPer1M,
			RetailAudioInputMicrosUSDPer1M:       spec.RetailAudioInputMicrosUSDPer1M,
			RetailCachedInputMicrosUSDPer1M:      spec.RetailCachedInputMicrosUSDPer1M,
			RetailLongCachedInputMicrosUSDPer1M:  spec.RetailLongCachedInputMicrosUSDPer1M,
			RetailAudioCachedInputMicrosUSDPer1M: spec.RetailAudioCachedInputMicrosUSDPer1M,
			RetailCacheWrite5mMicrosUSDPer1M:     spec.RetailCacheWrite5mMicrosUSDPer1M,
			RetailCacheWrite1hMicrosUSDPer1M:     spec.RetailCacheWrite1hMicrosUSDPer1M,
			RetailCacheStorageMicrosUSDPer1MHour: spec.RetailCacheStorageMicrosUSDPer1MHour,
			RetailOutputMicrosUSDPer1M:           spec.RetailOutputMicrosUSDPer1M,
			RetailLongOutputMicrosUSDPer1M:       spec.RetailLongOutputMicrosUSDPer1M,
			RetailImageOutputMicrosUSDPer1M:      spec.RetailImageOutputMicrosUSDPer1M,
			PromptTierThresholdTokens:            spec.PromptTierThresholdTokens,
			MinimumChargeCents:                   spec.MinimumChargeCents,
			BilledCents:                          cents,
		},
	}, nil
}

type selectedPricingTiers struct {
	InputModality             PricingInputModality
	OutputModality            PricingOutputModality
	PromptTier                PricingPromptTier
	InputMicrosUSDPer1M       int64
	CachedInputMicrosUSDPer1M *int64
	CacheWriteMicrosUSDPer1M  *int64
	OutputMicrosUSDPer1M      int64
}

func selectPricingTiers(spec HostedModelPrice, usage types.Usage, pricingCtx PricingContext) selectedPricingTiers {
	ctx := pricingCtx.normalized()
	selected := selectedPricingTiers{
		InputModality:             ctx.InputModality,
		OutputModality:            ctx.OutputModality,
		PromptTier:                PricingPromptTierStandard,
		InputMicrosUSDPer1M:       spec.RetailInputMicrosUSDPer1M,
		CachedInputMicrosUSDPer1M: spec.RetailCachedInputMicrosUSDPer1M,
		CacheWriteMicrosUSDPer1M:  spec.RetailCacheWrite5mMicrosUSDPer1M,
		OutputMicrosUSDPer1M:      spec.RetailOutputMicrosUSDPer1M,
	}

	if spec.PromptTierThresholdTokens != nil && usage.InputTokens > *spec.PromptTierThresholdTokens {
		selected.PromptTier = PricingPromptTierLong
		if spec.RetailLongInputMicrosUSDPer1M != nil {
			selected.InputMicrosUSDPer1M = *spec.RetailLongInputMicrosUSDPer1M
		}
		if spec.RetailLongCachedInputMicrosUSDPer1M != nil {
			selected.CachedInputMicrosUSDPer1M = spec.RetailLongCachedInputMicrosUSDPer1M
		}
		if spec.RetailLongOutputMicrosUSDPer1M != nil {
			selected.OutputMicrosUSDPer1M = *spec.RetailLongOutputMicrosUSDPer1M
		}
	}

	if ctx.InputModality == PricingInputModalityAudio {
		if spec.RetailAudioInputMicrosUSDPer1M != nil {
			selected.InputMicrosUSDPer1M = *spec.RetailAudioInputMicrosUSDPer1M
		}
		if spec.RetailAudioCachedInputMicrosUSDPer1M != nil {
			selected.CachedInputMicrosUSDPer1M = spec.RetailAudioCachedInputMicrosUSDPer1M
		}
	}

	if ctx.OutputModality == PricingOutputModalityImage && spec.RetailImageOutputMicrosUSDPer1M != nil {
		selected.OutputMicrosUSDPer1M = *spec.RetailImageOutputMicrosUSDPer1M
	}

	return selected
}

func (c PricingContext) normalized() PricingContext {
	out := c
	if out.InputModality == "" {
		out.InputModality = PricingInputModalityStandard
	}
	if out.OutputModality == "" {
		out.OutputModality = PricingOutputModalityText
	}
	return out
}

func PricingContextFromMetadata(metadata map[string]any) PricingContext {
	return PricingContext{
		InputModality:  pricingInputModalityFromAny(metadata["input_modality"]),
		OutputModality: pricingOutputModalityFromAny(metadata["output_modality"]),
	}
}

func pricingInputModalityFromAny(value any) PricingInputModality {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case string(PricingInputModalityAudio):
		return PricingInputModalityAudio
	default:
		return PricingInputModalityStandard
	}
}

func pricingOutputModalityFromAny(value any) PricingOutputModality {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case string(PricingOutputModalityImage):
		return PricingOutputModalityImage
	default:
		return PricingOutputModalityText
	}
}

func (s PricingSnapshot) JSON() string {
	raw, err := json.Marshal(s)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func mustUSDToMicros(raw string) int64 {
	value, err := usdToMicros(raw)
	if err != nil {
		panic(err)
	}
	return value
}

func optionalUSDToMicros(raw string) *int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "-" {
		return nil
	}
	value := mustUSDToMicros(raw)
	return &value
}

func usdToMicros(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty USD rate")
	}
	parts := strings.SplitN(raw, ".", 2)
	wholePart, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse USD whole %q: %w", raw, err)
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 6 {
		return 0, fmt.Errorf("USD rate %q has more than 6 decimal places", raw)
	}
	for len(fraction) < 6 {
		fraction += "0"
	}
	fractionPart := int64(0)
	if fraction != "" {
		fractionPart, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse USD fraction %q: %w", raw, err)
		}
	}
	return (wholePart * 1_000_000) + fractionPart, nil
}

func ceilDiv(numerator, denominator int64) int64 {
	if numerator <= 0 {
		return 0
	}
	return (numerator + denominator - 1) / denominator
}
