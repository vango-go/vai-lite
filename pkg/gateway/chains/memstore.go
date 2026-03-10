package chains

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

type MemoryStore struct {
	mu sync.RWMutex

	sessions          map[string]*types.SessionRecord
	sessionByExternal map[string]string
	sessionChains     map[string][]string

	chains       map[string]*types.ChainRecord
	chainHistory map[string][]types.Message
	chainResume  map[string]string

	runs      map[string]*types.ChainRunRecord
	chainRuns map[string][]string

	runItems         map[string][]types.RunTimelineItem
	effectiveRequest map[string]*types.EffectiveRequestResponse

	attachments map[string]*AttachmentRecord
	idempotency map[string]*IdempotencyRecord
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions:          make(map[string]*types.SessionRecord),
		sessionByExternal: make(map[string]string),
		sessionChains:     make(map[string][]string),
		chains:            make(map[string]*types.ChainRecord),
		chainHistory:      make(map[string][]types.Message),
		chainResume:       make(map[string]string),
		runs:              make(map[string]*types.ChainRunRecord),
		chainRuns:         make(map[string][]string),
		runItems:          make(map[string][]types.RunTimelineItem),
		effectiveRequest:  make(map[string]*types.EffectiveRequestResponse),
		attachments:       make(map[string]*AttachmentRecord),
		idempotency:       make(map[string]*IdempotencyRecord),
	}
}

func (s *MemoryStore) UpsertSession(_ context.Context, session *types.SessionRecord) error {
	if session == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.ID] = cloneJSON(session)
	if session.ExternalSessionID != "" && session.OrgID != "" {
		s.sessionByExternal[sessionExternalKey(session.OrgID, session.ExternalSessionID)] = session.ID
	}
	return nil
}

func (s *MemoryStore) GetSession(_ context.Context, sessionID string) (*types.SessionRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneJSON(session), nil
}

func (s *MemoryStore) GetSessionByExternal(_ context.Context, orgID, externalSessionID string) (*types.SessionRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sessionID, ok := s.sessionByExternal[sessionExternalKey(orgID, externalSessionID)]
	if !ok {
		return nil, ErrNotFound
	}
	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneJSON(session), nil
}

func (s *MemoryStore) ListSessionChains(_ context.Context, sessionID string) ([]types.ChainRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := s.sessionChains[sessionID]
	out := make([]types.ChainRecord, 0, len(ids))
	for _, id := range ids {
		record, ok := s.chains[id]
		if !ok {
			continue
		}
		out = append(out, *cloneJSON(record))
	}
	return out, nil
}

func (s *MemoryStore) SaveChain(_ context.Context, chain *types.ChainRecord, history []types.Message) error {
	if chain == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chains[chain.ID] = cloneJSON(chain)
	s.chainHistory[chain.ID] = cloneMessages(history)
	if chain.SessionID != "" {
		ids := s.sessionChains[chain.SessionID]
		if !slices.Contains(ids, chain.ID) {
			s.sessionChains[chain.SessionID] = append(ids, chain.ID)
		}
		if session, ok := s.sessions[chain.SessionID]; ok {
			session.LatestChainID = chain.ID
			session.UpdatedAt = chain.UpdatedAt
		}
	}
	return nil
}

func (s *MemoryStore) UpdateChain(_ context.Context, chain *types.ChainRecord) error {
	if chain == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.chains[chain.ID]; !ok {
		return ErrNotFound
	}
	s.chains[chain.ID] = cloneJSON(chain)
	if chain.SessionID != "" {
		if session, ok := s.sessions[chain.SessionID]; ok {
			session.LatestChainID = chain.ID
			session.UpdatedAt = chain.UpdatedAt
		}
	}
	return nil
}

func (s *MemoryStore) GetChain(_ context.Context, chainID string) (*types.ChainRecord, []types.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	chain, ok := s.chains[chainID]
	if !ok {
		return nil, nil, ErrNotFound
	}
	return cloneJSON(chain), cloneMessages(s.chainHistory[chainID]), nil
}

func (s *MemoryStore) AppendChainMessages(_ context.Context, chainID, _ string, messages []types.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	chain, ok := s.chains[chainID]
	if !ok {
		return ErrNotFound
	}
	s.chainHistory[chainID] = append(s.chainHistory[chainID], cloneMessages(messages)...)
	chain.MessageCountCached = len(s.chainHistory[chainID])
	return nil
}

func (s *MemoryStore) SetChainResumeTokenHash(_ context.Context, chainID, resumeTokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.chains[chainID]; !ok {
		return ErrNotFound
	}
	s.chainResume[chainID] = resumeTokenHash
	return nil
}

func (s *MemoryStore) GetChainResumeTokenHash(_ context.Context, chainID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.chains[chainID]; !ok {
		return "", ErrNotFound
	}
	return s.chainResume[chainID], nil
}

func (s *MemoryStore) CreateRun(_ context.Context, run *types.ChainRunRecord) error {
	if run == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[run.ID] = cloneJSON(run)
	s.chainRuns[run.ChainID] = append(s.chainRuns[run.ChainID], run.ID)
	return nil
}

func (s *MemoryStore) UpdateRun(_ context.Context, run *types.ChainRunRecord) error {
	if run == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[run.ID]; !ok {
		return ErrNotFound
	}
	s.runs[run.ID] = cloneJSON(run)
	return nil
}

func (s *MemoryStore) GetRun(_ context.Context, runID string) (*types.ChainRunRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.runs[runID]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneJSON(run), nil
}

func (s *MemoryStore) ListChainRuns(_ context.Context, chainID string) ([]types.ChainRunRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := s.chainRuns[chainID]
	out := make([]types.ChainRunRecord, 0, len(ids))
	for _, id := range ids {
		run, ok := s.runs[id]
		if !ok {
			continue
		}
		out = append(out, *cloneJSON(run))
	}
	return out, nil
}

func (s *MemoryStore) AppendRunItems(_ context.Context, runID string, items []types.RunTimelineItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[runID]; !ok {
		return ErrNotFound
	}
	s.runItems[runID] = append(s.runItems[runID], cloneJSON(items)...)
	return nil
}

func (s *MemoryStore) GetRunTimeline(_ context.Context, runID string) ([]types.RunTimelineItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items, ok := s.runItems[runID]
	if !ok {
		if _, hasRun := s.runs[runID]; !hasRun {
			return nil, ErrNotFound
		}
		return nil, nil
	}
	return cloneJSON(items), nil
}

func (s *MemoryStore) SaveEffectiveRequest(_ context.Context, req *types.EffectiveRequestResponse) error {
	if req == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.effectiveRequest[req.RunID] = cloneJSON(req)
	return nil
}

func (s *MemoryStore) GetEffectiveRequest(_ context.Context, runID string) (*types.EffectiveRequestResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	req, ok := s.effectiveRequest[runID]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneJSON(req), nil
}

func (s *MemoryStore) SaveAttachment(_ context.Context, attachment *AttachmentRecord) error {
	if attachment == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attachments[attachment.ID] = cloneJSON(attachment)
	return nil
}

func (s *MemoryStore) CloseAttachment(_ context.Context, attachmentID, reason string, endedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	attachment, ok := s.attachments[attachmentID]
	if !ok {
		return ErrNotFound
	}
	attachment.CloseReason = reason
	attachment.EndedAt = &endedAt
	return nil
}

func (s *MemoryStore) SaveIdempotency(_ context.Context, record *IdempotencyRecord) error {
	if record == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idempotency[idempotencyMapKey(IdempotencyScope{
		OrgID:          record.OrgID,
		PrincipalID:    record.PrincipalID,
		ChainID:        record.ChainID,
		Operation:      record.Operation,
		IdempotencyKey: record.IdempotencyKey,
	})] = cloneJSON(record)
	return nil
}

func (s *MemoryStore) GetIdempotency(_ context.Context, scope IdempotencyScope) (*IdempotencyRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.idempotency[idempotencyMapKey(scope)]
	if !ok {
		return nil, ErrNotFound
	}
	if !record.ExpiresAt.IsZero() && time.Now().After(record.ExpiresAt) {
		return nil, ErrNotFound
	}
	return cloneJSON(record), nil
}

func cloneMessages(in []types.Message) []types.Message {
	if len(in) == 0 {
		return nil
	}
	out := make([]types.Message, len(in))
	for i := range in {
		out[i] = cloneJSON(in[i])
	}
	return out
}

func sessionExternalKey(orgID, externalSessionID string) string {
	return orgID + "::" + externalSessionID
}

func idempotencyMapKey(scope IdempotencyScope) string {
	return fmt.Sprintf("%s::%s::%s::%s::%s", scope.OrgID, scope.PrincipalID, scope.ChainID, scope.Operation, scope.IdempotencyKey)
}

func cloneJSON[T any](value T) T {
	var zero T
	encoded, err := json.Marshal(value)
	if err != nil {
		return zero
	}
	var out T
	if err := json.Unmarshal(encoded, &out); err != nil {
		return zero
	}
	return out
}
