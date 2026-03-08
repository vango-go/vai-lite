package components

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/vango-go/vai-lite/internal/appruntime"
	"github.com/vango-go/vai-lite/internal/services"
	"github.com/vango-go/vango"
	workos "github.com/vango-go/vango-workos"
	. "github.com/vango-go/vango/el"
)

func CurrentActor(ctx vango.Ctx) (services.UserIdentity, bool) {
	identity, ok := workos.CurrentIdentity(ctx)
	if !ok {
		return services.UserIdentity{}, false
	}
	actor, err := services.ActorFromIdentity(identity)
	if err != nil {
		appruntime.Get().Logger.Error("actor projection failed", "error", err)
		return services.UserIdentity{}, false
	}
	return actor, true
}

func AuthRedirectPage() *vango.VNode {
	if appruntime.Get().Config.WorkOSEnabled {
		return Div(Class("page-loading"), Text("Redirecting to sign in..."))
	}
	return Section(
		Class("settings-panel"),
		H1(Text("Authentication unavailable")),
		P(Text("WorkOS authentication is not configured for this environment.")),
	)
}

func ResolveOrganization(ctx context.Context, actor services.UserIdentity) (*services.Organization, error) {
	org, err := appruntime.Get().Services.Org(ctx, actor.OrgID)
	if err == nil {
		return org, nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, pgx.ErrNoRows) {
		appruntime.Get().Logger.Warn("using fallback org projection", "org_id", actor.OrgID, "error", err)
		return &services.Organization{
			ID:                 actor.OrgID,
			Name:               actor.Name + "'s workspace",
			AllowBYOKOverride:  false,
			HostedUsageEnabled: true,
			DefaultModel:       appruntime.Get().Config.DefaultModel,
		}, nil
	}
	return nil, err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
