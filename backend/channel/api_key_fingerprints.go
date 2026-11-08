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
	MaskedKey   string
	KeyPrefix   string
	KeySuffix   string
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
	ChannelSource         string
	Keys                  []APIKeyFingerprint
	UnavailableKeyIDs     []int64
	UnavailableHintKeyIDs []int64
	HintsSynced           bool
	HintsComplete         bool
	Groups                *APIKeyGroupSet
	SyncedAt              *time.Time
	AttemptedAt           *time.Time
	SyncStatus            string
	SyncError             string
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
	return strings.ContainsAny(key, "*…•●") || strings.Contains(key, "...")
}

// APIKeyHintNormalization 只用于可见前后缀，独立于各上游的精确指纹规则。
const APIKeyHintNormalization = "trim_strip_sk_prefix"

const apiKeyHintVersion = 1

type apiKeyMatchHint struct {
	maskedKey string
	prefix    string
	suffix    string
}

// newAPIKeyMatchHint 不透传上游字符串：最多保留正文前 3 位和后 4 位。
// 对脱敏值只采信遮罩两端；过短的完整 Key 不生成提示，避免暴露大部分内容。
func newAPIKeyMatchHint(key string) apiKeyMatchHint {
	key = strings.TrimSpace(key)
	hasSKPrefix := strings.HasPrefix(key, "sk-")
	key = strings.TrimPrefix(key, "sk-")
	first, last := -1, -1
	for _, marker := range []string{"...", "*", "…", "•", "●"} {
		if start := strings.Index(key, marker); start >= 0 && (first < 0 || start < first) {
			first = start
		}
		if end := strings.LastIndex(key, marker); end >= 0 && end+len(marker) > last {
			last = end + len(marker)
		}
	}
	var prefix, suffix []rune
	if first >= 0 {
		prefix, suffix = []rune(key[:first]), []rune(key[last:])
	} else {
		value := []rune(key)
		if len(value) < 16 {
			return apiKeyMatchHint{}
		}
		prefix, suffix = value, value
	}
	if len(prefix) > 3 {
		prefix = prefix[:3]
	}
	if len(suffix) > 4 {
		suffix = suffix[len(suffix)-4:]
	}
	hint := apiKeyMatchHint{prefix: string(prefix), suffix: string(suffix)}
	if hint.prefix != "" || hint.suffix != "" {
		hint.maskedKey = hint.prefix + "..." + hint.suffix
		if hasSKPrefix {
			hint.maskedKey = "sk-" + hint.maskedKey
		}
	}
	return hint
}

// reuseAPIKeyMatchHint 仅在调用方已确认密钥未变时，保留旧快照中更完整的可见部分。
func reuseAPIKeyMatchHint(old storage.ChannelAPIKeySnapshot, current apiKeyMatchHint) apiKeyMatchHint {
	if old.KeyHintVersion == apiKeyHintVersion && strings.HasPrefix(old.KeyPrefix, current.prefix) && strings.HasSuffix(old.KeySuffix, current.suffix) {
		return apiKeyMatchHint{maskedKey: old.MaskedKey, prefix: old.KeyPrefix, suffix: old.KeySuffix}
	}
	return current
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
		s.keyListInvalid = true
		s.unavailableAPIKey(key.ID)
		return
	}
	if _, exists := s.bindings[key.ID]; exists {
		s.keyListInvalid = true
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
	s.keyHints[key.ID] = newAPIKeyMatchHint(key.Key)
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
		UnavailableHintKeyIDs: []int64{},
		Groups:                &APIKeyGroupSet{IDs: map[int64]struct{}{}, Names: map[string]struct{}{}},
		SyncedAt:              state.SyncedAt, AttemptedAt: state.AttemptedAt,
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
	result.HintsSynced = state.SyncedAt != nil
	result.HintsComplete = state.SyncedAt != nil && result.SyncStatus != "failed"
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
		exported := APIKeyFingerprint{APIKeyID: key.APIKeyID, GroupID: key.GroupID, GroupName: key.GroupName}
		if key.Fingerprint != "" && counts[key.Fingerprint] == 1 {
			exported.Fingerprint = key.Fingerprint
		}
		if key.KeyHintVersion != apiKeyHintVersion {
			result.HintsSynced = false
		} else {
			exported.MaskedKey, exported.KeyPrefix, exported.KeySuffix = key.MaskedKey, key.KeyPrefix, key.KeySuffix
		}
		if len([]rune(exported.KeySuffix)) != 4 {
			result.UnavailableHintKeyIDs = append(result.UnavailableHintKeyIDs, key.APIKeyID)
			result.HintsComplete = false
		}
		// 即使没有完整指纹也保留候选，避免兜底时漏掉可能发生冲突的 Key。
		result.Keys = append(result.Keys, exported)
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
