package channel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/bejix/upstream-ops/backend/connector"
	"github.com/bejix/upstream-ops/backend/storage"
)

type apiKeyGroupBinding struct {
	APIKeyID  int64
	GroupID   *int64
	GroupName string
}

type apiKeyGroupCandidate struct {
	binding  apiKeyGroupBinding
	resolved bool
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
	Keys              []APIKeyFingerprint
	UnavailableKeyIDs []int64
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

// ExportAPIKeyFingerprints 仅导出指纹与分组绑定，不接收外部 Key。
// 上游只返回打码值时，在本服务内读取完整值计算摘要；缓存不保留完整密钥。
// 单条密钥读取失败不应丢弃其他可用绑定，响应中显式标记未导出的密钥 ID。
func (s *Service) ExportAPIKeyFingerprints(ctx context.Context, channelID uint) (*APIKeyFingerprintExport, error) {
	unlock, err := s.lockAPIKeyGroups(ctx, channelID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	set, err := s.listAPIKeyGroupSet(ctx, channelID)
	if err != nil {
		return nil, err
	}
	pending := make([]int, 0)
	for i, candidate := range set.maskedKeys {
		if !candidate.resolved {
			pending = append(pending, i)
		}
	}
	if len(pending) > 0 {
		_, resolved, conn, session, prepareErr := s.prepareConnectorCall(ctx, channelID)
		if prepareErr == nil {
			for _, i := range pending {
				if ctx.Err() != nil {
					break
				}
				candidate := &set.maskedKeys[i]
				full, err := conn.RevealAPIKey(ctx, resolved, session, candidate.binding.APIKeyID)
				if err != nil {
					continue
				}
				full = normalizeFingerprintAPIKey(set.channelType, full)
				if full == "" || maskedAPIKey(full) {
					continue
				}
				set.rememberAPIKey(sha256.Sum256([]byte(full)), candidate.binding)
				candidate.resolved = true
			}
		}
	}
	unavailable := make(map[int64]struct{}, len(set.unavailableKeyIDs))
	for id := range set.unavailableKeyIDs {
		unavailable[id] = struct{}{}
	}
	for _, candidate := range set.maskedKeys {
		if !candidate.resolved {
			unavailable[candidate.binding.APIKeyID] = struct{}{}
		}
	}
	result := &APIKeyFingerprintExport{
		Keys:              make([]APIKeyFingerprint, 0, len(set.keyFingerprints)),
		UnavailableKeyIDs: make([]int64, 0, len(unavailable)),
	}
	for fingerprint, binding := range set.keyFingerprints {
		if binding.APIKeyID <= 0 {
			continue
		}
		result.Keys = append(result.Keys, APIKeyFingerprint{
			APIKeyID:    binding.APIKeyID,
			Fingerprint: hex.EncodeToString(fingerprint[:]),
			GroupID:     binding.GroupID,
			GroupName:   binding.GroupName,
		})
	}
	for id := range unavailable {
		result.UnavailableKeyIDs = append(result.UnavailableKeyIDs, id)
	}
	sort.Slice(result.Keys, func(i, j int) bool { return result.Keys[i].APIKeyID < result.Keys[j].APIKeyID })
	sort.Slice(result.UnavailableKeyIDs, func(i, j int) bool { return result.UnavailableKeyIDs[i] < result.UnavailableKeyIDs[j] })
	return result, nil
}
