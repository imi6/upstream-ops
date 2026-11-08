package channel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/connector"
	"github.com/bejix/upstream-ops/backend/storage"
)

type apiKeyGroupBinding struct {
	APIKeyID  int64
	GroupID   *int64
	GroupName string
}

type apiKeyGroupCandidate struct {
	binding apiKeyGroupBinding
}

// APIKeyFingerprint 用于外部系统在本地匹配其已有的渠道 Key，不携带密钥原文。
type APIKeyFingerprint struct {
	APIKeyID    int64
	Fingerprint string
	GroupID     *int64
	GroupName   string
}

func (f APIKeyFingerprint) ContainsSnapshot(snap storage.RateSnapshot) bool {
	if f.GroupID != nil && *f.GroupID > 0 {
		return snap.RemoteGroupID != nil && *snap.RemoteGroupID == *f.GroupID
	}
	return f.GroupName != "" && snap.ModelName == f.GroupName
}

type APIKeyFingerprintExport struct {
	ChannelSource     string
	Keys              []APIKeyFingerprint
	UnavailableKeyIDs []int64
	Groups            *APIKeyGroupSet
	SyncedAt          *time.Time
	AttemptedAt       *time.Time
	SyncStatus        string
	SyncError         string
}

// APIKeyFingerprintNormalization 声明外部系统计算 SHA-256 指纹前应采用的规则。
func APIKeyFingerprintNormalization(channelType storage.ChannelType) string {
	if channelType == storage.ChannelTypeNewAPI {
		return "trim_strip_sk_prefix"
	}
	return "trim"
}

func normalizeFingerprintAPIKey(channelType storage.ChannelType, key string) string {
	key = strings.TrimSpace(key)
	if channelType == storage.ChannelTypeNewAPI {
		key = strings.TrimPrefix(key, "sk-")
	}
	return key
}

func maskedAPIKey(key string) bool {
	return strings.ContainsAny(key, "*…") || strings.Contains(key, "...")
}

func (s *APIKeyGroupSet) unavailableAPIKey(id int64) {
	if s.unavailableKeyIDs == nil {
		s.unavailableKeyIDs = make(map[int64]struct{})
	}
	s.unavailableKeyIDs[id] = struct{}{}
}

func (s *APIKeyGroupSet) rememberAPIKey(fingerprint [32]byte, binding apiKeyGroupBinding) {
	if previous, exists := s.keyFingerprints[fingerprint]; exists && previous.APIKeyID != binding.APIKeyID {
		// 归一化后的指纹不唯一时不下发该绑定，防止外部系统关联到错误分组。
		s.unavailableAPIKey(binding.APIKeyID)
		if previous.APIKeyID > 0 {
			s.unavailableAPIKey(previous.APIKeyID)
		}
		s.keyFingerprints[fingerprint] = apiKeyGroupBinding{}
		return
	}
	s.keyFingerprints[fingerprint] = binding
}

func (s *APIKeyGroupSet) indexAPIKey(key connector.APIKey) {
	if key.ID <= 0 {
		s.unavailableAPIKey(key.ID)
		return
	}
	binding := apiKeyGroupBinding{
		APIKeyID:  key.ID,
		GroupID:   key.GroupID,
		GroupName: strings.TrimSpace(key.Group),
	}
	if s.channelType == storage.ChannelTypeSub2API || binding.GroupName == "" {
		binding.GroupName = strings.TrimSpace(key.GroupName)
	}
	s.bindings[key.ID] = binding
	s.keyHintHashes[key.ID] = apiKeyHintHash(s.channelType, key.Key)
	if (binding.GroupID == nil || *binding.GroupID <= 0) && binding.GroupName == "" {
		s.unavailableAPIKey(key.ID)
		return
	}
	value := normalizeFingerprintAPIKey(s.channelType, key.Key)
	if value != "" && !maskedAPIKey(value) {
		s.rememberAPIKey(sha256.Sum256([]byte(value)), binding)
		return
	}
	s.maskedKeys = append(s.maskedKeys, apiKeyGroupCandidate{binding: binding})
}

// ExportAPIKeyFingerprints 仅导出已持久化的快照，普通外部请求绝不访问上游。
func (s *Service) ExportAPIKeyFingerprints(ctx context.Context, channelID uint) (*APIKeyFingerprintExport, error) {
	if s.APIKeySnapshots == nil {
		return nil, errors.New("密钥快照存储不可用")
	}
	state, err := s.APIKeySnapshots.Load(ctx, channelID)
	if err != nil {
		return nil, err
	}
	result := &APIKeyFingerprintExport{
		ChannelSource: state.ChannelSource,
		Keys:          []APIKeyFingerprint{}, UnavailableKeyIDs: []int64{},
		Groups:   &APIKeyGroupSet{IDs: map[int64]struct{}{}, Names: map[string]struct{}{}},
		SyncedAt: state.SyncedAt, AttemptedAt: state.AttemptedAt,
		SyncStatus: "ready", SyncError: state.SyncError,
	}
	if state.SyncedAt == nil {
		result.SyncStatus = "not_synced"
		if state.SyncError != "" {
			result.SyncStatus = "failed"
		}
	} else if !state.Complete {
		result.SyncStatus = "partial"
		if state.AttemptedAt != nil && state.AttemptedAt.After(*state.SyncedAt) {
			result.SyncStatus = "failed"
		}
	}
	counts := make(map[string]int, len(state.Keys))
	for _, key := range state.Keys {
		if key.Fingerprint != "" {
			counts[key.Fingerprint]++
		}
	}
	for _, key := range state.Keys {
		if key.GroupID != nil && *key.GroupID > 0 {
			result.Groups.IDs[*key.GroupID] = struct{}{}
		}
		if key.GroupName != "" {
			result.Groups.Names[key.GroupName] = struct{}{}
		}
		if key.Fingerprint == "" || key.FingerprintStale || counts[key.Fingerprint] > 1 {
			result.UnavailableKeyIDs = append(result.UnavailableKeyIDs, key.APIKeyID)
		}
		if key.Fingerprint != "" && counts[key.Fingerprint] == 1 {
			result.Keys = append(result.Keys, APIKeyFingerprint{APIKeyID: key.APIKeyID, Fingerprint: key.Fingerprint, GroupID: key.GroupID, GroupName: key.GroupName})
		}
	}
	if len(result.UnavailableKeyIDs) > 0 && result.SyncStatus == "ready" {
		result.SyncStatus = "partial"
	}
	return result, nil
}

func apiKeyHintHash(channelType storage.ChannelType, key string) string {
	key = normalizeFingerprintAPIKey(channelType, key)
	if key == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(key))
	return hex.EncodeToString(digest[:])
}
