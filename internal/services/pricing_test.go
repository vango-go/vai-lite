package services

import "testing"

func TestPricingCatalogEstimateRoundsAndReturnsVersion(t *testing.T) {
	t.Parallel()

	catalog := DefaultPricingCatalog()
	cents, version, err := catalog.Estimate("oai-resp/gpt-5-mini", 1200, 200, 1400)
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if cents != 3 {
		t.Fatalf("Estimate() cents = %d, want 3", cents)
	}
	if version != "beta-2026-03-06" {
		t.Fatalf("Estimate() version = %q", version)
	}
}

func TestPricingCatalogEstimateRejectsUnknownModel(t *testing.T) {
	t.Parallel()

	catalog := DefaultPricingCatalog()
	if _, _, err := catalog.Estimate("unknown/model", 100, 50, 150); err == nil {
		t.Fatal("Estimate() expected error for unknown model")
	}
}
