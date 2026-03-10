package vai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAssetsService_Lifecycle(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/assets:upload-intent", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s", r.Method)
		}
		if strings.TrimSpace(r.Header.Get(chainSDKIdempotencyHeader)) == "" {
			t.Fatal("missing idempotency header")
		}
		_ = json.NewEncoder(w).Encode(AssetUploadIntentResponse{
			IntentToken: "intent_123",
			UploadURL:   "https://upload.test/intent_123",
			ContentType: "image/png",
			Headers:     map[string]string{"Content-Type": "image/png"},
			ExpiresAt:   time.Now().UTC().Add(time.Minute),
		})
	})
	mux.HandleFunc("/v1/assets:claim", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s", r.Method)
		}
		if strings.TrimSpace(r.Header.Get(chainSDKIdempotencyHeader)) == "" {
			t.Fatal("missing idempotency header")
		}
		_ = json.NewEncoder(w).Encode(AssetRecord{
			ID:        "asset_123",
			MediaType: "image/png",
			SizeBytes: 123,
			CreatedAt: time.Now().UTC(),
		})
	})
	mux.HandleFunc("/v1/assets/asset_123", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method=%s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(AssetRecord{
			ID:        "asset_123",
			MediaType: "image/png",
			SizeBytes: 123,
			CreatedAt: time.Now().UTC(),
		})
	})
	mux.HandleFunc("/v1/assets/asset_123:sign", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s", r.Method)
		}
		if strings.TrimSpace(r.Header.Get(chainSDKIdempotencyHeader)) == "" {
			t.Fatal("missing idempotency header")
		}
		_ = json.NewEncoder(w).Encode(AssetSignResponse{
			AssetID:   "asset_123",
			URL:       "https://download.test/asset_123",
			ExpiresAt: time.Now().UTC().Add(time.Minute),
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := NewClient(
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
	)

	ctx := context.Background()
	intent, err := client.Assets.CreateUploadIntent(ctx, &AssetUploadIntentRequest{
		Filename:    "cat.png",
		ContentType: "image/png",
		SizeBytes:   123,
	})
	if err != nil {
		t.Fatalf("CreateUploadIntent() error = %v", err)
	}
	if intent.IntentToken == "" || intent.UploadURL == "" {
		t.Fatalf("intent=%+v", intent)
	}

	asset, err := client.Assets.Claim(ctx, &AssetClaimRequest{
		IntentToken: "intent_123",
		Filename:    "cat.png",
		ContentType: "image/png",
	})
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if asset.ID != "asset_123" {
		t.Fatalf("asset=%+v", asset)
	}

	readAsset, err := client.Assets.Get(ctx, "asset_123")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if readAsset.ID != "asset_123" {
		t.Fatalf("read asset=%+v", readAsset)
	}

	signed, err := client.Assets.Sign(ctx, "asset_123")
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	if signed.AssetID != "asset_123" || signed.URL == "" {
		t.Fatalf("signed=%+v", signed)
	}
}
