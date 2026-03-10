package blobstore

import (
	"context"
	"os"
	"strings"

	s3store "github.com/vango-go/vango-s3"
)

func Build(ctx context.Context) (s3store.Store, error) {
	accessKey := strings.TrimSpace(os.Getenv("S3_ACCESS_KEY_ID"))
	secretKey := strings.TrimSpace(os.Getenv("S3_SECRET_ACCESS_KEY"))
	bucket := strings.TrimSpace(os.Getenv("S3_BUCKET"))
	intentSecret := strings.TrimSpace(os.Getenv("S3_INTENT_SECRET"))
	if accessKey == "" || secretKey == "" || bucket == "" || intentSecret == "" {
		return nil, nil
	}

	if accountID := strings.TrimSpace(os.Getenv("R2_ACCOUNT_ID")); accountID != "" {
		return s3store.NewR2(ctx, s3store.R2Config{
			AccountID:       accountID,
			AccessKeyID:     accessKey,
			SecretAccessKey: secretKey,
			Bucket:          bucket,
			IntentSecret:    intentSecret,
		})
	}

	return s3store.New(ctx, s3store.Config{
		Endpoint:        strings.TrimSpace(os.Getenv("S3_ENDPOINT")),
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
		Bucket:          bucket,
		Region:          envOr("S3_REGION", "auto"),
		IntentSecret:    intentSecret,
	})
}

func ProviderName() string {
	if strings.TrimSpace(os.Getenv("R2_ACCOUNT_ID")) != "" {
		return "r2"
	}
	return "s3"
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
