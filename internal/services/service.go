package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stripe/stripe-go/v84"
	"github.com/vango-go/vai-lite/pkg/core/types"
	neon "github.com/vango-go/vango-neon"
	s3store "github.com/vango-go/vango-s3"
	wos "github.com/vango-go/vango-workos"
	wosvault "github.com/workos/workos-go/v6/pkg/vault"
)

type KeySource string

const (
	KeySourcePlatformHosted       KeySource = "platform_hosted"
	KeySourceCustomerBYOKBrowser  KeySource = "customer_byok_browser"
	KeySourceCustomerBYOKVault    KeySource = "customer_byok_vault"
	KeySourceCustomerBYOKExternal KeySource = "customer_byok_external"
)

type AccessCredential string

const (
	AccessCredentialSessionAuth   AccessCredential = "session_auth"
	AccessCredentialGatewayAPIKey AccessCredential = "gateway_api_key"
)

type UserIdentity struct {
	UserID string
	OrgID  string
	Email  string
	Name   string
}

type AppServices struct {
	DB            neon.DB
	BaseURL       string
	DefaultModel  string
	StripeClient  *stripe.Client
	StripeWebhook string
	StripeLive    *bool
	TopupOptions  []int64
	Vault         *wosvault.Client
	BlobStore     s3store.Store
	Pricing       *PricingCatalog
	vaultRead     func(ctx context.Context, name string) (string, error)
}

const (
	MinTopupAmountCents int64 = 100
	MaxTopupAmountCents int64 = 1_000_000
)

type Organization struct {
	ID                 string
	Name               string
	AllowBYOKOverride  bool
	HostedUsageEnabled bool
	DefaultModel       string
}

type Conversation struct {
	ID        string
	Title     string
	Model     string
	KeySource KeySource
	UpdatedAt time.Time
}

type Attachment struct {
	ID          string
	Filename    string
	ContentType string
	SizeBytes   int64
	BlobRef     s3store.BlobRef
}

type ConversationMessage struct {
	ID          string
	Role        string
	BodyText    string
	KeySource   KeySource
	UsageJSON   string
	ToolTrace   string
	Attachments []Attachment
	CreatedAt   time.Time
}

type ConversationDetail struct {
	Conversation Conversation
	Messages     []ConversationMessage
}

type APIKeyRecord struct {
	ID          string
	Name        string
	TokenPrefix string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	RevokedAt   *time.Time
}

type CreatedAPIKey struct {
	Record APIKeyRecord
	Token  string
}

type ProviderSecretRecord struct {
	ID         string
	Provider   string
	ObjectName string
	UpdatedAt  time.Time
}

type WalletEntry struct {
	ID          string
	Kind        string
	AmountCents int64
	Description string
	CreatedAt   time.Time
}

type UsageEntry struct {
	ID                  string
	Model               string
	KeySource           KeySource
	AccessCredential    AccessCredential
	Billable            bool
	InputTokens         int
	OutputTokens        int
	TotalTokens         int
	EstimatedCostCents  int64
	PricingSnapshotJSON string
	CreatedAt           time.Time
}

type TopupIntent struct {
	ID          string
	AmountCents int64
	Status      string
	CheckoutURL string
}

type UploadIntent struct {
	IntentToken string            `json:"intent_token"`
	UploadURL   string            `json:"upload_url"`
	ContentType string            `json:"content_type"`
	Headers     map[string]string `json:"headers,omitempty"`
	ExpiresAt   time.Time         `json:"expires_at"`
}

func New(db neon.DB, baseURL, defaultModel, stripeSecret, stripeWebhook string, stripeLive *bool, topupOptions []int64, vaultClient *wosvault.Client, blobStore s3store.Store) *AppServices {
	if len(topupOptions) == 0 {
		topupOptions = []int64{1000, 2500, 5000}
	}
	sort.Slice(topupOptions, func(i, j int) bool { return topupOptions[i] < topupOptions[j] })

	var stripeClient *stripe.Client
	if strings.TrimSpace(stripeSecret) != "" {
		stripeClient = stripe.NewClient(strings.TrimSpace(stripeSecret))
	}

	return &AppServices{
		DB:            db,
		BaseURL:       strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		DefaultModel:  defaultModelOr(defaultModel),
		StripeClient:  stripeClient,
		StripeWebhook: strings.TrimSpace(stripeWebhook),
		StripeLive:    stripeLive,
		TopupOptions:  topupOptions,
		Vault:         vaultClient,
		BlobStore:     blobStore,
		Pricing:       DefaultPricingCatalog(),
		vaultRead: func(ctx context.Context, name string) (string, error) {
			if vaultClient == nil {
				return "", errors.New("WorkOS Vault is not configured")
			}
			obj, err := vaultClient.ReadObjectByName(ctx, wosvault.ReadObjectByNameOpts{Name: name})
			if err != nil {
				return "", err
			}
			return obj.Value, nil
		},
	}
}

func defaultModelOr(model string) string {
	if strings.TrimSpace(model) == "" {
		return "oai-resp/gpt-5-mini"
	}
	return strings.TrimSpace(model)
}

func (s *AppServices) EnsureIdentity(ctx context.Context, identity *wos.Identity) (UserIdentity, error) {
	if identity == nil || strings.TrimSpace(identity.UserID) == "" {
		return UserIdentity{}, errors.New("missing authenticated identity")
	}
	orgID := strings.TrimSpace(identity.OrgID)
	if orgID == "" {
		orgID = "org_" + sanitizeID(identity.UserID)
	}
	name := strings.TrimSpace(identity.Name)
	if name == "" {
		name = strings.TrimSpace(identity.Email)
	}
	user := UserIdentity{
		UserID: identity.UserID,
		OrgID:  orgID,
		Email:  strings.TrimSpace(identity.Email),
		Name:   name,
	}

	if _, err := s.DB.Exec(ctx, `
INSERT INTO app_users (id, email, name)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE
SET email = EXCLUDED.email,
    name = EXCLUDED.name,
    updated_at = now()`,
		user.UserID, user.Email, user.Name,
	); err != nil {
		return UserIdentity{}, fmt.Errorf("upsert user: %w", err)
	}

	orgName := strings.TrimSpace(identity.OrgID)
	if orgName == "" {
		orgName = user.Name + "'s workspace"
	}
	if _, err := s.DB.Exec(ctx, `
INSERT INTO app_orgs (id, name)
VALUES ($1, $2)
ON CONFLICT (id) DO UPDATE
SET name = app_orgs.name,
    updated_at = now()`,
		user.OrgID, orgName,
	); err != nil {
		return UserIdentity{}, fmt.Errorf("upsert org: %w", err)
	}

	role := "member"
	if len(identity.Roles) > 0 {
		role = identity.Roles[0]
	}
	if role == "" {
		role = "member"
	}
	if _, err := s.DB.Exec(ctx, `
INSERT INTO app_memberships (org_id, user_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (org_id, user_id) DO UPDATE
SET role = EXCLUDED.role,
    updated_at = now()`,
		user.OrgID, user.UserID, role,
	); err != nil {
		return UserIdentity{}, fmt.Errorf("upsert membership: %w", err)
	}

	return user, nil
}

func (s *AppServices) Org(ctx context.Context, orgID string) (*Organization, error) {
	row := s.DB.QueryRow(ctx, `
SELECT id, name, allow_byok_override, hosted_usage_enabled, default_model
FROM app_orgs
WHERE id = $1`,
		orgID,
	)
	var org Organization
	if err := row.Scan(&org.ID, &org.Name, &org.AllowBYOKOverride, &org.HostedUsageEnabled, &org.DefaultModel); err != nil {
		return nil, err
	}
	return &org, nil
}

func (s *AppServices) ListConversations(ctx context.Context, orgID string) ([]Conversation, error) {
	rows, err := s.DB.Query(ctx, `
SELECT id, title, model, key_source, updated_at
FROM conversations
WHERE org_id = $1
ORDER BY updated_at DESC`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.Title, &c.Model, &c.KeySource, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *AppServices) CreateConversation(ctx context.Context, actor UserIdentity, title, model string, keySource KeySource) (*Conversation, error) {
	id := newID("conv")
	title = strings.TrimSpace(title)
	if title == "" {
		title = "New chat"
	}
	model = defaultModelOr(model)
	if keySource == "" {
		keySource = KeySourcePlatformHosted
	}
	if _, err := s.DB.Exec(ctx, `
INSERT INTO conversations (id, org_id, created_by, title, model, key_source)
VALUES ($1, $2, $3, $4, $5, $6)`,
		id, actor.OrgID, actor.UserID, title, model, string(keySource),
	); err != nil {
		return nil, err
	}
	return &Conversation{
		ID:        id,
		Title:     title,
		Model:     model,
		KeySource: keySource,
		UpdatedAt: time.Now(),
	}, nil
}

func (s *AppServices) Conversation(ctx context.Context, orgID, conversationID string) (*ConversationDetail, error) {
	row := s.DB.QueryRow(ctx, `
SELECT id, title, model, key_source, updated_at
FROM conversations
WHERE id = $1 AND org_id = $2`,
		conversationID, orgID,
	)
	var detail ConversationDetail
	if err := row.Scan(&detail.Conversation.ID, &detail.Conversation.Title, &detail.Conversation.Model, &detail.Conversation.KeySource, &detail.Conversation.UpdatedAt); err != nil {
		return nil, err
	}

	rows, err := s.DB.Query(ctx, `
SELECT id, role, body_text, key_source, usage::text, tool_trace::text, created_at
FROM conversation_messages
WHERE conversation_id = $1
ORDER BY created_at`,
		conversationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messageIDs := make([]string, 0, 16)
	byID := make(map[string]*ConversationMessage)
	for rows.Next() {
		var msg ConversationMessage
		if err := rows.Scan(&msg.ID, &msg.Role, &msg.BodyText, &msg.KeySource, &msg.UsageJSON, &msg.ToolTrace, &msg.CreatedAt); err != nil {
			return nil, err
		}
		detail.Messages = append(detail.Messages, msg)
		messageIDs = append(messageIDs, msg.ID)
		byID[msg.ID] = &detail.Messages[len(detail.Messages)-1]
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(messageIDs) == 0 {
		return &detail, nil
	}

	attachRows, err := s.DB.Query(ctx, `
SELECT id, message_id, filename, content_type, size_bytes, blob_ref::text
FROM attachments
WHERE conversation_id = $1
ORDER BY created_at`,
		conversationID,
	)
	if err != nil {
		return nil, err
	}
	defer attachRows.Close()
	for attachRows.Next() {
		var (
			id          string
			messageID   string
			filename    string
			contentType string
			sizeBytes   int64
			rawBlob     string
		)
		if err := attachRows.Scan(&id, &messageID, &filename, &contentType, &sizeBytes, &rawBlob); err != nil {
			return nil, err
		}
		msg := byID[messageID]
		if msg == nil {
			continue
		}
		var blob s3store.BlobRef
		if err := json.Unmarshal([]byte(rawBlob), &blob); err != nil {
			return nil, err
		}
		msg.Attachments = append(msg.Attachments, Attachment{
			ID:          id,
			Filename:    filename,
			ContentType: contentType,
			SizeBytes:   sizeBytes,
			BlobRef:     blob,
		})
	}
	return &detail, attachRows.Err()
}

func (s *AppServices) UpdateConversationSettings(ctx context.Context, actor UserIdentity, conversationID, model string, keySource KeySource) error {
	model = defaultModelOr(model)
	if keySource == "" {
		keySource = KeySourcePlatformHosted
	}
	tag, err := s.DB.Exec(ctx, `
UPDATE conversations
SET model = $1,
    key_source = $2,
    updated_at = now()
WHERE id = $3 AND org_id = $4`,
		model, string(keySource), conversationID, actor.OrgID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("conversation not found")
	}
	return nil
}

func (s *AppServices) AddUserMessage(ctx context.Context, actor UserIdentity, conversationID, body string, keySource KeySource, attachmentIDs []string) (*ConversationMessage, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, errors.New("message body is required")
	}
	if keySource == "" {
		keySource = KeySourcePlatformHosted
	}

	msg := &ConversationMessage{
		ID:        newID("msg"),
		Role:      "user",
		BodyText:  body,
		KeySource: keySource,
		CreatedAt: time.Now(),
	}

	title := messageTitle(body)
	err := neon.WithTx(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
INSERT INTO conversation_messages (id, conversation_id, org_id, role, body_text, key_source, created_by)
VALUES ($1, $2, $3, 'user', $4, $5, $6)`,
			msg.ID, conversationID, actor.OrgID, body, string(keySource), actor.UserID,
		); err != nil {
			return err
		}

		if len(attachmentIDs) > 0 {
			if _, err := tx.Exec(ctx, `
UPDATE attachments
SET message_id = $1
WHERE org_id = $2
  AND conversation_id = $3
  AND id = ANY($4)
  AND (message_id IS NULL OR message_id = $1)`,
				msg.ID, actor.OrgID, conversationID, attachmentIDs,
			); err != nil {
				return err
			}
		}

		if _, err := tx.Exec(ctx, `
UPDATE conversations
SET title = CASE
		WHEN title = '' OR title = 'New chat' THEN $1
		ELSE title
	END,
	key_source = $2,
	updated_at = now()
WHERE id = $3 AND org_id = $4`,
			title, string(keySource), conversationID, actor.OrgID,
		); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return msg, nil
}

func (s *AppServices) AddAssistantMessage(ctx context.Context, actor UserIdentity, conversationID, body string, keySource KeySource, usage types.Usage, toolTrace any) (*ConversationMessage, error) {
	if keySource == "" {
		keySource = KeySourcePlatformHosted
	}
	usageJSON, err := json.Marshal(usage)
	if err != nil {
		return nil, err
	}
	toolTraceJSON, err := json.Marshal(toolTrace)
	if err != nil {
		return nil, err
	}

	msg := &ConversationMessage{
		ID:        newID("msg"),
		Role:      "assistant",
		BodyText:  body,
		KeySource: keySource,
		UsageJSON: string(usageJSON),
		ToolTrace: string(toolTraceJSON),
		CreatedAt: time.Now(),
	}
	if body == "" {
		msg.BodyText = "(no text content)"
	}

	if _, err := s.DB.Exec(ctx, `
INSERT INTO conversation_messages (id, conversation_id, org_id, role, body_text, tool_trace, key_source, usage, created_by)
VALUES ($1, $2, $3, 'assistant', $4, $5::jsonb, $6, $7::jsonb, $8)`,
		msg.ID, conversationID, actor.OrgID, msg.BodyText, string(toolTraceJSON), string(keySource), string(usageJSON), actor.UserID,
	); err != nil {
		return nil, err
	}
	if _, err := s.DB.Exec(ctx, `
UPDATE conversations
SET key_source = $1,
    updated_at = now()
WHERE id = $2 AND org_id = $3`,
		string(keySource), conversationID, actor.OrgID,
	); err != nil {
		return nil, err
	}
	return msg, nil
}

func (s *AppServices) TruncateConversationAfter(ctx context.Context, actor UserIdentity, conversationID, messageID string) error {
	tag, err := s.DB.Exec(ctx, `
DELETE FROM conversation_messages
WHERE conversation_id = $1
  AND org_id = $2
  AND created_at > (
    SELECT created_at
    FROM conversation_messages
    WHERE id = $3 AND conversation_id = $1 AND org_id = $2
  )`,
		conversationID, actor.OrgID, messageID,
	)
	if err != nil {
		return err
	}
	if _, err := s.DB.Exec(ctx, `
UPDATE conversations
SET updated_at = now()
WHERE id = $1 AND org_id = $2`,
		conversationID, actor.OrgID,
	); err != nil {
		return err
	}
	_ = tag
	return nil
}

func (s *AppServices) ReviseUserMessage(ctx context.Context, actor UserIdentity, conversationID, messageID, nextBody string) error {
	nextBody = strings.TrimSpace(nextBody)
	if nextBody == "" {
		return errors.New("revised message body is required")
	}

	return neon.WithTx(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var prevBody string
		if err := tx.QueryRow(ctx, `
SELECT body_text
FROM conversation_messages
WHERE id = $1 AND conversation_id = $2 AND org_id = $3 AND role = 'user'`,
			messageID, conversationID, actor.OrgID,
		).Scan(&prevBody); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
INSERT INTO message_revisions (id, org_id, conversation_id, message_id, previous_body_text, created_by)
VALUES ($1, $2, $3, $4, $5, $6)`,
			newID("rev"), actor.OrgID, conversationID, messageID, prevBody, actor.UserID,
		); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
UPDATE conversation_messages
SET body_text = $1
WHERE id = $2 AND conversation_id = $3 AND org_id = $4`,
			nextBody, messageID, conversationID, actor.OrgID,
		); err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
DELETE FROM conversation_messages
WHERE conversation_id = $1
  AND org_id = $2
  AND created_at > (
    SELECT created_at
    FROM conversation_messages
    WHERE id = $3 AND conversation_id = $1 AND org_id = $2
  )`,
			conversationID, actor.OrgID, messageID,
		); err != nil {
			return err
		}

		_, err := tx.Exec(ctx, `
UPDATE conversations
SET title = CASE
		WHEN title = '' OR title = 'New chat' THEN $1
		ELSE title
	END,
	updated_at = now()
WHERE id = $2 AND org_id = $3`,
			messageTitle(nextBody), conversationID, actor.OrgID,
		)
		return err
	})
}

func (s *AppServices) ListAPIKeys(ctx context.Context, orgID string) ([]APIKeyRecord, error) {
	rows, err := s.DB.Query(ctx, `
SELECT id, name, token_prefix, created_at, last_used_at, revoked_at
FROM gateway_api_keys
WHERE org_id = $1
ORDER BY created_at DESC`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []APIKeyRecord
	for rows.Next() {
		var rec APIKeyRecord
		if err := rows.Scan(&rec.ID, &rec.Name, &rec.TokenPrefix, &rec.CreatedAt, &rec.LastUsedAt, &rec.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *AppServices) CreateAPIKey(ctx context.Context, actor UserIdentity, name string) (*CreatedAPIKey, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("API key name is required")
	}
	token := "vai_sk_" + randomHex(24)
	prefix := token[:min(len(token), 14)]
	hash := hashToken(token)
	id := newID("gak")

	if _, err := s.DB.Exec(ctx, `
INSERT INTO gateway_api_keys (id, org_id, name, token_hash, token_prefix, created_by)
VALUES ($1, $2, $3, $4, $5, $6)`,
		id, actor.OrgID, name, hash, prefix, actor.UserID,
	); err != nil {
		return nil, err
	}

	now := time.Now()
	return &CreatedAPIKey{
		Record: APIKeyRecord{
			ID:          id,
			Name:        name,
			TokenPrefix: prefix,
			CreatedAt:   now,
		},
		Token: token,
	}, nil
}

func (s *AppServices) RevokeAPIKey(ctx context.Context, actor UserIdentity, id string) error {
	tag, err := s.DB.Exec(ctx, `
UPDATE gateway_api_keys
SET revoked_at = now()
WHERE id = $1 AND org_id = $2 AND revoked_at IS NULL`,
		id, actor.OrgID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("gateway API key not found")
	}
	return nil
}

func (s *AppServices) ValidateAPIKey(ctx context.Context, token string) (*Organization, error) {
	hash := hashToken(token)
	row := s.DB.QueryRow(ctx, `
SELECT o.id, o.name, o.allow_byok_override, o.hosted_usage_enabled, o.default_model, g.id
FROM gateway_api_keys g
JOIN app_orgs o ON o.id = g.org_id
WHERE g.token_hash = $1 AND g.revoked_at IS NULL`,
		hash,
	)
	var org Organization
	var gatewayKeyID string
	if err := row.Scan(&org.ID, &org.Name, &org.AllowBYOKOverride, &org.HostedUsageEnabled, &org.DefaultModel, &gatewayKeyID); err != nil {
		return nil, err
	}
	_, _ = s.DB.Exec(ctx, `UPDATE gateway_api_keys SET last_used_at = now() WHERE id = $1`, gatewayKeyID)
	return &org, nil
}

func (s *AppServices) ListProviderSecrets(ctx context.Context, orgID string) ([]ProviderSecretRecord, error) {
	rows, err := s.DB.Query(ctx, `
SELECT id, provider, object_name, updated_at
FROM provider_secrets
WHERE org_id = $1 AND deleted_at IS NULL
ORDER BY provider`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ProviderSecretRecord
	for rows.Next() {
		var rec ProviderSecretRecord
		if err := rows.Scan(&rec.ID, &rec.Provider, &rec.ObjectName, &rec.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *AppServices) StoreProviderSecret(ctx context.Context, actor UserIdentity, provider, secretValue string) (*ProviderSecretRecord, error) {
	if s.Vault == nil {
		return nil, errors.New("WorkOS Vault is not configured")
	}
	provider = canonicalProvider(provider)
	if provider == "" {
		return nil, errors.New("provider is required")
	}
	secretValue = strings.TrimSpace(secretValue)
	if secretValue == "" {
		return nil, errors.New("secret value is required")
	}

	name := vaultObjectName(actor.OrgID, provider)
	obj, err := s.Vault.ReadObjectByName(ctx, wosvault.ReadObjectByNameOpts{Name: name})
	switch {
	case err == nil:
		obj, err = s.Vault.UpdateObject(ctx, wosvault.UpdateObjectOpts{Id: obj.Id, Value: secretValue})
		if err != nil {
			return nil, fmt.Errorf("update vault object: %w", err)
		}
		if _, err := s.DB.Exec(ctx, `
INSERT INTO provider_secrets (id, org_id, provider, object_name, vault_object_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (org_id, provider) DO UPDATE
SET object_name = EXCLUDED.object_name,
    vault_object_id = EXCLUDED.vault_object_id,
    updated_at = now(),
    deleted_at = NULL`,
			newID("psec"), actor.OrgID, provider, name, obj.Id, actor.UserID,
		); err != nil {
			return nil, err
		}
	case strings.Contains(strings.ToLower(err.Error()), "not found"):
		meta, createErr := s.Vault.CreateObject(ctx, wosvault.CreateObjectOpts{
			Name:  name,
			Value: secretValue,
			KeyContext: wosvault.KeyContext{
				"organization_id": actor.OrgID,
				"provider":        provider,
			},
		})
		if createErr != nil {
			return nil, fmt.Errorf("create vault object: %w", createErr)
		}
		if _, err := s.DB.Exec(ctx, `
INSERT INTO provider_secrets (id, org_id, provider, object_name, vault_object_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (org_id, provider) DO UPDATE
SET object_name = EXCLUDED.object_name,
    vault_object_id = EXCLUDED.vault_object_id,
    updated_at = now(),
    deleted_at = NULL`,
			newID("psec"), actor.OrgID, provider, name, meta.Id, actor.UserID,
		); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("read vault object: %w", err)
	}

	row := s.DB.QueryRow(ctx, `
SELECT id, provider, object_name, updated_at
FROM provider_secrets
WHERE org_id = $1 AND provider = $2`,
		actor.OrgID, provider,
	)
	var rec ProviderSecretRecord
	if err := row.Scan(&rec.ID, &rec.Provider, &rec.ObjectName, &rec.UpdatedAt); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *AppServices) DeleteProviderSecret(ctx context.Context, actor UserIdentity, provider string) error {
	if s.Vault == nil {
		return errors.New("WorkOS Vault is not configured")
	}
	provider = canonicalProvider(provider)
	row := s.DB.QueryRow(ctx, `
SELECT id, object_name, vault_object_id
FROM provider_secrets
WHERE org_id = $1 AND provider = $2 AND deleted_at IS NULL`,
		actor.OrgID, provider,
	)
	var id, objectName, vaultObjectID string
	if err := row.Scan(&id, &objectName, &vaultObjectID); err != nil {
		return err
	}
	if _, err := s.Vault.DeleteObject(ctx, wosvault.DeleteObjectOpts{Id: vaultObjectID}); err != nil {
		return fmt.Errorf("delete vault object: %w", err)
	}
	_, err := s.DB.Exec(ctx, `
UPDATE provider_secrets
SET deleted_at = now(), updated_at = now()
WHERE id = $1`,
		id,
	)
	return err
}

func (s *AppServices) ResolveExecutionHeaders(ctx context.Context, orgID string, requestHeaders http.Header, requestedMode KeySource, accessCredential AccessCredential) (http.Header, KeySource, error) {
	headers := cloneHeaders(requestHeaders)
	org, err := s.Org(ctx, orgID)
	if err != nil {
		return nil, "", err
	}

	switch accessCredential {
	case AccessCredentialGatewayAPIKey:
		if hasProviderKeyHeader(headers) {
			return headers, KeySourceCustomerBYOKExternal, nil
		}
		if !org.HostedUsageEnabled {
			return nil, "", errors.New("VAI-hosted access is disabled for this workspace")
		}
		return stripProviderKeyHeaders(headers), KeySourcePlatformHosted, nil
	case AccessCredentialSessionAuth, "":
		switch requestedMode {
		case KeySourceCustomerBYOKBrowser:
			if !org.AllowBYOKOverride {
				return nil, "", errors.New("browser BYOK is disabled for this workspace")
			}
			if !hasProviderKeyHeader(headers) {
				return nil, "", errors.New("no browser BYOK header provided")
			}
			return headers, KeySourceCustomerBYOKBrowser, nil
		case KeySourceCustomerBYOKVault:
			headers = stripProviderKeyHeaders(headers)
			applied, err := s.injectWorkspaceProviderHeaders(ctx, orgID, headers)
			if err != nil {
				return nil, "", err
			}
			if !applied {
				return nil, "", errors.New("no workspace provider keys stored")
			}
			return headers, KeySourceCustomerBYOKVault, nil
		case "", KeySourcePlatformHosted:
			if !org.HostedUsageEnabled {
				return nil, "", errors.New("VAI-hosted access is disabled for this workspace")
			}
			return stripProviderKeyHeaders(headers), KeySourcePlatformHosted, nil
		default:
			return nil, "", fmt.Errorf("unsupported execution mode %q", requestedMode)
		}
	default:
		return nil, "", fmt.Errorf("unsupported access credential %q", accessCredential)
	}
}

func (s *AppServices) injectWorkspaceProviderHeaders(ctx context.Context, orgID string, headers http.Header) (bool, error) {
	if s.vaultRead == nil {
		return false, errors.New("WorkOS Vault is not configured")
	}
	rows, err := s.DB.Query(ctx, `
SELECT provider, object_name
FROM provider_secrets
WHERE org_id = $1 AND deleted_at IS NULL`,
		orgID,
	)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	applied := false
	for rows.Next() {
		var provider, objectName string
		if err := rows.Scan(&provider, &objectName); err != nil {
			return false, err
		}
		headerName := providerHeader(provider)
		if headerName == "" {
			continue
		}
		value, err := s.vaultRead(ctx, objectName)
		if err != nil {
			return false, fmt.Errorf("read workspace provider secret for %s: %w", provider, err)
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		headers.Set(headerName, value)
		applied = true
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return applied, nil
}

func (s *AppServices) CurrentBalance(ctx context.Context, orgID string) (int64, error) {
	var balance int64
	if err := s.DB.QueryRow(ctx, `
SELECT COALESCE(SUM(amount_cents), 0)
FROM wallet_ledger
WHERE org_id = $1`,
		orgID,
	).Scan(&balance); err != nil {
		return 0, err
	}
	return balance, nil
}

func (s *AppServices) Ledger(ctx context.Context, orgID string) ([]WalletEntry, error) {
	rows, err := s.DB.Query(ctx, `
SELECT id, kind, amount_cents, description, created_at
FROM wallet_ledger
WHERE org_id = $1
ORDER BY created_at DESC
LIMIT 50`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WalletEntry
	for rows.Next() {
		var entry WalletEntry
		if err := rows.Scan(&entry.ID, &entry.Kind, &entry.AmountCents, &entry.Description, &entry.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (s *AppServices) Usage(ctx context.Context, orgID string) ([]UsageEntry, error) {
	rows, err := s.DB.Query(ctx, `
SELECT id, model, key_source, access_credential, billable, input_tokens, output_tokens, total_tokens, estimated_cost_cents, pricing_snapshot::text, created_at
FROM usage_events
WHERE org_id = $1
ORDER BY created_at DESC
LIMIT 100`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UsageEntry
	for rows.Next() {
		var entry UsageEntry
		if err := rows.Scan(&entry.ID, &entry.Model, &entry.KeySource, &entry.AccessCredential, &entry.Billable, &entry.InputTokens, &entry.OutputTokens, &entry.TotalTokens, &entry.EstimatedCostCents, &entry.PricingSnapshotJSON, &entry.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (s *AppServices) ReservePlatformHostedUsage(ctx context.Context, orgID, model string) error {
	estimate, err := s.Pricing.Estimate(model, types.Usage{InputTokens: 1, TotalTokens: 1}, PricingContext{})
	if err != nil {
		return err
	}
	balance, err := s.CurrentBalance(ctx, orgID)
	if err != nil {
		return err
	}
	if balance < estimate.Snapshot.MinimumChargeCents {
		return errors.New("VAI credits depleted")
	}
	return nil
}

func (s *AppServices) RecordUsage(ctx context.Context, orgID, conversationID, messageID, requestKind, model string, keySource KeySource, accessCredential AccessCredential, usage types.Usage, metadata map[string]any) error {
	provider := "unknown"
	if idx := strings.Index(model, "/"); idx > 0 {
		provider = model[:idx]
	}

	billable := keySource == KeySourcePlatformHosted
	estimatedCost := int64(0)
	version := ""
	pricingSnapshotJSON := "{}"
	if billable {
		estimate, err := s.Pricing.Estimate(model, usage, PricingContextFromMetadata(metadata))
		if err != nil {
			return err
		}
		estimatedCost = estimate.BilledCents
		version = estimate.Snapshot.CatalogVersion
		pricingSnapshotJSON = estimate.Snapshot.JSON()
	} else if s.Pricing != nil {
		version = s.Pricing.Version
	}
	metaJSON, _ := json.Marshal(metadata)
	usageID := newID("usage")

	return neon.WithTx(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
INSERT INTO usage_events (id, org_id, conversation_id, message_id, request_kind, key_source, access_credential, provider, model, billable, input_tokens, output_tokens, total_tokens, estimated_cost_cents, pricing_version, pricing_snapshot, metadata)
VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16::jsonb, $17::jsonb)`,
			usageID, orgID, conversationID, messageID, requestKind, string(keySource), string(accessCredential), provider, model, billable, usage.InputTokens, usage.OutputTokens, usage.TotalTokens, estimatedCost, version, pricingSnapshotJSON, string(metaJSON),
		); err != nil {
			return err
		}
		if !billable || estimatedCost <= 0 {
			return nil
		}
		_, err := tx.Exec(ctx, `
INSERT INTO wallet_ledger (id, org_id, kind, amount_cents, currency, description, external_ref, metadata)
VALUES ($1, $2, 'debit', $3, 'usd', $4, $5, $6::jsonb)`,
			newID("wlt"), orgID, -estimatedCost, fmt.Sprintf("VAI-hosted usage for %s", model), usageID, string(metaJSON),
		)
		return err
	})
}

func (s *AppServices) CreateTopupCheckout(ctx context.Context, actor UserIdentity, amountCents int64) (*TopupIntent, error) {
	if s.StripeClient == nil {
		return nil, errors.New("Stripe is not configured")
	}
	if err := validateTopupAmount(amountCents); err != nil {
		return nil, err
	}
	topupID := newID("topup")
	successURL := s.BaseURL + "/settings/billing?topup=success"
	cancelURL := s.BaseURL + "/settings/billing?topup=cancel"
	params := &stripe.CheckoutSessionCreateParams{
		Mode:       stripe.String(string(stripe.CheckoutSessionModePayment)),
		SuccessURL: stripe.String(successURL),
		CancelURL:  stripe.String(cancelURL),
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{
			{
				Quantity: stripe.Int64(1),
				PriceData: &stripe.CheckoutSessionCreateLineItemPriceDataParams{
					Currency:   stripe.String(string(stripe.CurrencyUSD)),
					UnitAmount: stripe.Int64(amountCents),
					ProductData: &stripe.CheckoutSessionCreateLineItemPriceDataProductDataParams{
						Name: stripe.String(fmt.Sprintf("$%.2f wallet top-up", float64(amountCents)/100)),
					},
				},
			},
		},
		Metadata: map[string]string{
			"topup_id": topupID,
			"org_id":   actor.OrgID,
			"user_id":  actor.UserID,
		},
	}

	session, err := s.StripeClient.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		return nil, err
	}
	if _, err := s.DB.Exec(ctx, `
INSERT INTO stripe_topups (id, org_id, created_by, amount_cents, currency, checkout_session_id, status, metadata)
VALUES ($1, $2, $3, $4, 'usd', $5, 'pending', $6::jsonb)`,
		topupID, actor.OrgID, actor.UserID, amountCents, session.ID, `{"source":"stripe_checkout"}`,
	); err != nil {
		return nil, err
	}
	return &TopupIntent{
		ID:          topupID,
		AmountCents: amountCents,
		Status:      "pending",
		CheckoutURL: session.URL,
	}, nil
}

func (s *AppServices) ApplyStripeCheckoutCompleted(ctx context.Context, eventID, sessionID, topupID, orgID string, amountCents int64) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("stripe checkout session id is required")
	}
	if strings.TrimSpace(topupID) == "" || strings.TrimSpace(orgID) == "" {
		row := s.DB.QueryRow(ctx, `
SELECT id, org_id, amount_cents
FROM stripe_topups
WHERE checkout_session_id = $1`,
			sessionID,
		)
		var dbTopupID string
		var dbOrgID string
		var dbAmountCents int64
		if err := row.Scan(&dbTopupID, &dbOrgID, &dbAmountCents); err != nil {
			return fmt.Errorf("resolve stripe top-up by session: %w", err)
		}
		if strings.TrimSpace(topupID) == "" {
			topupID = dbTopupID
		}
		if strings.TrimSpace(orgID) == "" {
			orgID = dbOrgID
		}
		if amountCents <= 0 {
			amountCents = dbAmountCents
		}
	}

	return neon.WithTx(ctx, s.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
INSERT INTO processed_stripe_events (event_id, event_type)
VALUES ($1, 'checkout.session.completed')
ON CONFLICT (event_id) DO NOTHING`,
			eventID,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		updateTag, err := tx.Exec(ctx, `
UPDATE stripe_topups
SET status = 'completed', completed_at = now()
WHERE id = $1 AND org_id = $2`,
			topupID, orgID,
		)
		if err != nil {
			return err
		}
		if updateTag.RowsAffected() == 0 {
			return errors.New("stripe top-up record not found for completion")
		}
		_, err = tx.Exec(ctx, `
INSERT INTO wallet_ledger (id, org_id, kind, amount_cents, currency, description, external_ref, metadata)
VALUES ($1, $2, 'topup', $3, 'usd', $4, $5, $6::jsonb)
ON CONFLICT (org_id, external_ref) WHERE external_ref IS NOT NULL DO NOTHING`,
			newID("wlt"), orgID, amountCents, fmt.Sprintf("Stripe top-up via %s", sessionID), sessionID, fmt.Sprintf(`{"topup_id":%q}`, topupID),
		)
		return err
	})
}

func (s *AppServices) CreateImageUploadIntent(ctx context.Context, actor UserIdentity, filename, contentType string, size int64) (*UploadIntent, error) {
	if s.BlobStore == nil {
		return nil, errors.New("blob storage is not configured")
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "image/") {
		return nil, errors.New("only image uploads are supported in the beta chat UI")
	}
	prefix, err := s3store.Prefix(actor.OrgID)
	if err != nil {
		return nil, err
	}
	intent, err := s.BlobStore.CreateUploadIntent(ctx, s3store.IntentOptions{
		TenantPrefix: prefix,
		Filename:     filename,
		ContentType:  contentType,
		Size:         size,
	})
	if err != nil {
		return nil, err
	}
	return &UploadIntent{
		IntentToken: intent.Token,
		UploadURL:   intent.PresignedURL,
		ContentType: intent.BoundContentType,
		Headers:     intent.RequiredHeaders,
		ExpiresAt:   intent.ExpiresAt,
	}, nil
}

func (s *AppServices) ClaimImageAttachment(ctx context.Context, actor UserIdentity, conversationID, messageID, filename, contentType string, size int64, intentToken string) (*Attachment, error) {
	if s.BlobStore == nil {
		return nil, errors.New("blob storage is not configured")
	}
	prefix, err := s3store.Prefix(actor.OrgID)
	if err != nil {
		return nil, err
	}
	finalKey, err := s3store.FinalKeyChecked(prefix, "chat-images")
	if err != nil {
		return nil, err
	}
	ref, err := s.BlobStore.ClaimUpload(ctx, intentToken, prefix, finalKey)
	if err != nil {
		return nil, err
	}

	rawBlob, err := json.Marshal(ref)
	if err != nil {
		return nil, err
	}
	id := newID("att")
	if _, err := s.DB.Exec(ctx, `
INSERT INTO attachments (id, org_id, conversation_id, message_id, filename, content_type, size_bytes, blob_ref, created_by)
VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8::jsonb, $9)`,
		id, actor.OrgID, conversationID, messageID, filename, contentType, size, string(rawBlob), actor.UserID,
	); err != nil {
		return nil, err
	}
	return &Attachment{
		ID:          id,
		Filename:    filename,
		ContentType: contentType,
		SizeBytes:   size,
		BlobRef:     *ref,
	}, nil
}

func canonicalProvider(provider string) string {
	switch strings.TrimSpace(strings.ToLower(provider)) {
	case "anthropic":
		return "anthropic"
	case "openai", "oai-resp", "oai_resp":
		return "openai"
	case "gem-dev", "gemini", "gem_dev":
		return "gem-dev"
	case "gem-vert", "vertexai", "gem_vert":
		return "gem-vert"
	case "groq":
		return "groq"
	case "cerebras":
		return "cerebras"
	case "openrouter":
		return "openrouter"
	case "cartesia":
		return "cartesia"
	case "elevenlabs":
		return "elevenlabs"
	case "tavily":
		return "tavily"
	case "exa":
		return "exa"
	case "firecrawl":
		return "firecrawl"
	default:
		return ""
	}
}

func providerHeader(provider string) string {
	switch canonicalProvider(provider) {
	case "anthropic":
		return "X-Provider-Key-Anthropic"
	case "openai":
		return "X-Provider-Key-OpenAI"
	case "gem-dev":
		return "X-Provider-Key-Gemini"
	case "gem-vert":
		return "X-Provider-Key-VertexAI"
	case "groq":
		return "X-Provider-Key-Groq"
	case "cerebras":
		return "X-Provider-Key-Cerebras"
	case "openrouter":
		return "X-Provider-Key-OpenRouter"
	case "cartesia":
		return "X-Provider-Key-Cartesia"
	case "elevenlabs":
		return "X-Provider-Key-ElevenLabs"
	case "tavily":
		return "X-Provider-Key-Tavily"
	case "exa":
		return "X-Provider-Key-Exa"
	case "firecrawl":
		return "X-Provider-Key-Firecrawl"
	default:
		return ""
	}
}

func vaultObjectName(orgID, provider string) string {
	return fmt.Sprintf("vai-lite/%s/%s", sanitizeID(orgID), canonicalProvider(provider))
}

func messageTitle(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return "New chat"
	}
	body = strings.ReplaceAll(body, "\n", " ")
	body = strings.Join(strings.Fields(body), " ")
	if len(body) > 72 {
		return body[:69] + "..."
	}
	return body
}

func cloneHeaders(in http.Header) http.Header {
	out := make(http.Header, len(in))
	for k, vals := range in {
		out[k] = append([]string(nil), vals...)
	}
	return out
}

func hasProviderKeyHeader(headers http.Header) bool {
	for key := range headers {
		if strings.HasPrefix(http.CanonicalHeaderKey(key), "X-Provider-Key-") {
			return true
		}
	}
	return false
}

func stripProviderKeyHeaders(headers http.Header) http.Header {
	for key := range headers {
		if strings.HasPrefix(http.CanonicalHeaderKey(key), "X-Provider-Key-") {
			headers.Del(key)
		}
	}
	return headers
}

func validateTopupAmount(amountCents int64) error {
	switch {
	case amountCents < MinTopupAmountCents:
		return fmt.Errorf("top-up amount must be at least %d cents", MinTopupAmountCents)
	case amountCents > MaxTopupAmountCents:
		return fmt.Errorf("top-up amount must be at most %d cents", MaxTopupAmountCents)
	default:
		return nil
	}
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newID(prefix string) string {
	return prefix + "_" + randomHex(12)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

func sanitizeID(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, ":", "_")
	s = strings.ReplaceAll(s, " ", "_")
	if s == "" {
		return "unknown"
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
