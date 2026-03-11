package components

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/vango-go/vai-lite/internal/appruntime"
	"github.com/vango-go/vai-lite/internal/services"
	"github.com/vango-go/vango"
	"github.com/vango-go/vango/setup"
)

type DemoEntryPageProps struct {
	Actor services.UserIdentity
}

type DemoAliasPageProps struct {
	Actor          services.UserIdentity
	ConversationID string
}

func DemoEntryPage(p DemoEntryPageProps) vango.Component {
	return vango.Setup(p, func(s vango.SetupCtx[DemoEntryPageProps]) vango.RenderFn {
		props := s.Props()
		navigated := setup.Signal(&s, false)
		summaries := setup.ResourceKeyed(&s,
			func() string { return props.Get().Actor.OrgID },
			func(ctx context.Context, orgID string) ([]services.ManagedConversationSummary, error) {
				items, err := appruntime.Get().Services.ListManagedConversationSummaries(ctx, orgID)
				if err != nil {
					return nil, fmt.Errorf("load demo chats: %w", err)
				}
				return items, nil
			},
		)

		s.Effect(func() vango.Cleanup {
			if navigated.Get() || summaries.State() != vango.Ready {
				return nil
			}
			ctx := vango.UseCtx()
			if ctx == nil || ctx.Session() == nil {
				return nil
			}
			navigated.Set(true)
			ctx.Navigate(demoConversationTargetPath(summaries.Data()), vango.WithReplace())
			return nil
		})

		return func() *vango.VNode {
			ctx := vango.UseCtx()
			actor := props.Get().Actor
			switch summaries.State() {
			case vango.Ready:
				if ctx != nil && ctx.Session() == nil {
					ctx.Redirect(demoConversationTargetPath(summaries.Data()), http.StatusSeeOther)
				}
				return AppShell(ctx, actor, LoadingPanel("Opening demo..."))
			case vango.Error:
				return AppShell(ctx, actor, PageErrorPanel(summaries.Error()))
			default:
				return AppShell(ctx, actor, LoadingPanel("Opening demo..."))
			}
		}
	})
}

func DemoAliasPage(p DemoAliasPageProps) vango.Component {
	return vango.Setup(p, func(s vango.SetupCtx[DemoAliasPageProps]) vango.RenderFn {
		props := s.Props()
		targetPath := "/demo/" + strings.TrimSpace(props.Peek().ConversationID)

		s.OnMount(func() vango.Cleanup {
			ctx := vango.UseCtx()
			if ctx == nil || ctx.Session() == nil {
				return nil
			}
			ctx.Navigate(targetPath, vango.WithReplace())
			return nil
		})

		return func() *vango.VNode {
			ctx := vango.UseCtx()
			if ctx != nil && ctx.Session() == nil {
				ctx.Redirect(targetPath, http.StatusSeeOther)
			}
			return AppShell(ctx, props.Get().Actor, LoadingPanel("Opening demo..."))
		}
	})
}

func demoConversationTargetPath(summaries []services.ManagedConversationSummary) string {
	conversationID := newDraftConversationID()
	if len(summaries) > 0 {
		if existingID := strings.TrimSpace(summaries[0].ID); existingID != "" {
			conversationID = existingID
		}
	}
	return "/demo/" + conversationID
}
