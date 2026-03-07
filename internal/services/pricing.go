package services

import (
	"fmt"
	"strings"
)

type PriceSpec struct {
	Model                string
	InputCentsPer1K      int64
	OutputCentsPer1K     int64
	MinimumChargeCents   int64
}

type PricingCatalog struct {
	Version string
	ByModel map[string]PriceSpec
}

func DefaultPricingCatalog() *PricingCatalog {
	specs := []PriceSpec{
		{Model: "oai-resp/gpt-5-mini", InputCentsPer1K: 1, OutputCentsPer1K: 4, MinimumChargeCents: 1},
		{Model: "openai/gpt-4.1-mini", InputCentsPer1K: 1, OutputCentsPer1K: 4, MinimumChargeCents: 1},
		{Model: "anthropic/claude-sonnet-4", InputCentsPer1K: 4, OutputCentsPer1K: 20, MinimumChargeCents: 1},
		{Model: "gem-dev/gemini-2.5-flash", InputCentsPer1K: 1, OutputCentsPer1K: 3, MinimumChargeCents: 1},
		{Model: "groq/llama-3.3-70b-versatile", InputCentsPer1K: 1, OutputCentsPer1K: 2, MinimumChargeCents: 1},
	}
	byModel := make(map[string]PriceSpec, len(specs))
	for _, spec := range specs {
		byModel[strings.TrimSpace(spec.Model)] = spec
	}
	return &PricingCatalog{
		Version: "beta-2026-03-06",
		ByModel: byModel,
	}
}

func (c *PricingCatalog) Estimate(model string, inputTokens, outputTokens, totalTokens int) (int64, string, error) {
	if c == nil {
		return 0, "", fmt.Errorf("pricing catalog is not configured")
	}
	spec, ok := c.ByModel[strings.TrimSpace(model)]
	if !ok {
		return 0, c.Version, fmt.Errorf("no hosted pricing configured for model %q", model)
	}
	if totalTokens == 0 {
		totalTokens = inputTokens + outputTokens
	}
	cents := ((int64(inputTokens) * spec.InputCentsPer1K) + 999) / 1000
	cents += ((int64(outputTokens) * spec.OutputCentsPer1K) + 999) / 1000
	if cents == 0 && totalTokens > 0 {
		cents = spec.MinimumChargeCents
	}
	if cents < spec.MinimumChargeCents && totalTokens > 0 {
		cents = spec.MinimumChargeCents
	}
	return cents, c.Version, nil
}
