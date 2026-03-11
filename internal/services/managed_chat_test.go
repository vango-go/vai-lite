package services

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	neon "github.com/vango-go/vango-neon"
)

func TestListManagedConversationSummariesProjectsLatestMetadataWithoutFullHydration(t *testing.T) {
	now := time.Date(2026, time.March, 10, 14, 30, 0, 0, time.UTC)
	completedNew := now
	summaryQueries := 0
	svc := &AppServices{
		DefaultModel: "oai-resp/gpt-5-mini",
		DB: &neon.TestDB{
			QueryRowFunc: func(_ context.Context, sql string, _ ...any) pgx.Row {
				if strings.Contains(sql, "FROM app_orgs") {
					return managedChatTestRow("org_123", "Test Org", true, true, "oai-resp/gpt-5-mini")
				}
				return &neon.ErrRow{Err: fmt.Errorf("unexpected query row: %s", strings.TrimSpace(sql))}
			},
			QueryFunc: func(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
				if !strings.Contains(sql, "COALESCE(first_user.content_json") {
					return nil, fmt.Errorf("unexpected query: %s", strings.TrimSpace(sql))
				}
				summaryQueries++
				completedOld := now.Add(-45 * time.Minute)
				return managedChatTestRows(
					[]any{
						"conv_old",
						now.Add(-90 * time.Minute),
						`{"model":"oai-resp/gpt-5-mini"}`,
						timePointer(now.Add(-90 * time.Minute)),
						`[{"type":"text","text":"Old chat"}]`,
						"",
						`{"observability":{"key_source":"platform_hosted","transport":"sse"}}`,
						timePointer(now.Add(-90 * time.Minute)),
						&completedOld,
					},
					[]any{
						"conv_new",
						now.Add(-2 * time.Hour),
						`{"model":"openrouter/anthropic/claude-haiku-4.5"}`,
						timePointer(now.Add(-2 * time.Hour)),
						`[{"type":"text","text":"Latest question about observability"}]`,
						"openrouter/anthropic/claude-haiku-4.5",
						`{"observability":{"key_source":"customer_byok_vault","transport":"websocket"}}`,
						timePointer(now.Add(-5 * time.Minute)),
						&completedNew,
					},
				), nil
			},
		},
	}

	got, err := svc.ListManagedConversationSummaries(context.Background(), "org_123")
	if err != nil {
		t.Fatalf("ListManagedConversationSummaries() error = %v", err)
	}
	if summaryQueries != 1 {
		t.Fatalf("summary query count = %d, want 1", summaryQueries)
	}
	if len(got) != 2 {
		t.Fatalf("len(summaries) = %d, want 2", len(got))
	}
	if got[0].ID != "conv_new" {
		t.Fatalf("first summary id = %q, want %q", got[0].ID, "conv_new")
	}
	if got[0].Title != "Latest question about observability" {
		t.Fatalf("first summary title = %q", got[0].Title)
	}
	if got[0].Model != "openrouter/anthropic/claude-haiku-4.5" {
		t.Fatalf("first summary model = %q", got[0].Model)
	}
	if got[0].KeySource != KeySourceCustomerBYOKVault {
		t.Fatalf("first summary key source = %q", got[0].KeySource)
	}
	if got[0].PreferredTransport != "websocket" {
		t.Fatalf("first summary transport = %q", got[0].PreferredTransport)
	}
	if !got[0].UpdatedAt.Equal(completedNew) {
		t.Fatalf("first summary updated_at = %s, want %s", got[0].UpdatedAt, completedNew)
	}
	if got[1].ID != "conv_old" {
		t.Fatalf("second summary id = %q, want %q", got[1].ID, "conv_old")
	}
}

func timePointer(t time.Time) *time.Time {
	return &t
}

type managedChatRows struct {
	data [][]any
	idx  int
	err  error
}

func managedChatTestRows(rows ...[]any) *managedChatRows {
	return &managedChatRows{data: rows, idx: -1}
}

func (r *managedChatRows) Close() {}

func (r *managedChatRows) Err() error { return r.err }

func (r *managedChatRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (r *managedChatRows) Conn() *pgx.Conn { return nil }

func (r *managedChatRows) RawValues() [][]byte { return nil }

func (r *managedChatRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (r *managedChatRows) Next() bool {
	r.idx++
	return r.idx >= 0 && r.idx < len(r.data)
}

func (r *managedChatRows) Scan(dest ...any) error {
	if r.idx < 0 || r.idx >= len(r.data) {
		return pgx.ErrNoRows
	}
	row := r.data[r.idx]
	if len(dest) != len(row) {
		r.err = fmt.Errorf("scan dest count %d != row count %d", len(dest), len(row))
		return r.err
	}
	for i := range row {
		if err := managedChatAssignScanValue(dest[i], row[i]); err != nil {
			r.err = err
			return err
		}
	}
	return nil
}

func (r *managedChatRows) Values() ([]any, error) {
	if r.idx < 0 || r.idx >= len(r.data) {
		return nil, pgx.ErrNoRows
	}
	return append([]any(nil), r.data[r.idx]...), nil
}

func managedChatTestRow(values ...any) pgx.Row {
	return &managedChatRow{values: append([]any(nil), values...)}
}

type managedChatRow struct {
	values []any
	err    error
}

func (r *managedChatRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		r.err = fmt.Errorf("scan dest count %d != row count %d", len(dest), len(r.values))
		return r.err
	}
	for i := range r.values {
		if err := managedChatAssignScanValue(dest[i], r.values[i]); err != nil {
			r.err = err
			return err
		}
	}
	return nil
}

func managedChatAssignScanValue(dest any, src any) error {
	dstValue := reflect.ValueOf(dest)
	if !dstValue.IsValid() || dstValue.Kind() != reflect.Pointer || dstValue.IsNil() {
		return fmt.Errorf("scan target must be a non-nil pointer, got %T", dest)
	}
	return managedChatAssignValue(dstValue.Elem(), src)
}

func managedChatAssignValue(dst reflect.Value, src any) error {
	if !dst.CanSet() {
		return fmt.Errorf("scan target %s is not settable", dst.Type())
	}
	if src == nil {
		dst.Set(reflect.Zero(dst.Type()))
		return nil
	}

	srcValue := reflect.ValueOf(src)
	for srcValue.Kind() == reflect.Interface {
		if srcValue.IsNil() {
			dst.Set(reflect.Zero(dst.Type()))
			return nil
		}
		srcValue = srcValue.Elem()
	}

	if dst.Kind() == reflect.Pointer {
		if srcValue.Kind() == reflect.Pointer {
			if srcValue.IsNil() {
				dst.Set(reflect.Zero(dst.Type()))
				return nil
			}
			srcValue = srcValue.Elem()
		}
		elemType := dst.Type().Elem()
		if srcValue.Type().AssignableTo(elemType) {
			next := reflect.New(elemType)
			next.Elem().Set(srcValue)
			dst.Set(next)
			return nil
		}
		if srcValue.Type().ConvertibleTo(elemType) {
			next := reflect.New(elemType)
			next.Elem().Set(srcValue.Convert(elemType))
			dst.Set(next)
			return nil
		}
		return fmt.Errorf("cannot assign %s to %s", srcValue.Type(), dst.Type())
	}

	if srcValue.Kind() == reflect.Pointer {
		if srcValue.IsNil() {
			dst.Set(reflect.Zero(dst.Type()))
			return nil
		}
		srcValue = srcValue.Elem()
	}
	if srcValue.Type().AssignableTo(dst.Type()) {
		dst.Set(srcValue)
		return nil
	}
	if srcValue.Type().ConvertibleTo(dst.Type()) {
		dst.Set(srcValue.Convert(dst.Type()))
		return nil
	}
	return fmt.Errorf("cannot assign %s to %s", srcValue.Type(), dst.Type())
}
