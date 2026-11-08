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

func reusableAPIKeyFingerprint(row storage.ChannelAPIKeySnapshot, channelType storage.ChannelType, hint string) bool {
	if row.FingerprintNormalization != APIKeyFingerprintNormalization(channelType) {
		return false
	}
	decoded, err := hex.DecodeString(row.Fingerprint)
	if err != nil || len(decoded) != 32 {
		return false
	}
	return hint == "" || hint == row.KeyHintHash
}

// SyncAPIKeySnapshots 是密钥快照唯一的回源入口，供后台任务和显式强制刷新调用。
// 后台同步复用未变更密钥的持久化摘要，避免每轮都向上游读取完整 Key。
func (s *Service) SyncAPIKeySnapshots(ctx context.Context, channelID uint, force bool) error {
	if s.APIKeySnapshots == nil {
		return errors.New("密钥快照存储不可用")
	}
	unlock, err := s.lockAPIKeyGroups(ctx, channelID)
	if err != nil {
		return err
	}
	defer unlock()
	source, err := s.Channels.FindByID(channelID)
	if err != nil {
		return err
	}
	fail := func(syncErr error) error {
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = s.APIKeySnapshots.MarkFailed(writeCtx, source, time.Now())
		return syncErr
	}
	previous, err := s.APIKeySnapshots.Load(ctx, channelID)
	if err != nil {
		return fail(err)
	}
	byID := make(map[int64]storage.ChannelAPIKeySnapshot, len(previous.Keys))
	for _, key := range previous.Keys {
		byID[key.APIKeyID] = key
	}

	// 同步始终重新获取密钥列表，避免分页缓存掩盖删除、迁组及新建密钥。
	s.apiKeyGroupSetCache.Delete(channelID)
	set, err := s.listAPIKeyGroupSet(ctx, channelID)
	if err != nil {
		return fail(err)
	}
	pending := make([]int, 0, len(set.maskedKeys))
	for i := range set.maskedKeys {
		candidate := &set.maskedKeys[i]
		old, exists := byID[candidate.binding.APIKeyID]
		if !force && exists && reusableAPIKeyFingerprint(old, set.channelType, set.keyHintHashes[candidate.binding.APIKeyID]) {
			decoded, _ := hex.DecodeString(old.Fingerprint)
			var digest [32]byte
			copy(digest[:], decoded)
			set.rememberAPIKey(digest, candidate.binding)
			continue
		}
		pending = append(pending, i)
	}
	if len(pending) > 0 {
		_, resolved, conn, session, prepareErr := s.prepareConnectorCall(ctx, channelID)
		for _, i := range pending {
			candidate := &set.maskedKeys[i]
			full := ""
			if prepareErr == nil && ctx.Err() == nil {
				value, revealErr := conn.RevealAPIKey(ctx, resolved, session, candidate.binding.APIKeyID)
				if revealErr == nil {
					full = normalizeFingerprintAPIKey(set.channelType, value)
				}
			}
			if full != "" && !maskedAPIKey(full) {
				fingerprint := sha256.Sum256([]byte(full))
				set.rememberAPIKey(fingerprint, candidate.binding)
				continue
			}
			set.unavailableAPIKey(candidate.binding.APIKeyID)
			// 强刷失败但密钥提示未变时保留旧摘要，并明确标记过期，避免丢掉可用快照。
			old, exists := byID[candidate.binding.APIKeyID]
			if exists && reusableAPIKeyFingerprint(old, set.channelType, set.keyHintHashes[candidate.binding.APIKeyID]) {
				decoded, _ := hex.DecodeString(old.Fingerprint)
				var fingerprint [32]byte
				copy(fingerprint[:], decoded)
				set.rememberAPIKey(fingerprint, candidate.binding)
			}
		}
	}
	byKeyID := make(map[int64]string, len(set.keyFingerprints))
	for digest, binding := range set.keyFingerprints {
		if binding.APIKeyID > 0 {
			byKeyID[binding.APIKeyID] = hex.EncodeToString(digest[:])
		}
	}
	at := time.Now()
	rows := make([]storage.ChannelAPIKeySnapshot, 0, len(set.bindings))
	complete := len(set.unavailableKeyIDs) == 0
	for id, binding := range set.bindings {
		_, unavailable := set.unavailableKeyIDs[id]
		fingerprint := byKeyID[id]
		if fingerprint == "" {
			unavailable = true
			complete = false
		}
		rows = append(rows, storage.ChannelAPIKeySnapshot{
			ChannelID: channelID, APIKeyID: id, GroupID: binding.GroupID, GroupName: binding.GroupName,
			Fingerprint: fingerprint, FingerprintNormalization: APIKeyFingerprintNormalization(set.channelType),
			KeyHintHash: set.keyHintHashes[id], FingerprintStale: unavailable, LastSeenAt: at,
		})
	}
	summary := ""
	if !complete {
		summary = "部分密钥指纹暂不可用；可用旧指纹已保留"
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.APIKeySnapshots.Replace(writeCtx, source, rows, complete, summary, at); err != nil {
		return fail(err)
	}
	return ctx.Err()
}

// persistAPIKeyMutation 复用管理操作已获得的结果，只更新本地快照，不追加上游请求。
func (s *Service) persistAPIKeyMutation(ctx context.Context, source *storage.Channel, key *connector.APIKey, deletedID int64) {
	if s.APIKeySnapshots == nil {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var row *storage.ChannelAPIKeySnapshot
	if key != nil && key.ID > 0 {
		row = &storage.ChannelAPIKeySnapshot{ChannelID: source.ID, APIKeyID: key.ID}
		state, err := s.APIKeySnapshots.Load(writeCtx, source.ID)
		if err == nil {
			for _, old := range state.Keys {
				if old.APIKeyID == key.ID {
					*row = old
					break
				}
			}
		}
		if key.GroupID != nil {
			row.GroupID = key.GroupID
		}
		group := strings.TrimSpace(key.Group)
		if source.Type == storage.ChannelTypeSub2API || group == "" {
			group = strings.TrimSpace(key.GroupName)
		}
		if group != "" {
			row.GroupName = group
		}
		value := normalizeFingerprintAPIKey(source.Type, key.Key)
		if value != "" {
			hint := apiKeyHintHash(source.Type, key.Key)
			if !maskedAPIKey(value) {
				row.Fingerprint = hint
				row.FingerprintStale = false
			} else if !reusableAPIKeyFingerprint(*row, source.Type, hint) {
				row.Fingerprint = ""
				row.FingerprintStale = true
			}
			row.KeyHintHash = hint
		}
		row.FingerprintNormalization = APIKeyFingerprintNormalization(source.Type)
		if (row.GroupID == nil || *row.GroupID <= 0) && row.GroupName == "" {
			row.FingerprintStale = true
		}
		row.LastSeenAt = time.Now()
	}
	if err := s.APIKeySnapshots.SaveMutation(writeCtx, source, row, deletedID); err != nil {
		_ = s.APIKeySnapshots.MarkFailed(writeCtx, source, time.Now())
	}
}
