package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vango-go/vai-lite/pkg/core/types"
	chainrt "github.com/vango-go/vai-lite/pkg/gateway/chains"
)

type ManagedConversationDetail struct {
	Conversation       Conversation
	Session            *types.SessionRecord
	Chain              *types.ChainRecord
	History            []types.Message
	Runs               []types.ChainRunRecord
	LatestRun          *types.ChainRunRecord
	PreferredTransport string
}

type ManagedConversationSummary struct {
	ID                 string
	Title              string
	Model              string
	KeySource          KeySource
	UpdatedAt          time.Time
	PreferredTransport string
}

type ManagedAssetInfo struct {
	ID        string
	MediaType string
	SizeBytes int64
	URL       string
}

func (s *AppServices) ListManagedConversations(ctx context.Context, orgID string) ([]Conversation, error) {
	summaries, err := s.ListManagedConversationSummaries(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make([]Conversation, 0, len(summaries))
	for i := range summaries {
		out = append(out, Conversation{
			ID:        summaries[i].ID,
			Title:     summaries[i].Title,
			Model:     summaries[i].Model,
			KeySource: summaries[i].KeySource,
			UpdatedAt: summaries[i].UpdatedAt,
		})
	}
	return out, nil
}

func (s *AppServices) ListManagedConversationSummaries(ctx context.Context, orgID string) ([]ManagedConversationSummary, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("managed history store is not configured")
	}
	org, err := s.Org(ctx, orgID)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `
SELECT
	sess.external_session_id,
	sess.updated_at,
	COALESCE(chain.current_defaults_json, '{}'::jsonb) AS current_defaults_json,
	chain.updated_at,
	COALESCE(first_user.content_json, 'null'::jsonb) AS first_user_content_json,
	COALESCE(latest_run.model, ''),
	COALESCE(latest_run.metadata_json, '{}'::jsonb) AS latest_run_metadata_json,
	latest_run.started_at,
	latest_run.completed_at
FROM vai_sessions AS sess
LEFT JOIN LATERAL (
	SELECT
		c.id,
		c.current_defaults_json,
		c.updated_at,
		c.created_at
	FROM vai_chains AS c
	WHERE c.org_id = sess.org_id
	  AND (
		(COALESCE(sess.latest_chain_id, '') <> '' AND c.id = sess.latest_chain_id) OR
		(COALESCE(sess.latest_chain_id, '') = '' AND c.session_id = sess.id)
	  )
	ORDER BY
		CASE WHEN c.id = sess.latest_chain_id THEN 0 ELSE 1 END,
		c.updated_at DESC,
		c.created_at DESC
	LIMIT 1
) AS chain ON true
LEFT JOIN LATERAL (
	SELECT content_json
	FROM vai_chain_messages
	WHERE chain_id = chain.id
	  AND role = 'user'
	ORDER BY sequence_in_chain ASC, created_at ASC
	LIMIT 1
) AS first_user ON true
LEFT JOIN LATERAL (
	SELECT
		r.model,
		r.metadata_json,
		r.started_at,
		r.completed_at
	FROM vai_runs AS r
	WHERE r.chain_id = chain.id
	ORDER BY r.started_at DESC, r.completed_at DESC NULLS LAST
	LIMIT 1
) AS latest_run ON true
WHERE sess.org_id = $1
  AND COALESCE(sess.external_session_id, '') <> ''
ORDER BY sess.updated_at DESC, sess.created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ManagedConversationSummary, 0)
	for rows.Next() {
		var (
			externalSessionID string
			sessionUpdatedAt  time.Time
			defaultsRaw       []byte
			chainUpdatedAt    *time.Time
			firstUserRaw      []byte
			runModel          string
			runMetadataRaw    []byte
			runStartedAt      *time.Time
			runCompletedAt    *time.Time
		)
		if err := rows.Scan(
			&externalSessionID,
			&sessionUpdatedAt,
			&defaultsRaw,
			&chainUpdatedAt,
			&firstUserRaw,
			&runModel,
			&runMetadataRaw,
			&runStartedAt,
			&runCompletedAt,
		); err != nil {
			return nil, err
		}

		var defaults types.ChainDefaults
		if err := json.Unmarshal(defaultsRaw, &defaults); err != nil {
			return nil, fmt.Errorf("decode managed conversation defaults: %w", err)
		}
		runMetadata := map[string]any{}
		if err := json.Unmarshal(runMetadataRaw, &runMetadata); err != nil {
			return nil, fmt.Errorf("decode managed conversation run metadata: %w", err)
		}
		title := summaryTitleFromContentJSON(firstUserRaw, externalSessionID)

		var latestRun *types.ChainRunRecord
		if strings.TrimSpace(runModel) != "" || len(runMetadata) > 0 || runStartedAt != nil || runCompletedAt != nil {
			latestRun = &types.ChainRunRecord{
				Model:    strings.TrimSpace(runModel),
				Metadata: runMetadata,
			}
			if runStartedAt != nil {
				latestRun.StartedAt = runStartedAt.UTC()
			}
			if runCompletedAt != nil {
				completedAt := runCompletedAt.UTC()
				latestRun.CompletedAt = &completedAt
			}
		}

		chainTime := time.Time{}
		if chainUpdatedAt != nil {
			chainTime = chainUpdatedAt.UTC()
		}
		conversation, preferredTransport := managedConversationProjection(
			externalSessionID,
			title,
			org.DefaultModel,
			defaults.Model,
			sessionUpdatedAt.UTC(),
			chainTime,
			latestRun,
		)
		out = append(out, ManagedConversationSummary{
			ID:                 conversation.ID,
			Title:              conversation.Title,
			Model:              conversation.Model,
			KeySource:          conversation.KeySource,
			UpdatedAt:          conversation.UpdatedAt,
			PreferredTransport: preferredTransport,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (s *AppServices) ManagedConversation(ctx context.Context, orgID, externalSessionID string) (*ManagedConversationDetail, error) {
	store, err := s.managedChainStore()
	if err != nil {
		return nil, err
	}
	org, err := s.Org(ctx, orgID)
	if err != nil {
		return nil, err
	}
	externalSessionID = strings.TrimSpace(externalSessionID)
	if externalSessionID == "" {
		return nil, errors.New("external session id is required")
	}

	session, err := store.GetSessionByExternal(ctx, orgID, externalSessionID)
	if err != nil && !errors.Is(err, chainrt.ErrNotFound) {
		return nil, err
	}
	if session == nil {
		return &ManagedConversationDetail{
			Conversation: Conversation{
				ID:        externalSessionID,
				Title:     "New chat",
				Model:     org.DefaultModel,
				KeySource: KeySourcePlatformHosted,
				UpdatedAt: time.Now().UTC(),
			},
			History: nil,
		}, nil
	}

	chains, err := store.ListSessionChains(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	chain := latestConversationChain(session, chains)
	if chain == nil {
		return &ManagedConversationDetail{
			Conversation: Conversation{
				ID:        externalSessionID,
				Title:     "New chat",
				Model:     org.DefaultModel,
				KeySource: KeySourcePlatformHosted,
				UpdatedAt: session.UpdatedAt,
			},
			Session: session,
		}, nil
	}
	chainRecord, history, err := store.GetChain(ctx, chain.ID)
	if err != nil {
		return nil, err
	}
	runs, err := store.ListChainRuns(ctx, chain.ID)
	if err != nil {
		return nil, err
	}
	var latestRun *types.ChainRunRecord
	if len(runs) > 0 {
		sort.Slice(runs, func(i, j int) bool {
			return runs[i].StartedAt.After(runs[j].StartedAt)
		})
		latestRun = &runs[0]
	}
	conversation, preferredTransport := managedConversationProjection(
		externalSessionID,
		conversationTitleFromHistory(history, externalSessionID),
		org.DefaultModel,
		chainRecord.Defaults.Model,
		session.UpdatedAt,
		chainRecord.UpdatedAt,
		latestRun,
	)
	return &ManagedConversationDetail{
		Conversation:       conversation,
		Session:            session,
		Chain:              chainRecord,
		History:            history,
		Runs:               append([]types.ChainRunRecord(nil), runs...),
		LatestRun:          latestRun,
		PreferredTransport: preferredTransport,
	}, nil
}

func (s *AppServices) ManagedAsset(ctx context.Context, orgID, assetID string) (*ManagedAssetInfo, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("managed asset store is not configured")
	}
	var (
		objectKey string
		mediaType string
		sizeBytes int64
	)
	err := s.DB.QueryRow(ctx, `
SELECT object_key, media_type, size_bytes
FROM vai_assets
WHERE org_id = $1 AND id = $2`,
		orgID, assetID,
	).Scan(&objectKey, &mediaType, &sizeBytes)
	if err != nil {
		return nil, err
	}
	url := ""
	if s.BlobStore != nil && strings.TrimSpace(objectKey) != "" {
		signed, signErr := s.BlobStore.PresignGet(ctx, objectKey, 30*time.Minute)
		if signErr != nil {
			return nil, signErr
		}
		url = signed
	}
	return &ManagedAssetInfo{
		ID:        assetID,
		MediaType: mediaType,
		SizeBytes: sizeBytes,
		URL:       url,
	}, nil
}

func latestConversationChain(session *types.SessionRecord, chains []types.ChainRecord) *types.ChainRecord {
	if session != nil && strings.TrimSpace(session.LatestChainID) != "" {
		for i := range chains {
			if chains[i].ID == session.LatestChainID {
				return &chains[i]
			}
		}
	}
	if len(chains) == 0 {
		return nil
	}
	sort.Slice(chains, func(i, j int) bool {
		return chains[i].UpdatedAt.After(chains[j].UpdatedAt)
	})
	return &chains[0]
}

func conversationTitleFromHistory(history []types.Message, fallback string) string {
	for _, msg := range history {
		if msg.Role != "user" {
			continue
		}
		text := strings.TrimSpace(messageTextContent(msg))
		if text == "" {
			continue
		}
		runes := []rune(text)
		if len(runes) > 48 {
			return string(runes[:48]) + "…"
		}
		return text
	}
	if strings.TrimSpace(fallback) == "" {
		return "New chat"
	}
	return fallback
}

func messageTextContent(msg types.Message) string {
	switch value := msg.Content.(type) {
	case string:
		return strings.TrimSpace(value)
	default:
		var parts []string
		for _, block := range msg.ContentBlocks() {
			if text, ok := block.(types.TextBlock); ok {
				parts = append(parts, strings.TrimSpace(text.Text))
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
}

func managedConversationProjection(externalSessionID, title, orgDefaultModel, chainDefaultModel string, sessionUpdatedAt, chainUpdatedAt time.Time, latestRun *types.ChainRunRecord) (Conversation, string) {
	model := strings.TrimSpace(chainDefaultModel)
	if latestRun != nil && strings.TrimSpace(latestRun.Model) != "" {
		model = strings.TrimSpace(latestRun.Model)
	}
	if model == "" {
		model = strings.TrimSpace(orgDefaultModel)
	}
	keySource := KeySourcePlatformHosted
	preferredTransport := "sse"
	updatedAt := sessionUpdatedAt.UTC()
	if !chainUpdatedAt.IsZero() && chainUpdatedAt.After(updatedAt) {
		updatedAt = chainUpdatedAt.UTC()
	}
	if latestRun != nil {
		obs := metadataMap(latestRun.Metadata, "observability")
		if value := KeySource(metadataString(obs, "key_source")); strings.TrimSpace(string(value)) != "" {
			keySource = value
		}
		switch strings.TrimSpace(metadataString(obs, "transport")) {
		case "websocket":
			preferredTransport = "websocket"
		case "http":
			preferredTransport = "http"
		}
		if latestRun.CompletedAt != nil && latestRun.CompletedAt.After(updatedAt) {
			updatedAt = latestRun.CompletedAt.UTC()
		} else if !latestRun.StartedAt.IsZero() && latestRun.StartedAt.After(updatedAt) {
			updatedAt = latestRun.StartedAt.UTC()
		}
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = conversationTitleFromHistory(nil, externalSessionID)
	}
	return Conversation{
		ID:        externalSessionID,
		Title:     title,
		Model:     model,
		KeySource: keySource,
		UpdatedAt: updatedAt,
	}, preferredTransport
}

func summaryTitleFromContentJSON(raw []byte, fallback string) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return conversationTitleFromHistory(nil, fallback)
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		return conversationTitleFromHistory([]types.Message{{Role: "user", Content: text}}, fallback)
	}
	blocks, err := types.UnmarshalContentBlocks(trimmed)
	if err != nil {
		return conversationTitleFromHistory(nil, fallback)
	}
	return conversationTitleFromHistory([]types.Message{{Role: "user", Content: blocks}}, fallback)
}

func (s *AppServices) DebugManagedConversation(ctx context.Context, orgID, externalSessionID string) string {
	detail, err := s.ManagedConversation(ctx, orgID, externalSessionID)
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("conversation=%s chain=%s messages=%d", detail.Conversation.ID, chainID(detail.Chain), len(detail.History))
}

func chainID(record *types.ChainRecord) string {
	if record == nil {
		return ""
	}
	return record.ID
}
