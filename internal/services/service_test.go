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
	"github.com/vango-go/vai-lite/pkg/core/types"
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

func TestResolveExecutionHeaders_GatewayAPIKeyPrefersExternalBYOK(t *testing.T) {
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

	out, source, err := svc.ResolveExecutionHeaders(context.Background(), "org_123", headers, "", AccessCredentialGatewayAPIKey)
	if err != nil {
		t.Fatalf("ResolveExecutionHeaders() error = %v", err)
	}
	if source != KeySourceCustomerBYOKExternal {
		t.Fatalf("ResolveExecutionHeaders() source = %q, want %q", source, KeySourceCustomerBYOKExternal)
	}
	if got := out.Get("X-Provider-Key-OpenAI"); got != "sk-browser" {
		t.Fatalf("ResolveExecutionHeaders() header = %q", got)
	}
}

func TestResolveExecutionHeaders_GatewayAPIKeyUsesPlatformHostedWithoutProviderHeaders(t *testing.T) {
	t.Parallel()

	svc := &AppServices{
		DB: &neon.TestDB{
			QueryRowFunc: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return orgRow("org_123", false, true)
			},
		},
	}

	out, source, err := svc.ResolveExecutionHeaders(context.Background(), "org_123", nil, "", AccessCredentialGatewayAPIKey)
	if err != nil {
		t.Fatalf("ResolveExecutionHeaders() error = %v", err)
	}
	if source != KeySourcePlatformHosted {
		t.Fatalf("ResolveExecutionHeaders() source = %q, want %q", source, KeySourcePlatformHosted)
	}
	if out == nil {
		t.Fatal("ResolveExecutionHeaders() returned nil headers")
	}
}

func TestResolveExecutionHeaders_UsesWorkspaceVaultKeysForSessionMode(t *testing.T) {
	t.Parallel()

	svc := &AppServices{
		DB: &neon.TestDB{
			QueryRowFunc: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return orgRow("org_123", true, true)
			},
			QueryFunc: func(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
				return neon.NewRows([]string{"provider", "object_name"}).AddRow("openai", "vault/openai").Build(), nil
			},
		},
		vaultRead: func(_ context.Context, name string) (string, error) {
			if name != "vault/openai" {
				t.Fatalf("vaultRead name = %q", name)
			}
			return "sk-vault", nil
		},
	}

	out, source, err := svc.ResolveExecutionHeaders(context.Background(), "org_123", nil, KeySourceCustomerBYOKVault, AccessCredentialSessionAuth)
	if err != nil {
		t.Fatalf("ResolveExecutionHeaders() error = %v", err)
	}
	if source != KeySourceCustomerBYOKVault {
		t.Fatalf("ResolveExecutionHeaders() source = %q, want %q", source, KeySourceCustomerBYOKVault)
	}
	if got := out.Get("X-Provider-Key-OpenAI"); got != "sk-vault" {
		t.Fatalf("vault header = %q", got)
	}
}

func TestReservePlatformHostedUsageRejectsDepletedBalance(t *testing.T) {
	t.Parallel()

	svc := &AppServices{
		DB: &neon.TestDB{
			QueryRowFunc: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return neon.NewRow(int64(0))
			},
		},
		Pricing: DefaultPricingCatalog(),
	}

	err := svc.ReservePlatformHostedUsage(context.Background(), "org_123", "oai-resp/gpt-5-mini")
	if err == nil || !strings.Contains(err.Error(), "depleted") {
		t.Fatalf("ReservePlatformHostedUsage() error = %v, want depleted balance", err)
	}
}

func TestRecordUsageBrowserBYOKDoesNotDebitWallet(t *testing.T) {
	t.Parallel()

	tx := &txRecorder{}
	svc := &AppServices{
		DB:      newTxDB(tx),
		Pricing: DefaultPricingCatalog(),
	}

	err := svc.RecordUsage(context.Background(), "org_123", "conv_123", "msg_123", "chat_stream", "unknown/model", KeySourceCustomerBYOKBrowser, AccessCredentialSessionAuth, types.Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150}, map[string]any{
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
	if got := tx.execCalls[0].args[9]; got != false {
		t.Fatalf("billable arg = %#v, want false", got)
	}
	if got := tx.execCalls[0].args[6]; got != string(AccessCredentialSessionAuth) {
		t.Fatalf("access credential arg = %#v", got)
	}
}

func TestRecordUsagePlatformHostedDebitsWallet(t *testing.T) {
	t.Parallel()

	tx := &txRecorder{}
	svc := &AppServices{
		DB:      newTxDB(tx),
		Pricing: DefaultPricingCatalog(),
	}

	estimate, err := svc.Pricing.Estimate("oai-resp/gpt-5-mini", types.Usage{InputTokens: 1200, OutputTokens: 200, TotalTokens: 1400}, PricingContext{})
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}

	err = svc.RecordUsage(context.Background(), "org_123", "conv_123", "msg_123", "chat_stream", "oai-resp/gpt-5-mini", KeySourcePlatformHosted, AccessCredentialGatewayAPIKey, types.Usage{InputTokens: 1200, OutputTokens: 200, TotalTokens: 1400}, map[string]any{
		"via": "test",
	})
	if err != nil {
		t.Fatalf("RecordUsage() error = %v", err)
	}
	if len(tx.execCalls) != 2 {
		t.Fatalf("len(execCalls) = %d, want 2", len(tx.execCalls))
	}
	if got := tx.execCalls[0].args[9]; got != true {
		t.Fatalf("billable arg = %#v, want true", got)
	}
	if got := tx.execCalls[0].args[6]; got != string(AccessCredentialGatewayAPIKey) {
		t.Fatalf("access credential arg = %#v", got)
	}
	if !strings.Contains(tx.execCalls[1].sql, "INSERT INTO wallet_ledger") {
		t.Fatalf("second exec sql = %q", tx.execCalls[1].sql)
	}
	if got := tx.execCalls[1].args[2]; got != -estimate.BilledCents {
		t.Fatalf("wallet debit = %#v, want %d", got, -estimate.BilledCents)
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
	}

	_, err := svc.CreateTopupCheckout(context.Background(), UserIdentity{OrgID: "org_123", UserID: "user_123"}, 99)
	if err == nil || !strings.Contains(err.Error(), "at least") {
		t.Fatalf("CreateTopupCheckout() error = %v", err)
	}
}

func TestRecordUsagePlatformHostedRejectsUnknownModel(t *testing.T) {
	t.Parallel()

	svc := &AppServices{
		DB:      &neon.TestDB{},
		Pricing: DefaultPricingCatalog(),
	}

	err := svc.RecordUsage(context.Background(), "org_123", "", "", "chat_stream", "unknown/model", KeySourcePlatformHosted, AccessCredentialSessionAuth, types.Usage{InputTokens: 10, OutputTokens: 10, TotalTokens: 20}, nil)
	if err == nil {
		t.Fatal("RecordUsage() expected error for unknown hosted model")
	}
}

func TestResolveExecutionHeaders_BrowserBYOKHonorsWorkspaceSetting(t *testing.T) {
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

	_, _, err := svc.ResolveExecutionHeaders(context.Background(), "org_123", headers, KeySourceCustomerBYOKBrowser, AccessCredentialSessionAuth)
	if err == nil {
		t.Fatal("ResolveExecutionHeaders() expected browser BYOK policy error")
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

func TestValidateTopupAmount(t *testing.T) {
	t.Parallel()

	if err := validateTopupAmount(MinTopupAmountCents); err != nil {
		t.Fatalf("validateTopupAmount(min) error = %v", err)
	}
	if err := validateTopupAmount(MinTopupAmountCents - 1); err == nil {
		t.Fatal("validateTopupAmount() expected low amount error")
	}
	if err := validateTopupAmount(MaxTopupAmountCents + 1); err == nil {
		t.Fatal("validateTopupAmount() expected high amount error")
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

func TestResolveExecutionHeadersPropagatesOrgLookupError(t *testing.T) {
	t.Parallel()

	svc := &AppServices{
		DB: &neon.TestDB{
			QueryRowFunc: func(_ context.Context, _ string, _ ...any) pgx.Row {
				return &neon.ErrRow{Err: errors.New("boom")}
			},
		},
	}

	_, _, err := svc.ResolveExecutionHeaders(context.Background(), "org_123", nil, "", AccessCredentialGatewayAPIKey)
	if err == nil {
		t.Fatal("ResolveExecutionHeaders() expected error")
	}
}
