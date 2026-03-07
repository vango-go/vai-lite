package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/vango-go/vai-lite/internal/services"
	"github.com/vango-go/vango"
	. "github.com/vango-go/vango/el"
	vserver "github.com/vango-go/vango/pkg/server"
	workos "github.com/vango-go/vango-workos"
)

type chatParams struct {
	ID string `param:"id"`
}

func (s *betaServer) registerPages() {
	s.app.Layout("/", s.rootLayout)
	s.app.Page("/", s.homePage)
	s.app.Page("/chat/:id", s.chatPage)
	s.app.Page("/settings", s.settingsPage)
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
		ctx.Navigate("/chat/" + conversations[0].ID, vango.WithReplace())
		return Div(Class("page-loading"), Text("Opening your workspace..."))
	}

	return s.pageShell(ctx, actor, nil,
		Div(
			Class("empty-state"),
			H1(Text("Your workspace is ready")),
			P(Text("Start a new chat, store shared provider keys in WorkOS Vault, or add wallet credits for hosted usage.")),
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
		"conversationId":      detail.Conversation.ID,
		"streamURL":           "/api/chat/stream",
		"uploadIntentURL":     "/api/uploads/intent",
		"uploadClaimURL":      "/api/uploads/claim",
		"csrfToken":           vserver.CSRFCtxToken(ctx),
		"model":               detail.Conversation.Model,
		"messages":            s.chatMessagesView(ctx.StdContext(), detail),
		"modelOptions":        s.modelOptions(detail.Conversation.Model),
		"allowBYOKOverride":   org.AllowBYOKOverride,
		"hostedUsageEnabled":  org.HostedUsageEnabled,
		"hasHostedProviders":  len(providerSecrets) > 0,
		"initialKeySource":    string(detail.Conversation.KeySource),
		"conversationTitle":   detail.Conversation.Title,
		"providerHints":       s.providerHints(),
		"settingsKeysURL":     "/settings/keys",
		"settingsBillingURL":  "/settings/billing",
		"currentBalanceCents": s.currentBalanceSafe(ctx.StdContext(), actor.OrgID),
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
				P(Textf("Hosted usage balance: %s", centsLabel(balance))),
				Ul(
					Li(Textf("Hosted usage enabled: %t", org.HostedUsageEnabled)),
					Li(Textf("Browser BYOK override allowed: %t", org.AllowBYOKOverride)),
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
				P(Text("Create organization-scoped gateway API keys for `/v1/*` clients. Keys are shown exactly once.")),
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
				H1(Text("Hosted provider keys")),
				P(Text("Store organization-shared provider keys in WorkOS Vault for billed hosted usage. Browser BYOK stays local to the chat app and is never persisted server-side.")),
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
				H1(Text("Wallet billing")),
				P(Textf("Current balance: %s", centsLabel(balance))),
				P(Text("Hosted requests debit the wallet only when organization-managed Vault keys are used. Browser BYOK requests remain non-billable.")),
				Div(
					Class("topup-grid"),
					RangeKeyed(s.cfg.TopupOptions,
						func(item int64) any { return fmt.Sprintf("%d", item) },
						func(item int64) *vango.VNode {
							return s.postForm(ctx, "/actions/billing/topup",
								Class("topup-card"),
								Input(Type("hidden"), Name("amount_cents"), Value(fmt.Sprintf("%d", item))),
								Strong(Text(centsLabel(item))),
								P(Text("Stripe hosted checkout")),
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
									P(Textf("%s • input %d • output %d • total %d", item.KeySource, item.InputTokens, item.OutputTokens, item.TotalTokens)),
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
			P(Text("Use the hosted gateway with WorkOS org auth, WorkOS Vault shared keys, wallet-based hosted usage, and browser-local BYOK when you want to bring your own credentials.")),
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
				Li(Text("Shared organization provider keys in WorkOS Vault")),
				Li(Text("Wallet credits via Stripe one-time top-ups")),
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
		link("/settings/developers", "Developers"),
		link("/settings/keys", "Hosted Keys"),
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
	models := make([]string, 0, len(s.services.Pricing.ByModel))
	for model := range s.services.Pricing.ByModel {
		models = append(models, model)
	}
	sort.Strings(models)
	out := make([]map[string]any, 0, len(models))
	for _, model := range models {
		out = append(out, map[string]any{
			"id":      model,
			"label":   model,
			"selected": model == current,
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
