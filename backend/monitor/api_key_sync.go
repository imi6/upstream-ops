package monitor

import (
	"context"

	"github.com/bejix/upstream-ops/backend/storage"
)

// SyncExternalAPIKeys 按现有渠道并发上限同步外部 API 授权的渠道。
// 启动时只补齐尚未同步或上次不完整的快照，定时任务更新全部授权渠道。
func (s *Service) SyncExternalAPIKeys(ctx context.Context, onlyMissing bool) {
	if s.channelSvc == nil {
		return
	}
	list, err := s.channelSvc.APIKeySyncChannels()
	if err != nil {
		s.log.Warn("list external API key sync channels failed", "err", err)
		return
	}
	if len(list) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, scanBudget(len(list), s.scanConcurrency()))
	defer cancel()
	s.forEachChannel(ctx, list, "api_keys", func(chCtx context.Context, ch *storage.Channel, _ int) {
		if onlyMissing {
			state, err := s.channelSvc.ExportAPIKeyFingerprints(chCtx, ch.ID)
			if err == nil && state.SyncStatus == "ready" {
				return
			}
		}
		if err := s.channelSvc.SyncAPIKeySnapshots(chCtx, ch.ID, false); err != nil {
			s.log.Warn("sync external API key snapshots failed", "channel", ch.Name, "err", err)
			return
		}
		state, err := s.channelSvc.ExportAPIKeyFingerprints(chCtx, ch.ID)
		if err == nil && state.SyncStatus != "ready" {
			s.log.Warn("external API key snapshots incomplete", "channel", ch.Name, "unavailableKeyCount", len(state.UnavailableKeyIDs))
		}
	})
}
