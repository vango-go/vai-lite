package services

import (
	"testing"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

func TestPricingCatalogEstimateRoundsAndReturnsVersion(t *testing.T) {
	t.Parallel()

	catalog := DefaultPricingCatalog()
	estimate, err := catalog.Estimate("oai-resp/gpt-5-mini", types.Usage{InputTokens: 1200, OutputTokens: 200, TotalTokens: 1400}, PricingContext{})
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if estimate.BilledCents != 1 {
		t.Fatalf("Estimate() billed = %d, want 1", estimate.BilledCents)
	}
	if estimate.Snapshot.CatalogVersion != "beta-2026-03-06-hosted-retail-catalog" {
		t.Fatalf("Estimate() version = %q", estimate.Snapshot.CatalogVersion)
	}
	if estimate.Snapshot.RetailInputMicrosUSDPer1M != mustUSDToMicros("0.25") || estimate.Snapshot.RetailOutputMicrosUSDPer1M != mustUSDToMicros("2.00") {
		t.Fatalf("Estimate() snapshot rates = %#v", estimate.Snapshot)
	}
	if estimate.Snapshot.AppliedInputMicrosUSDPer1M != mustUSDToMicros("0.25") || estimate.Snapshot.AppliedOutputMicrosUSDPer1M != mustUSDToMicros("2.00") {
		t.Fatalf("Estimate() applied rates = %#v", estimate.Snapshot)
	}
}

func TestPricingCatalogEstimateRejectsUnknownModel(t *testing.T) {
	t.Parallel()

	catalog := DefaultPricingCatalog()
	if _, err := catalog.Estimate("unknown/model", types.Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150}, PricingContext{}); err == nil {
		t.Fatal("Estimate() expected error for unknown model")
	}
}

func TestPricingCatalogEstimateUsesCachedInputRate(t *testing.T) {
	t.Parallel()

	catalog := DefaultPricingCatalog()
	cacheRead := 1000
	estimate, err := catalog.Estimate("oai-resp/gpt-5", types.Usage{
		InputTokens:     2000,
		OutputTokens:    1000,
		TotalTokens:     3000,
		CacheReadTokens: &cacheRead,
	}, PricingContext{})
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if estimate.BilledCents != 2 {
		t.Fatalf("Estimate() billed = %d, want 2", estimate.BilledCents)
	}
	if estimate.Snapshot.RetailCachedInputMicrosUSDPer1M == nil || *estimate.Snapshot.RetailCachedInputMicrosUSDPer1M != mustUSDToMicros("0.125") {
		t.Fatalf("Estimate() cached snapshot = %#v", estimate.Snapshot)
	}
}

func TestPricingCatalogEstimateUsesAnthropicCacheWriteRate(t *testing.T) {
	t.Parallel()

	catalog := DefaultPricingCatalog()
	cacheRead := 1000
	cacheWrite := 2000
	estimate, err := catalog.Estimate("anthropic/claude-sonnet-4.6", types.Usage{
		InputTokens:      3000,
		OutputTokens:     1000,
		TotalTokens:      4000,
		CacheReadTokens:  &cacheRead,
		CacheWriteTokens: &cacheWrite,
	}, PricingContext{})
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if estimate.BilledCents != 3 {
		t.Fatalf("Estimate() billed = %d, want 3", estimate.BilledCents)
	}
	if estimate.Snapshot.RetailCacheWrite5mMicrosUSDPer1M == nil || *estimate.Snapshot.RetailCacheWrite5mMicrosUSDPer1M != mustUSDToMicros("3.75") {
		t.Fatalf("Estimate() cache write snapshot = %#v", estimate.Snapshot)
	}
	if estimate.Snapshot.RetailCacheWrite1hMicrosUSDPer1M == nil || *estimate.Snapshot.RetailCacheWrite1hMicrosUSDPer1M != mustUSDToMicros("6.00") {
		t.Fatalf("Estimate() 1h cache write snapshot = %#v", estimate.Snapshot)
	}
}

func TestPricingCatalogEstimateUsesGeminiLongPromptRates(t *testing.T) {
	t.Parallel()

	catalog := DefaultPricingCatalog()
	estimate, err := catalog.Estimate("gem-dev/gemini-2.5-pro", types.Usage{
		InputTokens:  250001,
		OutputTokens: 1000,
		TotalTokens:  251001,
	}, PricingContext{})
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if estimate.Snapshot.PromptTier != PricingPromptTierLong {
		t.Fatalf("Estimate() prompt tier = %q, want long", estimate.Snapshot.PromptTier)
	}
	if estimate.Snapshot.AppliedInputMicrosUSDPer1M != mustUSDToMicros("2.50") {
		t.Fatalf("Estimate() applied input = %d, want %d", estimate.Snapshot.AppliedInputMicrosUSDPer1M, mustUSDToMicros("2.50"))
	}
	if estimate.Snapshot.AppliedOutputMicrosUSDPer1M != mustUSDToMicros("15.00") {
		t.Fatalf("Estimate() applied output = %d, want %d", estimate.Snapshot.AppliedOutputMicrosUSDPer1M, mustUSDToMicros("15.00"))
	}
}

func TestPricingCatalogEstimateUsesGeminiAudioRates(t *testing.T) {
	t.Parallel()

	catalog := DefaultPricingCatalog()
	cacheRead := 1000
	estimate, err := catalog.Estimate("gem-dev/gemini-2.5-flash", types.Usage{
		InputTokens:     4000,
		OutputTokens:    1000,
		TotalTokens:     5000,
		CacheReadTokens: &cacheRead,
	}, PricingContext{
		InputModality: PricingInputModalityAudio,
	})
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if estimate.Snapshot.InputModality != PricingInputModalityAudio {
		t.Fatalf("Estimate() input modality = %q, want audio", estimate.Snapshot.InputModality)
	}
	if estimate.Snapshot.AppliedInputMicrosUSDPer1M != mustUSDToMicros("1.00") {
		t.Fatalf("Estimate() applied audio input = %d, want %d", estimate.Snapshot.AppliedInputMicrosUSDPer1M, mustUSDToMicros("1.00"))
	}
	if estimate.Snapshot.AppliedCachedInputMicrosUSDPer1M == nil || *estimate.Snapshot.AppliedCachedInputMicrosUSDPer1M != mustUSDToMicros("0.10") {
		t.Fatalf("Estimate() applied audio cached input = %#v", estimate.Snapshot)
	}
}

func TestPricingCatalogEstimateUsesGeminiImageOutputRate(t *testing.T) {
	t.Parallel()

	catalog := DefaultPricingCatalog()
	estimate, err := catalog.Estimate("gem-dev/gemini-3.1-flash-image-preview", types.Usage{
		InputTokens:  4000,
		OutputTokens: 1000,
		TotalTokens:  5000,
	}, PricingContext{
		OutputModality: PricingOutputModalityImage,
	})
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if estimate.Snapshot.OutputModality != PricingOutputModalityImage {
		t.Fatalf("Estimate() output modality = %q, want image", estimate.Snapshot.OutputModality)
	}
	if estimate.Snapshot.AppliedOutputMicrosUSDPer1M != mustUSDToMicros("60.00") {
		t.Fatalf("Estimate() applied image output = %d, want %d", estimate.Snapshot.AppliedOutputMicrosUSDPer1M, mustUSDToMicros("60.00"))
	}
}

func TestPricingCatalogEstimateUsesOpenRouterCacheRates(t *testing.T) {
	t.Parallel()

	catalog := DefaultPricingCatalog()
	cacheRead := 1000
	cacheWrite := 2000
	estimate, err := catalog.Estimate("openrouter/anthropic/claude-3-haiku", types.Usage{
		InputTokens:      5000,
		OutputTokens:     1000,
		TotalTokens:      6000,
		CacheReadTokens:  &cacheRead,
		CacheWriteTokens: &cacheWrite,
	}, PricingContext{})
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if estimate.Snapshot.Provider != "openrouter" {
		t.Fatalf("Estimate() provider = %q, want openrouter", estimate.Snapshot.Provider)
	}
	if estimate.Snapshot.RetailCachedInputMicrosUSDPer1M == nil || *estimate.Snapshot.RetailCachedInputMicrosUSDPer1M != mustUSDToMicros("0.03") {
		t.Fatalf("Estimate() cached rate snapshot = %#v", estimate.Snapshot)
	}
	if estimate.Snapshot.RetailCacheWrite5mMicrosUSDPer1M == nil || *estimate.Snapshot.RetailCacheWrite5mMicrosUSDPer1M != mustUSDToMicros("0.30") {
		t.Fatalf("Estimate() cache write snapshot = %#v", estimate.Snapshot)
	}
	if estimate.BilledCents != 1 {
		t.Fatalf("Estimate() billed = %d, want 1", estimate.BilledCents)
	}
}

func TestPricingCatalogEstimateAllowsFreeOpenRouterModel(t *testing.T) {
	t.Parallel()

	catalog := DefaultPricingCatalog()
	estimate, err := catalog.Estimate("openrouter/openrouter/free", types.Usage{
		InputTokens:  1000,
		OutputTokens: 1000,
		TotalTokens:  2000,
	}, PricingContext{})
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if estimate.BilledCents != 0 {
		t.Fatalf("Estimate() billed = %d, want 0", estimate.BilledCents)
	}
	if estimate.Snapshot.MinimumChargeCents != 0 {
		t.Fatalf("Estimate() minimum charge = %d, want 0", estimate.Snapshot.MinimumChargeCents)
	}
}
