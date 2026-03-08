package components

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/vango-go/vai-lite/internal/appruntime"
	"github.com/vango-go/vai-lite/internal/chatruntime"
	"github.com/vango-go/vai-lite/internal/services"
	"github.com/vango-go/vango"
	. "github.com/vango-go/vango/el"
	"github.com/vango-go/vango/setup"
)

type ChatPageProps struct {
	Actor          services.UserIdentity
	ConversationID string
}

type chatPageData struct {
	Org                 *services.Organization
	Conversations       []services.Conversation
	Detail              *services.ConversationDetail
	ProviderSecrets     []services.ProviderSecretRecord
	CurrentBalanceCents int64
	Messages            []map[string]any
}

func ChatPage(p ChatPageProps) vango.Component {
	return vango.Setup(p, func(s vango.SetupCtx[ChatPageProps]) vango.RenderFn {
		pageCtx := s.Ctx()
		props := s.Props()
		pendingConversationID := setup.Signal(&s, "")

		pageData := setup.ResourceKeyed(&s,
			func() string {
				current := props.Get()
				return current.Actor.OrgID + "|" + current.ConversationID
			},
			func(ctx context.Context, _ string) (*chatPageData, error) {
				current := props.Peek()
				org, err := ResolveOrganization(ctx, current.Actor)
				if err != nil {
					return nil, fmt.Errorf("load org: %w", err)
				}
				conversations, err := appruntime.Get().Services.ListConversations(ctx, current.Actor.OrgID)
				if err != nil {
					return nil, fmt.Errorf("load conversations: %w", err)
				}
				detail, err := appruntime.Get().Services.Conversation(ctx, current.Actor.OrgID, current.ConversationID)
				if err != nil {
					return nil, fmt.Errorf("load conversation: %w", err)
				}
				providerSecrets, err := appruntime.Get().Services.ListProviderSecrets(ctx, current.Actor.OrgID)
				if err != nil {
					return nil, fmt.Errorf("load provider secrets: %w", err)
				}
				balance, err := appruntime.Get().Services.CurrentBalance(ctx, current.Actor.OrgID)
				if err != nil {
					balance = 0
				}
				messageViews, err := chatMessagesView(ctx, detail)
				if err != nil {
					return nil, fmt.Errorf("prepare messages: %w", err)
				}
				return &chatPageData{
					Org:                 org,
					Conversations:       conversations,
					Detail:              detail,
					ProviderSecrets:     providerSecrets,
					CurrentBalanceCents: balance,
					Messages:            messageViews,
				}, nil
			},
		)

		newConversation := setup.Action(&s,
			func(ctx context.Context, _ struct{}) (*services.Conversation, error) {
				return appruntime.Get().Services.CreateConversation(
					ctx,
					props.Peek().Actor,
					"",
					appruntime.Get().Config.DefaultModel,
					services.KeySourcePlatformHosted,
				)
			},
			vango.DropWhileRunning(),
			vango.ActionOnSuccess(func(result any) {
				conversation, ok := result.(*services.Conversation)
				if !ok || conversation == nil {
					return
				}
				pendingConversationID.Set(conversation.ID)
			}),
		)

		s.Effect(func() vango.Cleanup {
			if conversationID := pendingConversationID.Get(); conversationID != "" {
				if ctx := vango.UseCtx(); ctx != nil {
					pendingConversationID.Set("")
					ctx.Navigate("/chat/" + conversationID)
				}
			}
			return nil
		})

		var activeRunMu sync.Mutex
		var activeRunRequestID string
		var activeRunCancel context.CancelFunc

		setActiveRun := func(requestID string, cancel context.CancelFunc) {
			activeRunMu.Lock()
			activeRunRequestID = requestID
			activeRunCancel = cancel
			activeRunMu.Unlock()
		}
		clearActiveRun := func(requestID string) {
			activeRunMu.Lock()
			if activeRunRequestID == requestID {
				activeRunRequestID = ""
				activeRunCancel = nil
			}
			activeRunMu.Unlock()
		}
		stopActiveRun := func(requestID string) bool {
			activeRunMu.Lock()
			cancel := activeRunCancel
			activeRequestID := activeRunRequestID
			if cancel == nil {
				activeRunMu.Unlock()
				return false
			}
			if requestID != "" && activeRequestID != requestID {
				activeRunMu.Unlock()
				return false
			}
			activeRunCancel = nil
			activeRunRequestID = ""
			activeRunMu.Unlock()
			cancel()
			return true
		}

		uploadIntent := setup.Action(&s,
			func(ctx context.Context, in chatUploadIntentCommand) (struct{}, error) {
				intent, err := appruntime.Get().Services.CreateImageUploadIntent(ctx, props.Peek().Actor, in.Filename, in.ContentType, in.SizeBytes)
				if err != nil {
					dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
						Type:      "upload_intent_error",
						RequestID: in.RequestID,
						Error:     err.Error(),
					})
					return struct{}{}, err
				}
				dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
					Type:      "upload_intent_ready",
					RequestID: in.RequestID,
					Intent:    intent,
				})
				return struct{}{}, nil
			},
			vango.Queue(8),
		)

		uploadClaim := setup.Action(&s,
			func(ctx context.Context, in chatUploadClaimCommand) (struct{}, error) {
				attachment, err := appruntime.Get().Services.ClaimImageAttachment(
					ctx,
					props.Peek().Actor,
					props.Peek().ConversationID,
					"",
					in.Filename,
					in.ContentType,
					in.SizeBytes,
					in.IntentToken,
				)
				if err != nil {
					dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
						Type:      "upload_claim_error",
						RequestID: in.RequestID,
						Error:     err.Error(),
					})
					return struct{}{}, err
				}
				dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
					Type:       "upload_claim_ready",
					RequestID:  in.RequestID,
					Attachment: buildChatAttachmentPayload(ctx, attachment),
				})
				return struct{}{}, nil
			},
			vango.Queue(8),
		)

		submitChat := setup.Action(&s,
			func(ctx context.Context, in chatSubmitCommand) (struct{}, error) {
				actor := props.Peek().Actor
				if in.RequestID == "" {
					err := errors.New("chat request id is required")
					dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
						Type:      "chat_error",
						RequestID: in.RequestID,
						Error:     err.Error(),
					})
					return struct{}{}, err
				}

				org, err := appruntime.Get().Services.Org(ctx, actor.OrgID)
				if err != nil {
					dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
						Type:      "chat_error",
						RequestID: in.RequestID,
						Error:     err.Error(),
					})
					return struct{}{}, err
				}

				model := strings.TrimSpace(in.Model)
				if model == "" {
					model = org.DefaultModel
				}

				resolvedHeaders, keySource, err := appruntime.Get().Services.ResolveExecutionHeaders(
					ctx,
					actor.OrgID,
					buildBrowserBYOKHeaders(in.BrowserKeys),
					in.KeySource,
					services.AccessCredentialSessionAuth,
				)
				if err != nil {
					dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
						Type:      "chat_error",
						RequestID: in.RequestID,
						Error:     err.Error(),
					})
					return struct{}{}, err
				}
				if keySource == services.KeySourcePlatformHosted {
					if err := appruntime.Get().Services.ReservePlatformHostedUsage(ctx, actor.OrgID, model); err != nil {
						dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
							Type:      "chat_error",
							RequestID: in.RequestID,
							Error:     err.Error(),
						})
						return struct{}{}, err
					}
				}

				if err := appruntime.Get().Services.UpdateConversationSettings(ctx, actor, props.Peek().ConversationID, model, keySource); err != nil {
					dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
						Type:      "chat_error",
						RequestID: in.RequestID,
						Error:     err.Error(),
					})
					return struct{}{}, err
				}

				switch {
				case in.EditMessageID != "":
					if err := appruntime.Get().Services.ReviseUserMessage(ctx, actor, props.Peek().ConversationID, in.EditMessageID, in.Message); err != nil {
						dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
							Type:      "chat_error",
							RequestID: in.RequestID,
							Error:     err.Error(),
						})
						return struct{}{}, err
					}
				case in.Regenerate:
					detail, err := appruntime.Get().Services.Conversation(ctx, actor.OrgID, props.Peek().ConversationID)
					if err != nil {
						dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
							Type:      "chat_error",
							RequestID: in.RequestID,
							Error:     err.Error(),
						})
						return struct{}{}, err
					}
					lastUserID := chatruntime.LastUserMessageID(detail.Messages)
					if lastUserID == "" {
						err := errors.New("conversation has no user message to regenerate from")
						dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
							Type:      "chat_error",
							RequestID: in.RequestID,
							Error:     err.Error(),
						})
						return struct{}{}, err
					}
					if err := appruntime.Get().Services.TruncateConversationAfter(ctx, actor, props.Peek().ConversationID, lastUserID); err != nil {
						dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
							Type:      "chat_error",
							RequestID: in.RequestID,
							Error:     err.Error(),
						})
						return struct{}{}, err
					}
				default:
					if _, err := appruntime.Get().Services.AddUserMessage(ctx, actor, props.Peek().ConversationID, in.Message, keySource, in.AttachmentIDs); err != nil {
						dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
							Type:      "chat_error",
							RequestID: in.RequestID,
							Error:     err.Error(),
						})
						return struct{}{}, err
					}
				}

				detail, err := appruntime.Get().Services.Conversation(ctx, actor.OrgID, props.Peek().ConversationID)
				if err != nil {
					dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
						Type:      "chat_error",
						RequestID: in.RequestID,
						Error:     err.Error(),
					})
					return struct{}{}, err
				}
				runReq, err := chatruntime.BuildConversationRunRequest(ctx, appruntime.Get().Services.BlobStore, detail, resolvedHeaders)
				if err != nil {
					dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
						Type:      "chat_error",
						RequestID: in.RequestID,
						Error:     err.Error(),
					})
					return struct{}{}, err
				}
				runReq.Request.Model = model

				dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
					Type:      "chat_started",
					RequestID: in.RequestID,
				})

				runCtx, cancel := context.WithCancel(ctx)
				setActiveRun(in.RequestID, cancel)
				defer clearActiveRun(in.RequestID)

				streamResult, err := chatruntime.StreamConversationRun(runCtx, appruntime.Get().Gateway, runReq, resolvedHeaders, func(event chatruntime.SSEEvent) {
					relayChatStreamEvent(pageCtx, in.HID, in.RequestID, event)
				})
				if err != nil {
					if errors.Is(err, context.Canceled) {
						dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
							Type:      "chat_stopped",
							RequestID: in.RequestID,
						})
						return struct{}{}, err
					}
					message := strings.TrimSpace(err.Error())
					var gatewayErr *chatruntime.GatewayError
					if errors.As(err, &gatewayErr) && strings.TrimSpace(gatewayErr.Message) != "" {
						message = strings.TrimSpace(gatewayErr.Message)
					}
					dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
						Type:      "chat_error",
						RequestID: in.RequestID,
						Error:     message,
					})
					return struct{}{}, err
				}
				if streamResult == nil || streamResult.Result == nil || streamResult.Result.Response == nil {
					message := "conversation completed without a response"
					if streamResult != nil && len(streamResult.Raw) > 0 {
						runErr, extractErr := chatruntime.ExtractRunErrorMessage(streamResult.Raw)
						if extractErr == nil && strings.TrimSpace(runErr) != "" {
							message = strings.TrimSpace(runErr)
						}
					}
					err := errors.New(message)
					dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
						Type:      "chat_error",
						RequestID: in.RequestID,
						Error:     message,
					})
					return struct{}{}, err
				}

				if _, err := appruntime.Get().Services.AddAssistantMessage(
					ctx,
					actor,
					props.Peek().ConversationID,
					streamResult.Result.Response.TextContent(),
					keySource,
					streamResult.Result.Usage,
					streamResult.Result.Steps,
				); err != nil {
					dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
						Type:      "chat_error",
						RequestID: in.RequestID,
						Error:     err.Error(),
					})
					return struct{}{}, err
				}
				if err := appruntime.Get().Services.RecordUsage(
					ctx,
					actor.OrgID,
					props.Peek().ConversationID,
					"",
					"chat_stream",
					model,
					keySource,
					services.AccessCredentialSessionAuth,
					streamResult.Result.Usage,
					map[string]any{"via": "chat_island"},
				); err != nil {
					appruntime.Get().Logger.Error("record usage failed", "error", err)
				}

				dispatchChatIsland(pageCtx, in.HID, chatServerMessage{
					Type:      "chat_complete",
					RequestID: in.RequestID,
					Assistant: buildChatAssistantPayload(streamResult.Result, keySource),
				})
				return struct{}{}, nil
			},
			vango.DropWhileRunning(),
			vango.ActionOnSuccess(func(any) {
				pageData.Refetch()
			}),
		)

		handleIslandMessage := func(msg vango.IslandMessage) {
			payload, err := vango.DecodeIslandPayload[chatIslandClientMessage](msg)
			if err != nil {
				appruntime.Get().Logger.Warn("chat island payload rejected", "hid", msg.HID, "error", err)
				return
			}

			switch strings.TrimSpace(payload.Type) {
			case "upload_intent":
				if !uploadIntent.Run(chatUploadIntentCommand{
					HID:         msg.HID,
					RequestID:   payload.RequestID,
					Filename:    payload.Filename,
					ContentType: payload.ContentType,
					SizeBytes:   payload.SizeBytes,
				}) {
					dispatchChatIsland(pageCtx, msg.HID, chatServerMessage{
						Type:      "upload_intent_error",
						RequestID: payload.RequestID,
						Error:     "upload request rejected while another upload is still being processed",
					})
				}
			case "upload_claim":
				if !uploadClaim.Run(chatUploadClaimCommand{
					HID:         msg.HID,
					RequestID:   payload.RequestID,
					Filename:    payload.Filename,
					ContentType: payload.ContentType,
					SizeBytes:   payload.SizeBytes,
					IntentToken: payload.IntentToken,
				}) {
					dispatchChatIsland(pageCtx, msg.HID, chatServerMessage{
						Type:      "upload_claim_error",
						RequestID: payload.RequestID,
						Error:     "upload claim rejected while another upload is still being processed",
					})
				}
			case "submit":
				requestedMode := services.KeySource(strings.TrimSpace(payload.KeySource))
				if requestedMode == "" {
					requestedMode = services.KeySourcePlatformHosted
				}
				if !submitChat.Run(chatSubmitCommand{
					HID:           msg.HID,
					RequestID:     payload.RequestID,
					Message:       payload.Message,
					Model:         payload.Model,
					KeySource:     requestedMode,
					AttachmentIDs: append([]string(nil), payload.AttachmentIDs...),
					Regenerate:    payload.Regenerate,
					EditMessageID: payload.EditMessageID,
					BrowserKeys:   payload.BrowserKeys,
				}) {
					dispatchChatIsland(pageCtx, msg.HID, chatServerMessage{
						Type:      "chat_error",
						RequestID: payload.RequestID,
						Error:     "a chat run is already in progress",
					})
				}
			case "stop":
				if !stopActiveRun(payload.RequestID) {
					dispatchChatIsland(pageCtx, msg.HID, chatServerMessage{
						Type:      "chat_stopped",
						RequestID: payload.RequestID,
					})
				}
			default:
				dispatchChatIsland(pageCtx, msg.HID, chatServerMessage{
					Type:      "chat_error",
					RequestID: payload.RequestID,
					Error:     "unsupported chat island event",
				})
			}
		}

		return func() *vango.VNode {
			actor := props.Get().Actor
			ctx := vango.UseCtx()
			return pageData.Match(
				vango.OnLoadingOrPending[*chatPageData](func() *vango.VNode {
					return AppShell(ctx, actor, LoadingPanel("Loading chat..."))
				}),
				vango.OnError[*chatPageData](func(err error) *vango.VNode {
					return AppShell(ctx, actor, PageErrorPanel(err))
				}),
				vango.OnReady(func(data *chatPageData) *vango.VNode {
					islandProps := map[string]any{
						"conversationId":        data.Detail.Conversation.ID,
						"model":                 data.Detail.Conversation.Model,
						"messages":              data.Messages,
						"modelOptions":          modelOptions(data.Detail.Conversation.Model),
						"allowBrowserBYOK":      data.Org.AllowBYOKOverride,
						"platformHostedEnabled": data.Org.HostedUsageEnabled,
						"hasWorkspaceProviders": len(data.ProviderSecrets) > 0,
						"initialKeySource":      string(data.Detail.Conversation.KeySource),
						"conversationTitle":     data.Detail.Conversation.Title,
						"providerHints":         providerHints(),
						"settingsKeysURL":       "/settings/keys",
						"settingsBillingURL":    "/settings/billing",
						"settingsAccessURL":     "/settings/access",
						"currentBalanceCents":   data.CurrentBalanceCents,
						"hostedModels":          hostedModelOptions(data.Detail.Conversation.Model),
					}

					return AppShell(ctx, actor,
						Div(
							Class("chat-page"),
							Sidebar(actor, data.Conversations, data.Detail.Conversation.ID, newConversation.IsRunning(), func() {
								newConversation.Run(struct{}{})
							}),
							Main(
								Class("chat-main"),
								Header(
									Class("chat-header"),
									Div(
										H1(Text(data.Detail.Conversation.Title)),
										P(Textf("Model: %s", data.Detail.Conversation.Model)),
									),
									Div(
										Class("chat-header-actions"),
										A(Href("/settings/keys"), Class("btn btn-secondary"), Text("Keys")),
										A(Href("/settings/billing"), Class("btn btn-secondary"), Text("Billing")),
									),
								),
								chatIslandBoundary(islandProps, handleIslandMessage),
							),
						),
					)
				}),
			)
		}
	})
}

func chatMessagesView(ctx context.Context, detail *services.ConversationDetail) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(detail.Messages))
	for _, msg := range detail.Messages {
		attachments := make([]map[string]any, 0, len(msg.Attachments))
		for _, att := range msg.Attachments {
			attachmentURL := ""
			if appruntime.Get().Services.BlobStore != nil {
				signed, err := appruntime.Get().Services.BlobStore.PresignGet(ctx, att.BlobRef.Key, 30*time.Minute)
				if err != nil {
					return nil, err
				}
				attachmentURL = signed
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
	return out, nil
}
