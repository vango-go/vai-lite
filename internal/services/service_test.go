package services

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stripe/stripe-go/v84"
	neon "github.com/vango-go/vango-neon"
)

type txRecorder struct {
	execCalls []execCall
	execTags  []pgconn.CommandTag
	execErr   error
}

type execCall struct {
	sql  string
	args []any
}

func (t *txRecorder) Begin(_ context.Context) (pgx.Tx, error) { panic("unexpected Begin") }

func (t *txRecorder) Commit(_ context.Context) error { return nil }

func (t *txRecorder) Rollback(_ context.Context) error { return nil }

func (t *txRecorder) CopyFrom(_ context.Context, _ pgx.Identifier, _ []string, _ pgx.CopyFromSource) (int64, error) {
	panic("unexpected CopyFrom")
}

func (t *txRecorder) SendBatch(_ context.Context, _ *pgx.Batch) pgx.BatchResults {
	panic("unexpected SendBatch")
}

func (t *txRecorder) LargeObjects() pgx.LargeObjects { panic("unexpected LargeObjects") }

func (t *txRecorder) Prepare(_ context.Context, _ string, _ string) (*pgconn.StatementDescription, error) {
	panic("unexpected Prepare")
}

func (t *txRecorder) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.execCalls = append(t.execCalls, execCall{sql: sql, args: append([]any(nil), args...)})
	if t.execErr != nil {
		return pgconn.CommandTag{}, t.execErr
	}
	if len(t.execTags) > 0 {
		tag := t.execTags[0]
		t.execTags = t.execTags[1:]
		return tag, nil
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (t *txRecorder) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	panic("unexpected Query")
}

func (t *txRecorder) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	panic("unexpected QueryRow")
}

func (t *txRecorder) Conn() *pgx.Conn { return nil }

func newTxDB(tx pgx.Tx) *neon.TestDB {
	return &neon.TestDB{
		BeginTxFunc: func(_ context.Context, _ pgx.TxOptions) (pgx.Tx, error) {
			return tx, nil
		},
	}
}

func orgRow(orgID string, allowBYOK, hostedUsage bool) pgx.Row {
	return neon.NewRow(orgID, "Test Org", allowBYOK, hostedUsage, "oai-resp/gpt-5-mini")
}

func TestResolveHostedHeaders_PrefersExternalBYOKWhenOverrideAllowed(t *testing.T) {
	t.Parallel()

	svc := &AppServices{
		DB: &neon.TestDB{
			QueryRowFunc: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return orgRow("org_123", true, true)
			},
		},
	}

	headers := http.Header{}
	headers.Set("X-Provider-Key-OpenAI", "sk-browser")

	out, source, err := svc.ResolveHostedHeaders(context.Background(), "org_123", headers, false)
	if err != nil {
		t.Fatalf("ResolveHostedHeaders() error = %v", err)
	}
	if source != KeySourceExternalBYOK {
		t.Fatalf("ResolveHostedHeaders() source = %q, want %q", source, KeySourceExternalBYOK)
	}
	if got := out.Get("X-Provider-Key-OpenAI"); got != "sk-browser" {
		t.Fatalf("ResolveHostedHeaders() header = %q", got)
	}
}

func TestResolveHostedHeaders_ReturnsEmptyWithoutVaultOrBYOK(t *testing.T) {
	t.Parallel()

	svc := &AppServices{
		DB: &neon.TestDB{
			QueryRowFunc: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return orgRow("org_123", false, true)
			},
		},
	}

	out, source, err := svc.ResolveHostedHeaders(context.Background(), "org_123", nil, false)
	if err != nil {
		t.Fatalf("ResolveHostedHeaders() error = %v", err)
	}
	if source != "" {
		t.Fatalf("ResolveHostedHeaders() source = %q, want empty", source)
	}
	if out == nil {
		t.Fatal("ResolveHostedHeaders() returned nil headers")
	}
}

func TestReserveHostedUsageRejectsDepletedBalance(t *testing.T) {
	t.Parallel()

	svc := &AppServices{
		DB: &neon.TestDB{
			QueryRowFunc: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return neon.NewRow(int64(0))
			},
		},
	}

	err := svc.ReserveHostedUsage(context.Background(), "org_123")
	if err == nil || !strings.Contains(err.Error(), "depleted") {
		t.Fatalf("ReserveHostedUsage() error = %v, want depleted balance", err)
	}
}

func TestRecordUsageBrowserBYOKDoesNotDebitWallet(t *testing.T) {
	t.Parallel()

	tx := &txRecorder{}
	svc := &AppServices{
		DB:      newTxDB(tx),
		Pricing: DefaultPricingCatalog(),
	}

	err := svc.RecordUsage(context.Background(), "org_123", "conv_123", "msg_123", "chat_stream", "unknown/model", KeySourceBrowserBYOK, 100, 50, 150, map[string]any{
		"via": "test",
	})
	if err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}
	if len(tx.execCalls) != 1 {
		t.Fatalf("len(execCalls) = %d, want 1", len(tx.execCalls))
	}
	if !strings.Contains(tx.execCalls[0].sql, "INSERT INTO usage_events") {
		t.Fatalf("first exec sql = %q", tx.execCalls[0].sql)
	}
	if got := tx.execCalls[0].args[8]; got != false {
		t.Fatalf("billable arg = %#v, want false", got)
	}
}

func TestRecordUsageHostedDebitsWallet(t *testing.T) {
	t.Parallel()

	tx := &txRecorder{}
	svc := &AppServices{
		DB:      newTxDB(tx),
		Pricing: DefaultPricingCatalog(),
	}

	expectedCost, _, err := svc.Pricing.Estimate("oai-resp/gpt-5-mini", 1200, 200, 1400)
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}

	err = svc.RecordUsage(context.Background(), "org_123", "conv_123", "msg_123", "chat_stream", "oai-resp/gpt-5-mini", KeySourceHosted, 1200, 200, 1400, map[string]any{
		"via": "test",
	})
	if err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}
	if len(tx.execCalls) != 2 {
		t.Fatalf("len(execCalls) = %d, want 2", len(tx.execCalls))
	}
	if got := tx.execCalls[0].args[8]; got != true {
		t.Fatalf("billable arg = %#v, want true", got)
	}
	if !strings.Contains(tx.execCalls[1].sql, "INSERT INTO wallet_ledger") {
		t.Fatalf("second exec sql = %q", tx.execCalls[1].sql)
	}
	if got := tx.execCalls[1].args[2]; got != -expectedCost {
		t.Fatalf("wallet debit = %#v, want %d", got, -expectedCost)
	}
}

func TestApplyStripeCheckoutCompletedIdempotent(t *testing.T) {
	t.Parallel()

	tx := &txRecorder{
		execTags: []pgconn.CommandTag{
			pgconn.NewCommandTag("INSERT 0 0"),
		},
	}
	svc := &AppServices{DB: newTxDB(tx)}

	err := svc.ApplyStripeCheckoutCompleted(context.Background(), "evt_123", "cs_123", "topup_123", "org_123", 2500)
	if err != nil {
		t.Fatalf("ApplyStripeCheckoutCompleted() error = %v", err)
	}
	if len(tx.execCalls) != 1 {
		t.Fatalf("len(execCalls) = %d, want 1", len(tx.execCalls))
	}
	if !strings.Contains(tx.execCalls[0].sql, "processed_stripe_events") {
		t.Fatalf("first exec sql = %q", tx.execCalls[0].sql)
	}
}

func TestCreateTopupCheckoutRejectsInvalidAmount(t *testing.T) {
	t.Parallel()

	svc := &AppServices{
		StripeClient: &stripe.Client{},
		TopupOptions: []int64{1000, 2500},
	}

	_, err := svc.CreateTopupCheckout(context.Background(), UserIdentity{OrgID: "org_123", UserID: "user_123"}, 9999)
	if err == nil || !strings.Contains(err.Error(), "invalid top-up amount") {
		t.Fatalf("CreateTopupCheckout() error = %v", err)
	}
}

func TestRecordUsageHostedRejectsUnknownModel(t *testing.T) {
	t.Parallel()

	svc := &AppServices{
		DB:      &neon.TestDB{},
		Pricing: DefaultPricingCatalog(),
	}

	err := svc.RecordUsage(context.Background(), "org_123", "", "", "chat_stream", "unknown/model", KeySourceHosted, 10, 10, 20, nil)
	if err == nil {
		t.Fatal("RecordUsage() expected error for unknown hosted model")
	}
}

func TestResolveHostedHeadersPassesThroughBYOKWhenHostedDisabled(t *testing.T) {
	t.Parallel()

	svc := &AppServices{
		DB: &neon.TestDB{
			QueryRowFunc: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return orgRow("org_123", false, false)
			},
		},
	}

	headers := http.Header{}
	headers.Set("X-Provider-Key-Anthropic", "sk-ant")

	out, source, err := svc.ResolveHostedHeaders(context.Background(), "org_123", headers, false)
	if err != nil {
		t.Fatalf("ResolveHostedHeaders() error = %v", err)
	}
	if source != KeySourceExternalBYOK {
		t.Fatalf("ResolveHostedHeaders() source = %q", source)
	}
	if got := out.Get("X-Provider-Key-Anthropic"); got != "sk-ant" {
		t.Fatalf("ResolveHostedHeaders() anthropic header = %q", got)
	}
}

func TestSanitizeIDReturnsFallbackForEmpty(t *testing.T) {
	t.Parallel()

	if got := sanitizeID("   "); got != "unknown" {
		t.Fatalf("sanitizeID() = %q, want unknown", got)
	}
}

func TestHashTokenChangesWithInput(t *testing.T) {
	t.Parallel()

	if hashToken("a") == hashToken("b") {
		t.Fatal("hashToken() should vary by input")
	}
}

func TestProviderHeaderUnknownProviderEmpty(t *testing.T) {
	t.Parallel()

	if got := providerHeader("not-real"); got != "" {
		t.Fatalf("providerHeader() = %q, want empty", got)
	}
}

func TestMessageTitleShortensLongBodies(t *testing.T) {
	t.Parallel()

	title := messageTitle(strings.Repeat("x", 100))
	if len(title) > 72 {
		t.Fatalf("messageTitle() len = %d, want <= 72", len(title))
	}
}

func TestContainsAmount(t *testing.T) {
	t.Parallel()

	if !containsAmount([]int64{1000, 2500}, 2500) {
		t.Fatal("containsAmount() = false, want true")
	}
	if containsAmount([]int64{1000, 2500}, 5000) {
		t.Fatal("containsAmount() = true, want false")
	}
}

func TestNewDefaultsTopupOptionsAndModel(t *testing.T) {
	t.Parallel()

	svc := New(&neon.TestDB{}, "http://localhost:8080", "", "", "", nil, nil, nil, nil)
	if svc.DefaultModel == "" {
		t.Fatal("DefaultModel should not be empty")
	}
	if len(svc.TopupOptions) == 0 {
		t.Fatal("TopupOptions should default")
	}
}

func TestResolveHostedHeadersPropagatesOrgLookupError(t *testing.T) {
	t.Parallel()

	svc := &AppServices{
		DB: &neon.TestDB{
			QueryRowFunc: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return &neon.ErrRow{Err: errors.New("boom")}
			},
		},
	}

	_, _, err := svc.ResolveHostedHeaders(context.Background(), "org_123", nil, false)
	if err == nil {
		t.Fatal("ResolveHostedHeaders() expected error")
	}
}
