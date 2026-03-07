package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vango-go/vai-lite/internal/services"
	"github.com/vango-go/vango"
	workos "github.com/vango-go/vango-workos"
	. "github.com/vango-go/vango/el"
	vserver "github.com/vango-go/vango/pkg/server"
)

type chatParams struct {
	ID string `param:"id"`
}

func (s *betaServer) registerPages() {
	s.app.Layout("/", s.rootLayout)
	s.app.Page("/", s.homePage)
	s.app.Page("/chat/:id", s.chatPage)
	s.app.Page("/settings", s.settingsPage)
	s.app.Page("/settings/access", s.accessPage)
	s.app.Page("/settings/developers", s.developersPage)
	s.app.Page("/settings/keys", s.keysPage)
	s.app.Page("/settings/billing", s.billingPage)
	s.app.SetNotFound(func(ctx vango.Ctx) *vango.VNode {
		return Div(
			Class("missing-page"),
			H1(Text("Page not found")),
			P(Text("The route you requested does not exist.")),
			A(Href("/"), Text("Back to home")),
		)
	})
}

func (s *betaServer) rootLayout(ctx vango.Ctx, children vango.Slot) *vango.VNode {
	return Html(
		Head(
			Meta(Charset("utf-8")),
			Meta(Name("viewport"), Content("width=device-width, initial-scale=1")),
			TitleEl(Text("Vango AI Gateway")),
			LinkEl(Rel("stylesheet"), Href(ctx.Asset("app.css"))),
		),
		Body(
			Class("beta-app"),
			children,
			VangoScripts(),
		),
	)
}

func (s *betaServer) homePage(ctx vango.Ctx) *vango.VNode {
	actor, _, ok := s.currentActorPage(ctx)
	if !ok {
		return s.landingPage(ctx)
	}
	conversations, err := s.services.ListConversations(ctx.StdContext(), actor.OrgID)
	if err == nil && len(conversations) > 0 {
		ctx.Navigate("/chat/"+conversations[0].ID, vango.WithReplace())
		return Div(Class("page-loading"), Text("Opening your workspace..."))
	}

	return s.pageShell(ctx, actor, nil,
		Div(
			Class("empty-state"),
			H1(Text("Your workspace is ready")),
			P(Text("Start a new chat, store shared workspace BYOK keys in WorkOS Vault, or add VAI credits for platform-hosted usage.")),
			Div(
				Class("cta-row"),
				s.postForm(ctx, "/actions/chat/new",
					Button(Type("submit"), Class("btn btn-primary"), Text("Start a chat")),
				),
				A(Href("/settings/keys"), Class("btn btn-secondary"), Text("Manage keys")),
				A(Href("/settings/billing"), Class("btn btn-secondary"), Text("Billing")),
			),
		),
	)
}

func (s *betaServer) chatPage(ctx vango.Ctx, p chatParams) *vango.VNode {
	actor, org, ok := s.currentActorPage(ctx)
	if !ok {
		return Div(Class("page-loading"), Text("Redirecting to sign in..."))
	}

	conversations, err := s.services.ListConversations(ctx.StdContext(), actor.OrgID)
	if err != nil {
		return s.pageError(ctx, actor, fmt.Errorf("load conversations: %w", err))
	}
	detail, err := s.services.Conversation(ctx.StdContext(), actor.OrgID, p.ID)
	if err != nil {
		return s.pageError(ctx, actor, fmt.Errorf("load conversation: %w", err))
	}
	providerSecrets, err := s.services.ListProviderSecrets(ctx.StdContext(), actor.OrgID)
	if err != nil {
		return s.pageError(ctx, actor, fmt.Errorf("load provider secrets: %w", err))
	}

	islandProps := map[string]any{
		"conversationId":        detail.Conversation.ID,
		"streamURL":             "/api/chat/stream",
		"uploadIntentURL":       "/api/uploads/intent",
		"uploadClaimURL":        "/api/uploads/claim",
		"csrfToken":             vserver.CSRFCtxToken(ctx),
		"model":                 detail.Conversation.Model,
		"messages":              s.chatMessagesView(ctx.StdContext(), detail),
		"modelOptions":          s.modelOptions(detail.Conversation.Model),
		"allowBrowserBYOK":      org.AllowBYOKOverride,
		"platformHostedEnabled": org.HostedUsageEnabled,
		"hasWorkspaceProviders": len(providerSecrets) > 0,
		"initialKeySource":      string(detail.Conversation.KeySource),
		"conversationTitle":     detail.Conversation.Title,
		"providerHints":         s.providerHints(),
		"settingsKeysURL":       "/settings/keys",
		"settingsBillingURL":    "/settings/billing",
		"settingsAccessURL":     "/settings/access",
		"currentBalanceCents":   s.currentBalanceSafe(ctx.StdContext(), actor.OrgID),
		"hostedModels":          s.hostedModelOptions(detail.Conversation.Model),
	}

	return s.pageShell(ctx, actor, conversations,
		Div(
			Class("chat-page"),
			s.sidebar(ctx, actor, conversations, detail.Conversation.ID),
			Main(
				Class("chat-main"),
				Header(
					Class("chat-header"),
					Div(
						H1(Text(detail.Conversation.Title)),
						P(Textf("Model: %s", detail.Conversation.Model)),
					),
					Div(
						Class("chat-header-actions"),
						A(Href("/settings/keys"), Class("btn btn-secondary"), Text("Keys")),
						A(Href("/settings/billing"), Class("btn btn-secondary"), Text("Billing")),
					),
				),
				Div(
					Class("chat-island"),
					JSIsland("vai-chat", islandProps),
				),
			),
		),
	)
}

func (s *betaServer) settingsPage(ctx vango.Ctx) *vango.VNode {
	actor, org, ok := s.currentActorPage(ctx)
	if !ok {
		return Div(Class("page-loading"), Text("Redirecting to sign in..."))
	}
	balance := s.currentBalanceSafe(ctx.StdContext(), actor.OrgID)
	return s.pageShell(ctx, actor, nil,
		Div(
			Class("settings-grid"),
			s.settingsNav("/settings"),
			Section(
				Class("settings-panel"),
				H1(Text("Workspace settings")),
				P(Textf("Workspace: %s", org.Name)),
				P(Textf("Default model: %s", org.DefaultModel)),
				P(Textf("VAI credits balance: %s", centsLabel(balance))),
				Ul(
					Li(Textf("VAI-hosted mode enabled: %t", org.HostedUsageEnabled)),
					Li(Textf("Browser BYOK enabled: %t", org.AllowBYOKOverride)),
				),
				Div(
					Class("stack"),
					H2(Text("Execution modes")),
					P(Text("VAI credits are consumed only when requests use VAI-hosted capacity. Workspace provider keys and browser-local BYOK stay customer-managed and never consume VAI credits.")),
					A(Href("/settings/access"), Class("btn btn-secondary"), Text("View hosted access details")),
				),
			),
		),
	)
}

func (s *betaServer) accessPage(ctx vango.Ctx) *vango.VNode {
	actor, _, ok := s.currentActorPage(ctx)
	if !ok {
		return Div(Class("page-loading"), Text("Redirecting to sign in..."))
	}
	models := s.services.Pricing.HostedModels()
	return s.pageShell(ctx, actor, nil,
		Div(
			Class("settings-grid"),
			s.settingsNav("/settings/access"),
			Section(
				Class("settings-panel"),
				H1(Text("VAI-hosted access")),
				P(Text("VAI credits apply only when a request uses VAI-managed upstream provider accounts. Customer-owned provider keys, whether stored in Workspace Keys or supplied from the browser, are always BYOK and never consume VAI credits.")),
				Ul(
					Li(Text("Gateway API key without provider headers: VAI-hosted and billable")),
					Li(Text("Gateway API key with provider headers: customer BYOK and non-billable")),
					Li(Text("Chat with workspace key: customer BYOK and non-billable")),
					Li(Text("Chat with browser key: customer BYOK and non-billable")),
				),
				H2(Text("Hosted retail catalog")),
				Div(
					Class("table-stack"),
					RangeKeyed(models,
						func(item services.HostedModelPrice) any { return item.Model },
						func(item services.HostedModelPrice) *vango.VNode {
							return Article(
								Class("card-row"),
								Div(
									Strong(Text(item.DisplayName)),
									P(Textf("%s • %s", item.Provider, item.Model)),
									P(Text(hostedPricingSummary(item))),
								),
								Span(Text(s.services.Pricing.Version)),
							)
						},
					),
				),
			),
		),
	)
}

func (s *betaServer) developersPage(ctx vango.Ctx) *vango.VNode {
	actor, _, ok := s.currentActorPage(ctx)
	if !ok {
		return Div(Class("page-loading"), Text("Redirecting to sign in..."))
	}
	keys, err := s.services.ListAPIKeys(ctx.StdContext(), actor.OrgID)
	if err != nil {
		return s.pageError(ctx, actor, fmt.Errorf("load api keys: %w", err))
	}
	return s.pageShell(ctx, actor, nil,
		Div(
			Class("settings-grid"),
			s.settingsNav("/settings/developers"),
			Section(
				Class("settings-panel"),
				H1(Text("Developer access")),
				P(Text("Create organization-scoped gateway API keys for `/v1/*` clients. Requests without provider headers use billable VAI-hosted access. Requests with provider headers are treated as customer BYOK and remain non-billable.")),
				s.postForm(ctx, "/actions/developers/api-keys",
					Div(
						Class("stack"),
						Label(Text("Key name")),
						Input(Type("text"), Name("name"), Placeholder("Production backend")),
						Button(Type("submit"), Class("btn btn-primary"), Text("Create API key")),
					),
				),
				Div(
					Class("table-stack"),
					RangeKeyed(keys,
						func(item services.APIKeyRecord) any { return item.ID },
						func(item services.APIKeyRecord) *vango.VNode {
							status := "active"
							if item.RevokedAt != nil {
								status = "revoked"
							}
							return Article(
								Class("card-row"),
								Div(
									Strong(Text(item.Name)),
									P(Textf("Prefix: %s", item.TokenPrefix)),
									P(Textf("Created: %s", item.CreatedAt.Format(time.RFC822))),
									P(Textf("Status: %s", status)),
								),
								If(item.RevokedAt == nil,
									s.postForm(ctx, "/actions/developers/api-keys/revoke",
										Input(Type("hidden"), Name("id"), Value(item.ID)),
										Button(Type("submit"), Class("btn btn-danger"), Text("Revoke")),
									),
								),
							)
						},
					),
				),
			),
		),
	)
}

func (s *betaServer) keysPage(ctx vango.Ctx) *vango.VNode {
	actor, _, ok := s.currentActorPage(ctx)
	if !ok {
		return Div(Class("page-loading"), Text("Redirecting to sign in..."))
	}
	secrets, err := s.services.ListProviderSecrets(ctx.StdContext(), actor.OrgID)
	if err != nil {
		return s.pageError(ctx, actor, fmt.Errorf("load provider secrets: %w", err))
	}

	return s.pageShell(ctx, actor, nil,
		Div(
			Class("settings-grid"),
			s.settingsNav("/settings/keys"),
			Section(
				Class("settings-panel"),
				H1(Text("Workspace provider keys")),
				P(Text("Store organization-shared provider keys in WorkOS Vault for shared BYOK usage. These customer-managed keys can be used from the chat app and gateway flows without consuming VAI credits. Browser BYOK stays local to the chat app and is never persisted server-side.")),
				s.postForm(ctx, "/actions/provider-secrets/store",
					Div(
						Class("stack"),
						Label(Text("Provider")),
						Select(
							Name("provider"),
							Option(Value("anthropic"), Text("Anthropic")),
							Option(Value("openai"), Text("OpenAI")),
							Option(Value("gem-dev"), Text("Gemini Dev")),
							Option(Value("groq"), Text("Groq")),
							Option(Value("tavily"), Text("Tavily")),
							Option(Value("exa"), Text("Exa")),
							Option(Value("firecrawl"), Text("Firecrawl")),
						),
						Label(Text("Secret value")),
						Input(Type("password"), Name("secret_value"), Placeholder("sk-...")),
						Button(Type("submit"), Class("btn btn-primary"), Text("Store or update")),
					),
				),
				Div(
					Class("table-stack"),
					RangeKeyed(secrets,
						func(item services.ProviderSecretRecord) any { return item.ID },
						func(item services.ProviderSecretRecord) *vango.VNode {
							return Article(
								Class("card-row"),
								Div(
									Strong(Text(item.Provider)),
									P(Text(item.ObjectName)),
									P(Textf("Updated: %s", item.UpdatedAt.Format(time.RFC822))),
								),
								s.postForm(ctx, "/actions/provider-secrets/delete",
									Input(Type("hidden"), Name("provider"), Value(item.Provider)),
									Button(Type("submit"), Class("btn btn-danger"), Text("Delete")),
								),
							)
						},
					),
				),
			),
		),
	)
}

func (s *betaServer) billingPage(ctx vango.Ctx) *vango.VNode {
	actor, _, ok := s.currentActorPage(ctx)
	if !ok {
		return Div(Class("page-loading"), Text("Redirecting to sign in..."))
	}
	balance := s.currentBalanceSafe(ctx.StdContext(), actor.OrgID)
	ledger, err := s.services.Ledger(ctx.StdContext(), actor.OrgID)
	if err != nil {
		return s.pageError(ctx, actor, fmt.Errorf("load ledger: %w", err))
	}
	usage, err := s.services.Usage(ctx.StdContext(), actor.OrgID)
	if err != nil {
		return s.pageError(ctx, actor, fmt.Errorf("load usage: %w", err))
	}

	return s.pageShell(ctx, actor, nil,
		Div(
			Class("settings-grid"),
			s.settingsNav("/settings/billing"),
			Section(
				Class("settings-panel"),
				H1(Text("VAI credits")),
				P(Textf("Current balance: %s", centsLabel(balance))),
				P(Text("Credits are consumed only for VAI-hosted requests that run on VAI-managed upstream accounts. Workspace keys and browser-local BYOK remain non-billable.")),
				s.postForm(ctx, "/actions/billing/topup",
					Class("topup-card"),
					Div(
						Class("stack"),
						Label(Text("Add credits (USD)")),
						Input(
							Type("number"),
							Name("amount_usd"),
							Placeholder("25.00"),
							Attr("min", "1.00"),
							Attr("max", "10000.00"),
							Attr("step", "0.01"),
							Attr("inputmode", "decimal"),
							Attr("required", "required"),
						),
						P(Text("Enter any amount between $1.00 and $10,000.00.")),
						Button(Type("submit"), Class("btn btn-primary"), Text("Checkout with Stripe")),
					),
				),
				H2(Text("Suggested amounts")),
				Div(
					Class("topup-grid"),
					RangeKeyed(s.cfg.TopupOptions,
						func(item int64) any { return fmt.Sprintf("%d", item) },
						func(item int64) *vango.VNode {
							return s.postForm(ctx, "/actions/billing/topup",
								Class("topup-card"),
								Input(Type("hidden"), Name("amount_cents"), Value(fmt.Sprintf("%d", item))),
								Strong(Text(centsLabel(item))),
								P(Text("Quick add")),
								Button(Type("submit"), Class("btn btn-primary"), Text("Add credits")),
							)
						},
					),
				),
				H2(Text("Wallet ledger")),
				Div(
					Class("table-stack"),
					RangeKeyed(ledger,
						func(item services.WalletEntry) any { return item.ID },
						func(item services.WalletEntry) *vango.VNode {
							return Article(
								Class("card-row"),
								Div(
									Strong(Text(item.Description)),
									P(Textf("%s • %s", item.Kind, centsLabel(item.AmountCents))),
								),
								Span(Text(item.CreatedAt.Format(time.RFC822))),
							)
						},
					),
				),
				H2(Text("Usage events")),
				Div(
					Class("table-stack"),
					RangeKeyed(usage,
						func(item services.UsageEntry) any { return item.ID },
						func(item services.UsageEntry) *vango.VNode {
							return Article(
								Class("card-row"),
								Div(
									Strong(Text(item.Model)),
									P(Textf("%s via %s • input %d • output %d • total %d", keySourceLabel(item.KeySource), accessCredentialLabel(item.AccessCredential), item.InputTokens, item.OutputTokens, item.TotalTokens)),
									P(Textf("Estimated cost: %s", centsLabel(item.EstimatedCostCents))),
								),
								Span(Text(item.CreatedAt.Format(time.RFC822))),
							)
						},
					),
				),
			),
		),
	)
}

func (s *betaServer) currentActorPage(ctx vango.Ctx) (services.UserIdentity, *services.Organization, bool) {
	identity, ok := workos.CurrentIdentity(ctx)
	if !ok {
		if s.workos != nil {
			ctx.Redirect("/auth/signin?return_to="+url.QueryEscape(ctx.Path()), http.StatusSeeOther)
		}
		return services.UserIdentity{}, nil, false
	}
	actor, err := s.services.EnsureIdentity(ctx.StdContext(), identity)
	if err != nil {
		s.logger.Error("ensure identity failed", "error", err)
		return services.UserIdentity{}, nil, false
	}
	org, err := s.services.Org(ctx.StdContext(), actor.OrgID)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, pgx.ErrNoRows) {
			s.logger.Warn("using fallback org projection", "org_id", actor.OrgID, "error", err)
			return actor, &services.Organization{
				ID:                 actor.OrgID,
				Name:               actor.Name + "'s workspace",
				AllowBYOKOverride:  false,
				HostedUsageEnabled: true,
				DefaultModel:       s.cfg.DefaultModel,
			}, true
		}
		s.logger.Error("load org failed", "error", err)
		return services.UserIdentity{}, nil, false
	}
	return actor, org, true
}

func (s *betaServer) landingPage(ctx vango.Ctx) *vango.VNode {
	return Div(
		Class("landing"),
		Div(
			Class("landing-copy"),
			H1(Text("Vango AI Gateway beta")),
			P(Text("Use VAI-hosted gateway access with wallet-backed credits, or bring your own provider keys through shared Workspace Keys and browser-local BYOK without consuming VAI credits.")),
			Div(
				Class("cta-row"),
				If(s.workos != nil,
					A(Href("/auth/signup"), Class("btn btn-primary"), Text("Create workspace")),
				),
				If(s.workos != nil,
					A(Href("/auth/signin"), Class("btn btn-secondary"), Text("Sign in")),
				),
				If(s.workos == nil,
					Span(Class("badge"), Text("WorkOS auth is not configured")),
				),
			),
		),
		Div(
			Class("landing-panel"),
			H2(Text("Private beta scope")),
			Ul(
				Li(Text("Shared organization BYOK provider keys in WorkOS Vault")),
				Li(Text("VAI credits via Stripe one-time top-ups")),
				Li(Text("Stable `/v1/*` gateway for backend clients")),
				Li(Text("Chat UI with markdown streaming island and attachment uploads")),
			),
		),
	)
}

func (s *betaServer) pageShell(ctx vango.Ctx, actor services.UserIdentity, conversations []services.Conversation, content *vango.VNode) *vango.VNode {
	return Div(
		Class("page-shell"),
		Header(
			Class("app-header"),
			Div(
				Class("brand"),
				A(Href("/"), Text("vai-lite beta")),
				If(actor.Name != "",
					Span(Class("brand-subtitle"), Text(actor.Name)),
				),
			),
			Nav(
				Class("top-nav"),
				A(Href("/settings"), Text("Settings")),
				A(Href("/settings/access"), Text("VAI credits")),
				A(Href("/settings/developers"), Text("Developers")),
				A(Href("/settings/keys"), Text("Keys")),
				A(Href("/settings/billing"), Text("Billing")),
				If(s.workos != nil,
					Form(
						Method("POST"),
						Action("/auth/logout"),
						csrfField(ctx),
						Button(Type("submit"), Class("btn btn-ghost"), Text("Sign out")),
					),
				),
			),
		),
		content,
	)
}

func (s *betaServer) sidebar(ctx vango.Ctx, actor services.UserIdentity, conversations []services.Conversation, activeConversationID string) *vango.VNode {
	return Aside(
		Class("chat-sidebar"),
		s.postForm(ctx, "/actions/chat/new",
			Button(Type("submit"), Class("btn btn-primary sidebar-create"), Text("New chat")),
		),
		Div(
			Class("conversation-list"),
			RangeKeyed(conversations,
				func(item services.Conversation) any { return item.ID },
				func(item services.Conversation) *vango.VNode {
					className := "conversation-link"
					if item.ID == activeConversationID {
						className += " conversation-link-active"
					}
					return A(
						Href("/chat/"+item.ID),
						Class(className),
						Strong(Text(item.Title)),
						Span(Text(item.Model)),
					)
				},
			),
		),
		Div(
			Class("sidebar-footer"),
			Span(Text(actor.Email)),
		),
	)
}

func (s *betaServer) settingsNav(active string) *vango.VNode {
	link := func(href, label string) *vango.VNode {
		className := "settings-link"
		if href == active {
			className += " settings-link-active"
		}
		return A(Href(href), Class(className), Text(label))
	}
	return Nav(
		Class("settings-nav"),
		link("/settings", "Overview"),
		link("/settings/access", "VAI-hosted"),
		link("/settings/developers", "Developers"),
		link("/settings/keys", "Workspace Keys"),
		link("/settings/billing", "Billing"),
	)
}

func (s *betaServer) pageError(ctx vango.Ctx, actor services.UserIdentity, err error) *vango.VNode {
	return s.pageShell(ctx, actor, nil,
		Section(
			Class("settings-panel"),
			H1(Text("Something failed")),
			P(Text(err.Error())),
		),
	)
}

func (s *betaServer) postForm(ctx vango.Ctx, action string, children ...any) *vango.VNode {
	nodes := make([]any, 0, len(children)+1)
	nodes = append(nodes, Method("POST"), Action(action), csrfField(ctx))
	for _, child := range children {
		nodes = append(nodes, child)
	}
	return Form(nodes...)
}

func csrfField(ctx vango.Ctx) *vango.VNode {
	token := vserver.CSRFCtxToken(ctx)
	if token == "" {
		return nil
	}
	return Input(Type("hidden"), Name("csrf"), Value(token))
}

func (s *betaServer) currentBalanceSafe(ctx context.Context, orgID string) int64 {
	balance, err := s.services.CurrentBalance(ctx, orgID)
	if err != nil {
		return 0
	}
	return balance
}

func (s *betaServer) modelOptions(current string) []map[string]any {
	models := make([]string, 0, len(s.services.Pricing.ByModel)+1)
	for model := range s.services.Pricing.ByModel {
		models = append(models, model)
	}
	if current != "" {
		models = append(models, current)
	}
	sort.Strings(models)
	models = compactStrings(models)
	out := make([]map[string]any, 0, len(models))
	for _, model := range models {
		_, hostedAvailable := s.services.Pricing.HostedModel(model)
		out = append(out, map[string]any{
			"id":              model,
			"label":           model,
			"selected":        model == current,
			"hostedAvailable": hostedAvailable,
		})
	}
	return out
}

func (s *betaServer) hostedModelOptions(current string) []map[string]any {
	models := s.services.Pricing.HostedModels()
	out := make([]map[string]any, 0, len(models))
	for _, model := range models {
		out = append(out, map[string]any{
			"id":                                   model.Model,
			"displayName":                          model.DisplayName,
			"provider":                             model.Provider,
			"retailInputMicrosUSDPer1M":            model.RetailInputMicrosUSDPer1M,
			"retailLongInputMicrosUSDPer1M":        model.RetailLongInputMicrosUSDPer1M,
			"retailAudioInputMicrosUSDPer1M":       model.RetailAudioInputMicrosUSDPer1M,
			"retailCachedInputMicrosUSDPer1M":      model.RetailCachedInputMicrosUSDPer1M,
			"retailLongCachedInputMicrosUSDPer1M":  model.RetailLongCachedInputMicrosUSDPer1M,
			"retailAudioCachedInputMicrosUSDPer1M": model.RetailAudioCachedInputMicrosUSDPer1M,
			"retailCacheWrite5mMicrosUSDPer1M":     model.RetailCacheWrite5mMicrosUSDPer1M,
			"retailCacheWrite1hMicrosUSDPer1M":     model.RetailCacheWrite1hMicrosUSDPer1M,
			"retailCacheStorageMicrosUSDPer1MHour": model.RetailCacheStorageMicrosUSDPer1MHour,
			"retailOutputMicrosUSDPer1M":           model.RetailOutputMicrosUSDPer1M,
			"retailLongOutputMicrosUSDPer1M":       model.RetailLongOutputMicrosUSDPer1M,
			"retailImageOutputMicrosUSDPer1M":      model.RetailImageOutputMicrosUSDPer1M,
			"promptTierThresholdTokens":            model.PromptTierThresholdTokens,
			"minimumChargeCents":                   model.MinimumChargeCents,
			"selected":                             model.Model == current,
		})
	}
	return out
}

func (s *betaServer) providerHints() []map[string]string {
	return []map[string]string{
		{"provider": "anthropic", "header": "X-Provider-Key-Anthropic"},
		{"provider": "openai", "header": "X-Provider-Key-OpenAI"},
		{"provider": "gem-dev", "header": "X-Provider-Key-Gemini"},
		{"provider": "groq", "header": "X-Provider-Key-Groq"},
	}
}

func (s *betaServer) chatMessagesView(ctx context.Context, detail *services.ConversationDetail) []map[string]any {
	out := make([]map[string]any, 0, len(detail.Messages))
	for _, msg := range detail.Messages {
		attachments := make([]map[string]any, 0, len(msg.Attachments))
		for _, att := range msg.Attachments {
			attachmentURL := ""
			if s.services.BlobStore != nil {
				if signed, err := s.services.BlobStore.PresignGet(ctx, att.BlobRef.Key, 30*time.Minute); err == nil {
					attachmentURL = signed
				}
			}
			attachments = append(attachments, map[string]any{
				"id":          att.ID,
				"filename":    att.Filename,
				"contentType": att.ContentType,
				"sizeBytes":   att.SizeBytes,
				"url":         attachmentURL,
			})
		}

		var toolTrace any
		if strings.TrimSpace(msg.ToolTrace) != "" && msg.ToolTrace != "null" {
			_ = json.Unmarshal([]byte(msg.ToolTrace), &toolTrace)
		}

		out = append(out, map[string]any{
			"id":          msg.ID,
			"role":        msg.Role,
			"text":        msg.BodyText,
			"keySource":   string(msg.KeySource),
			"createdAt":   msg.CreatedAt.Format(time.RFC3339),
			"attachments": attachments,
			"toolTrace":   toolTrace,
		})
	}
	return out
}

func centsLabel(amount int64) string {
	sign := ""
	if amount < 0 {
		sign = "-"
		amount = -amount
	}
	return fmt.Sprintf("%s$%.2f", sign, float64(amount)/100)
}

func compactStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	var prev string
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" || item == prev {
			continue
		}
		out = append(out, item)
		prev = item
	}
	return out
}

func keySourceLabel(source services.KeySource) string {
	switch source {
	case services.KeySourcePlatformHosted:
		return "VAI-hosted"
	case services.KeySourceCustomerBYOKBrowser:
		return "Browser BYOK"
	case services.KeySourceCustomerBYOKVault:
		return "Workspace key"
	case services.KeySourceCustomerBYOKExternal:
		return "External BYOK"
	default:
		return string(source)
	}
}

func accessCredentialLabel(credential services.AccessCredential) string {
	switch credential {
	case services.AccessCredentialGatewayAPIKey:
		return "gateway API key"
	case services.AccessCredentialSessionAuth:
		return "session auth"
	default:
		return string(credential)
	}
}

func hostedInputRateLabel(model services.HostedModelPrice) string {
	parts := []string{fmt.Sprintf("Input %s / 1M", microsUSDPer1MLabel(model.RetailInputMicrosUSDPer1M))}
	if model.PromptTierThresholdTokens != nil && model.RetailLongInputMicrosUSDPer1M != nil {
		parts = append(parts, fmt.Sprintf("> %s prompt %s / 1M", compactTokenCount(*model.PromptTierThresholdTokens), microsUSDPer1MLabel(*model.RetailLongInputMicrosUSDPer1M)))
	}
	if model.RetailAudioInputMicrosUSDPer1M != nil {
		parts = append(parts, fmt.Sprintf("Audio input %s / 1M", microsUSDPer1MLabel(*model.RetailAudioInputMicrosUSDPer1M)))
	}
	if model.RetailCachedInputMicrosUSDPer1M != nil {
		parts = append(parts, fmt.Sprintf("Cached %s / 1M", microsUSDPer1MLabel(*model.RetailCachedInputMicrosUSDPer1M)))
	}
	if model.PromptTierThresholdTokens != nil && model.RetailLongCachedInputMicrosUSDPer1M != nil {
		parts = append(parts, fmt.Sprintf("> %s cached %s / 1M", compactTokenCount(*model.PromptTierThresholdTokens), microsUSDPer1MLabel(*model.RetailLongCachedInputMicrosUSDPer1M)))
	}
	if model.RetailAudioCachedInputMicrosUSDPer1M != nil {
		parts = append(parts, fmt.Sprintf("Audio cached %s / 1M", microsUSDPer1MLabel(*model.RetailAudioCachedInputMicrosUSDPer1M)))
	}
	return strings.Join(parts, " • ")
}

func hostedCacheWriteLabel(model services.HostedModelPrice) string {
	parts := make([]string, 0, 3)
	if model.RetailCacheWrite5mMicrosUSDPer1M != nil && model.RetailCacheWrite1hMicrosUSDPer1M != nil {
		parts = append(parts, fmt.Sprintf("Cache write 5m %s / 1M", microsUSDPer1MLabel(*model.RetailCacheWrite5mMicrosUSDPer1M)))
		parts = append(parts, fmt.Sprintf("Cache write 1h %s / 1M", microsUSDPer1MLabel(*model.RetailCacheWrite1hMicrosUSDPer1M)))
	} else if model.RetailCacheWrite5mMicrosUSDPer1M != nil {
		parts = append(parts, fmt.Sprintf("Cache write %s / 1M", microsUSDPer1MLabel(*model.RetailCacheWrite5mMicrosUSDPer1M)))
	} else if model.RetailCacheWrite1hMicrosUSDPer1M != nil {
		parts = append(parts, fmt.Sprintf("Cache write 1h %s / 1M", microsUSDPer1MLabel(*model.RetailCacheWrite1hMicrosUSDPer1M)))
	}
	if model.RetailCacheStorageMicrosUSDPer1MHour != nil {
		parts = append(parts, fmt.Sprintf("Cache storage %s / 1M / hour", microsUSDPer1MLabel(*model.RetailCacheStorageMicrosUSDPer1MHour)))
	}
	if len(parts) == 0 {
		return "No cache tier"
	}
	return strings.Join(parts, " • ")
}

func microsUSDPer1MLabel(micros int64) string {
	return fmt.Sprintf("$%.6g", float64(micros)/1_000_000)
}

func hostedOutputRateLabel(model services.HostedModelPrice) string {
	parts := []string{fmt.Sprintf("Output %s / 1M", microsUSDPer1MLabel(model.RetailOutputMicrosUSDPer1M))}
	if model.PromptTierThresholdTokens != nil && model.RetailLongOutputMicrosUSDPer1M != nil {
		parts = append(parts, fmt.Sprintf("> %s output %s / 1M", compactTokenCount(*model.PromptTierThresholdTokens), microsUSDPer1MLabel(*model.RetailLongOutputMicrosUSDPer1M)))
	}
	if model.RetailImageOutputMicrosUSDPer1M != nil {
		parts = append(parts, fmt.Sprintf("Image output %s / 1M", microsUSDPer1MLabel(*model.RetailImageOutputMicrosUSDPer1M)))
	}
	return strings.Join(parts, " • ")
}

func hostedPricingSummary(model services.HostedModelPrice) string {
	return fmt.Sprintf("%s • %s • %s • Minimum %s", hostedInputRateLabel(model), hostedCacheWriteLabel(model), hostedOutputRateLabel(model), centsLabel(model.MinimumChargeCents))
}

func compactTokenCount(value int) string {
	if value >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(value)/1_000_000)
	}
	if value >= 1_000 {
		return fmt.Sprintf("%.0fK", float64(value)/1_000)
	}
	return fmt.Sprintf("%d", value)
}
