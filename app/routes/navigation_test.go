package routes_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/vango-go/vai-lite/app/routes/chat/id_"
	"github.com/vango-go/vai-lite/app/routes/demo"
	"github.com/vango-go/vai-lite/internal/appruntime"
	"github.com/vango-go/vai-lite/internal/services"
	"github.com/vango-go/vango"
	neon "github.com/vango-go/vango-neon"
	workos "github.com/vango-go/vango-workos"
	"github.com/vango-go/vango/pkg/auth"
	"github.com/vango-go/vango/pkg/server"
	"github.com/vango-go/vango/pkg/shell"
	"github.com/vango-go/vango/pkg/surface"
	corevango "github.com/vango-go/vango/pkg/vango"
)

func TestDemoIndexPageIsThinAndDoesNotNavigateInRouteHandler(t *testing.T) {
	now := time.Date(2026, time.March, 10, 15, 0, 0, 0, time.UTC)
	installRouteTestRuntime(&services.AppServices{
		DefaultModel: "oai-resp/gpt-5-mini",
		DB: &neon.TestDB{
			QueryRowFunc: func(_ context.Context, sql string, _ ...any) pgx.Row {
				if strings.Contains(sql, "FROM app_orgs") {
					return routeTestRow("org_123", "Test Org", true, true, "oai-resp/gpt-5-mini")
				}
				return &neon.ErrRow{Err: fmt.Errorf("unexpected query row: %s", strings.TrimSpace(sql))}
			},
			QueryFunc: func(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
				if !strings.Contains(sql, "COALESCE(first_user.content_json") {
					return nil, fmt.Errorf("unexpected query: %s", strings.TrimSpace(sql))
				}
				completedAt := now
				return routeTestRows(
					[]any{
						"conv_latest",
						now.Add(-time.Hour),
						[]byte(`{"model":"oai-resp/gpt-5-mini"}`),
						timePtr(now.Add(-time.Hour)),
						[]byte(`[{"type":"text","text":"Latest chat"}]`),
						"oai-resp/gpt-5-mini",
						[]byte(`{"observability":{"key_source":"platform_hosted","transport":"sse"}}`),
						timePtr(now.Add(-5 * time.Minute)),
						&completedAt,
					},
				), nil
			},
		},
	})

	ctx := newRouteTestCtx("/demo")
	node := demo.IndexPage(ctx)
	if node == nil {
		t.Fatal("expected page node")
	}
	if ctx.navigatePath != "" {
		t.Fatalf("route handler queued navigate path %q, want none", ctx.navigatePath)
	}
}

func TestDemoIndexPageSSRRedirectsToLatestConversation(t *testing.T) {
	now := time.Date(2026, time.March, 10, 15, 0, 0, 0, time.UTC)
	installRouteTestRuntime(&services.AppServices{
		DefaultModel: "oai-resp/gpt-5-mini",
		DB: &neon.TestDB{
			QueryRowFunc: func(_ context.Context, sql string, _ ...any) pgx.Row {
				if strings.Contains(sql, "FROM app_orgs") {
					return routeTestRow("org_123", "Test Org", true, true, "oai-resp/gpt-5-mini")
				}
				return &neon.ErrRow{Err: fmt.Errorf("unexpected query row: %s", strings.TrimSpace(sql))}
			},
			QueryFunc: func(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
				if !strings.Contains(sql, "COALESCE(first_user.content_json") {
					return nil, fmt.Errorf("unexpected query: %s", strings.TrimSpace(sql))
				}
				completedAt := now
				return routeTestRows(
					[]any{
						"conv_latest",
						now.Add(-time.Hour),
						[]byte(`{"model":"oai-resp/gpt-5-mini"}`),
						timePtr(now.Add(-time.Hour)),
						[]byte(`[{"type":"text","text":"Latest chat"}]`),
						"oai-resp/gpt-5-mini",
						[]byte(`{"observability":{"key_source":"platform_hosted","transport":"sse"}}`),
						timePtr(now.Add(-5 * time.Minute)),
						&completedAt,
					},
				), nil
			},
		},
	})

	app := newRouteTestApp()
	app.Page("/demo", demo.IndexPage)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/demo", nil)
	req = req.WithContext(vango.WithUser(req.Context(), routeTestIdentity()))
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusSeeOther)
	}
	if got := rr.Header().Get("Location"); got != "/demo/conv_latest" {
		t.Fatalf("Location = %q, want %q", got, "/demo/conv_latest")
	}
}

func TestDemoIndexPageSSRCreatesDraftConversationWhenNoHistoryExists(t *testing.T) {
	installRouteTestRuntime(&services.AppServices{
		DefaultModel: "oai-resp/gpt-5-mini",
		DB: &neon.TestDB{
			QueryRowFunc: func(_ context.Context, sql string, _ ...any) pgx.Row {
				if strings.Contains(sql, "FROM app_orgs") {
					return routeTestRow("org_123", "Test Org", true, true, "oai-resp/gpt-5-mini")
				}
				return &neon.ErrRow{Err: fmt.Errorf("unexpected query row: %s", strings.TrimSpace(sql))}
			},
			QueryFunc: func(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
				if !strings.Contains(sql, "COALESCE(first_user.content_json") {
					return nil, fmt.Errorf("unexpected query: %s", strings.TrimSpace(sql))
				}
				return routeTestRows(), nil
			},
		},
	})

	app := newRouteTestApp()
	app.Page("/demo", demo.IndexPage)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/demo", nil)
	req = req.WithContext(vango.WithUser(req.Context(), routeTestIdentity()))
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusSeeOther)
	}
	if got := rr.Header().Get("Location"); !strings.HasPrefix(got, "/demo/chat_") {
		t.Fatalf("Location = %q, want /demo/chat_*", got)
	}
}

func TestChatRouteIsThinAndDoesNotNavigateInRouteHandler(t *testing.T) {
	installRouteTestRuntime(&services.AppServices{DefaultModel: "oai-resp/gpt-5-mini"})
	ctx := newRouteTestCtx("/chat/conv_123")

	node := id_.ShowPage(ctx, id_.Params{ID: "conv_123"})
	if node == nil {
		t.Fatal("expected page node")
	}
	if ctx.navigatePath != "" {
		t.Fatalf("route handler queued navigate path %q, want none", ctx.navigatePath)
	}
}

func TestChatRouteSSRRedirectsToCanonicalDemoRoute(t *testing.T) {
	installRouteTestRuntime(&services.AppServices{DefaultModel: "oai-resp/gpt-5-mini"})

	app := newRouteTestApp()
	app.Page("/chat/:id", id_.ShowPage)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/chat/conv_123", nil)
	req = req.WithContext(vango.WithUser(req.Context(), routeTestIdentity()))
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusSeeOther)
	}
	if got := rr.Header().Get("Location"); got != "/demo/conv_123" {
		t.Fatalf("Location = %q, want %q", got, "/demo/conv_123")
	}
}

func newRouteTestApp() *vango.App {
	cfg := vango.Config{
		DevMode: true,
		Static:  vango.StaticConfig{Prefix: "/"},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return vango.MustNew(cfg)
}

func installRouteTestRuntime(svc *services.AppServices) {
	if svc == nil {
		panic("services are required")
	}
	if svc.Pricing == nil {
		svc.Pricing = services.DefaultPricingCatalog()
	}
	if strings.TrimSpace(svc.DefaultModel) == "" {
		svc.DefaultModel = "oai-resp/gpt-5-mini"
	}
	appruntime.Set(&appruntime.Deps{
		Services: svc,
		Gateway:  http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config: appruntime.Config{
			BaseURL:       "http://localhost:3000",
			DefaultModel:  svc.DefaultModel,
			TopupOptions:  []int64{1000, 2500, 5000},
			WorkOSEnabled: true,
		},
	})
}

func timePtr(t time.Time) *time.Time {
	return &t
}

type routeRows struct {
	data [][]any
	idx  int
	err  error
}

func routeTestRows(rows ...[]any) *routeRows {
	return &routeRows{data: rows, idx: -1}
}

func (r *routeRows) Close() {}

func (r *routeRows) Err() error { return r.err }

func (r *routeRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

func (r *routeRows) Conn() *pgx.Conn { return nil }

func (r *routeRows) RawValues() [][]byte { return nil }

func (r *routeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

func (r *routeRows) Next() bool {
	r.idx++
	return r.idx >= 0 && r.idx < len(r.data)
}

func (r *routeRows) Scan(dest ...any) error {
	if r.idx < 0 || r.idx >= len(r.data) {
		return pgx.ErrNoRows
	}
	row := r.data[r.idx]
	if len(dest) != len(row) {
		r.err = fmt.Errorf("scan dest count %d != row count %d", len(dest), len(row))
		return r.err
	}
	for i := range row {
		if err := routeAssignScanValue(dest[i], row[i]); err != nil {
			r.err = err
			return err
		}
	}
	return nil
}

func (r *routeRows) Values() ([]any, error) {
	if r.idx < 0 || r.idx >= len(r.data) {
		return nil, pgx.ErrNoRows
	}
	return append([]any(nil), r.data[r.idx]...), nil
}

func routeTestRow(values ...any) pgx.Row {
	return &routeRow{values: append([]any(nil), values...)}
}

type routeRow struct {
	values []any
	err    error
}

func (r *routeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		r.err = fmt.Errorf("scan dest count %d != row count %d", len(dest), len(r.values))
		return r.err
	}
	for i := range r.values {
		if err := routeAssignScanValue(dest[i], r.values[i]); err != nil {
			r.err = err
			return err
		}
	}
	return nil
}

func routeAssignScanValue(dest any, src any) error {
	dstValue := reflect.ValueOf(dest)
	if !dstValue.IsValid() || dstValue.Kind() != reflect.Pointer || dstValue.IsNil() {
		return fmt.Errorf("scan target must be a non-nil pointer, got %T", dest)
	}
	return routeAssignValue(dstValue.Elem(), src)
}

func routeAssignValue(dst reflect.Value, src any) error {
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

type routeTestCtx struct {
	user            any
	stdCtx          context.Context
	path            string
	navigatePath    string
	navigateOptions server.AppliedNavigateOptions
}

func newRouteTestCtx(path string) *routeTestCtx {
	return &routeTestCtx{
		path:   path,
		stdCtx: context.Background(),
		user:   routeTestIdentity(),
	}
}

func routeTestIdentity() any {
	return workos.TestIdentity(
		workos.WithUserID("user_123"),
		workos.WithOrgID("org_123"),
		workos.WithEmail("tester@example.com"),
	)
}

var _ vango.Ctx = (*routeTestCtx)(nil)

func (c *routeTestCtx) Request() *http.Request              { return nil }
func (c *routeTestCtx) Path() string                        { return c.path }
func (c *routeTestCtx) Method() string                      { return http.MethodGet }
func (c *routeTestCtx) Query() url.Values                   { return url.Values{} }
func (c *routeTestCtx) QueryParam(string) string            { return "" }
func (c *routeTestCtx) Param(string) string                 { return "" }
func (c *routeTestCtx) Header(string) string                { return "" }
func (c *routeTestCtx) Cookie(string) (*http.Cookie, error) { return nil, http.ErrNoCookie }
func (c *routeTestCtx) Status(int)                          {}
func (c *routeTestCtx) Redirect(string, int)                {}
func (c *routeTestCtx) RedirectExternal(string, int)        {}
func (c *routeTestCtx) SetHeader(string, string)            {}
func (c *routeTestCtx) SetCookie(*http.Cookie)              {}
func (c *routeTestCtx) SetCookieStrict(*http.Cookie, ...server.CookieOption) error {
	return nil
}
func (c *routeTestCtx) Session() corevango.Session { return nil }
func (c *routeTestCtx) Surface() surface.Info      { return surface.Default() }
func (c *routeTestCtx) Shell() shell.Bridge {
	return shell.UnavailableBridge(surface.Default())
}
func (c *routeTestCtx) AuthSession() auth.Session         { return nil }
func (c *routeTestCtx) User() any                         { return c.user }
func (c *routeTestCtx) SetUser(user any)                  { c.user = user }
func (c *routeTestCtx) Principal() (auth.Principal, bool) { return auth.Principal{}, false }
func (c *routeTestCtx) MustPrincipal() auth.Principal     { return auth.Principal{} }
func (c *routeTestCtx) RevalidateAuth() error             { return nil }
func (c *routeTestCtx) BroadcastAuthLogout()              {}
func (c *routeTestCtx) Logger() *slog.Logger              { return slog.Default() }
func (c *routeTestCtx) Done() <-chan struct{}             { return nil }
func (c *routeTestCtx) SetValue(any, any)                 {}
func (c *routeTestCtx) Value(any) any                     { return nil }
func (c *routeTestCtx) Emit(string, any)                  {}
func (c *routeTestCtx) Dispatch(fn func()) {
	if fn != nil {
		fn()
	}
}
func (c *routeTestCtx) Navigate(path string, opts ...server.NavigateOption) {
	c.navigatePath = path
	c.navigateOptions = server.ApplyNavigateOptions(opts...)
}
func (c *routeTestCtx) StdContext() context.Context {
	if c.stdCtx != nil {
		return c.stdCtx
	}
	return context.Background()
}
func (c *routeTestCtx) WithStdContext(stdCtx context.Context) server.Ctx {
	c.stdCtx = stdCtx
	return c
}
func (c *routeTestCtx) Event() *server.Event                      { return nil }
func (c *routeTestCtx) PatchCount() int                           { return 0 }
func (c *routeTestCtx) AddPatchCount(int)                         {}
func (c *routeTestCtx) StormBudget() corevango.StormBudgetChecker { return nil }
func (c *routeTestCtx) Mode() int                                 { return 0 }
func (c *routeTestCtx) Asset(source string) string                { return source }
