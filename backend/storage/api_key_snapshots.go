package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ChannelAPIKeySnapshot 只保存分组关系与摘要，不保存完整密钥或打码密钥。
type ChannelAPIKeySnapshot struct {
	ChannelID                uint      `gorm:"primaryKey;autoIncrement:false"`
	APIKeyID                 int64     `gorm:"primaryKey;autoIncrement:false"`
	GroupID                  *int64    `gorm:"index"`
	GroupName                string    `gorm:"size:256"`
	Fingerprint              string    `gorm:"size:64"`
	FingerprintNormalization string    `gorm:"size:32"`
	KeyHintHash              string    `gorm:"size:64"`
	FingerprintStale         bool      `gorm:"not null;default:false"`
	LastSeenAt               time.Time `gorm:"not null"`
}

func (ChannelAPIKeySnapshot) TableName() string { return "channel_api_key_snapshots" }

type APIKeySnapshotState struct {
	ChannelSource string
	Keys          []ChannelAPIKeySnapshot
	SyncedAt      *time.Time
	AttemptedAt   *time.Time
	Complete      bool
	SyncError     string
}

type APIKeySnapshots struct{ db *gorm.DB }

func NewAPIKeySnapshots(db *gorm.DB) *APIKeySnapshots { return &APIKeySnapshots{db: db} }

// APIKeySnapshotSource 将账号身份与快照绑定，防止切换站点或账号后下发原账号的分组。
func APIKeySnapshotSource(ch *Channel) string {
	value, _ := json.Marshal([]string{
		string(ch.Type), ch.SiteURL, ch.Username, string(ch.CredentialMode),
		strconv.FormatUint(ch.APIKeyCredentialRevision, 10), ch.LoginExtraParams,
	})
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

// Load 始终只读本地数据库；没有快照时返回未同步状态，不访问上游补齐。
func (r *APIKeySnapshots) Load(ctx context.Context, channelID uint) (*APIKeySnapshotState, error) {
	state := &APIKeySnapshotState{Keys: []ChannelAPIKeySnapshot{}}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ch Channel
		if err := tx.First(&ch, channelID).Error; err != nil {
			return err
		}
		state.ChannelSource = APIKeySnapshotSource(&ch)
		if ch.APIKeySnapshotSource != APIKeySnapshotSource(&ch) {
			return nil
		}
		state.SyncedAt = ch.APIKeySyncedAt
		state.AttemptedAt = ch.APIKeySyncAttemptedAt
		state.Complete = ch.APIKeySyncComplete
		state.SyncError = ch.APIKeySyncError
		return tx.Where("channel_id = ?", channelID).Order("api_key_id ASC").Find(&state.Keys).Error
	})
	return state, err
}

// withSource 在事务内核对账号身份，避免采集期间切换账号、删除渠道造成旧快照回写。
func (r *APIKeySnapshots) withSource(ctx context.Context, source *Channel, fn func(*gorm.DB) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current Channel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, source.ID).Error; err != nil {
			return err
		}
		expected := APIKeySnapshotSource(source)
		if APIKeySnapshotSource(&current) != expected {
			return errors.New("渠道账号已变更，忽略旧账号的密钥同步结果")
		}
		if current.APIKeySnapshotSource != expected {
			if err := tx.Where("channel_id = ?", source.ID).Delete(&ChannelAPIKeySnapshot{}).Error; err != nil {
				return err
			}
			if err := tx.Model(&Channel{}).Where("id = ?", source.ID).Updates(map[string]any{
				"api_key_snapshot_source": expected,
				"api_key_synced_at":       nil, "api_key_sync_attempted_at": nil,
				"api_key_sync_complete": false, "api_key_sync_error": "",
			}).Error; err != nil {
				return err
			}
		}
		return fn(tx)
	})
}

// Replace 仅在完整密钥列表拉取成功后替换快照；事务保证删除、迁组与空列表原子生效。
func (r *APIKeySnapshots) Replace(ctx context.Context, source *Channel, rows []ChannelAPIKeySnapshot, complete bool, summary string, at time.Time) error {
	return r.withSource(ctx, source, func(tx *gorm.DB) error {
		if err := tx.Where("channel_id = ?", source.ID).Delete(&ChannelAPIKeySnapshot{}).Error; err != nil {
			return err
		}
		if len(rows) > 0 {
			if err := tx.CreateInBatches(&rows, 50).Error; err != nil {
				return err
			}
		}
		return tx.Model(&Channel{}).Where("id = ?", source.ID).Updates(map[string]any{
			"api_key_synced_at": at, "api_key_sync_attempted_at": at,
			"api_key_sync_complete": complete, "api_key_sync_error": summary,
		}).Error
	})
}

// MarkFailed 保留上次成功采集的快照，只更新失败状态；对外不暴露上游响应或登录信息。
func (r *APIKeySnapshots) MarkFailed(ctx context.Context, source *Channel, at time.Time) error {
	return r.withSource(ctx, source, func(tx *gorm.DB) error {
		return tx.Model(&Channel{}).Where("id = ?", source.ID).Updates(map[string]any{
			"api_key_sync_attempted_at": at, "api_key_sync_complete": false,
			"api_key_sync_error": "密钥同步失败，保留上次成功采集的数据",
		}).Error
	})
}

// SaveMutation 将本系统已确认的新增、迁组和删除同步到本地，不额外调用上游。
func (r *APIKeySnapshots) SaveMutation(ctx context.Context, source *Channel, row *ChannelAPIKeySnapshot, deletedID int64) error {
	return r.withSource(ctx, source, func(tx *gorm.DB) error {
		if deletedID > 0 {
			return tx.Where("channel_id = ? AND api_key_id = ?", source.ID, deletedID).Delete(&ChannelAPIKeySnapshot{}).Error
		}
		if row == nil || row.APIKeyID <= 0 {
			return nil
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "channel_id"}, {Name: "api_key_id"}},
			UpdateAll: true,
		}).Create(row).Error; err != nil {
			return err
		}
		if row.Fingerprint == "" || row.FingerprintStale {
			return tx.Model(&Channel{}).Where("id = ?", source.ID).Updates(map[string]any{
				"api_key_sync_complete": false, "api_key_sync_error": "部分密钥指纹待后台同步",
			}).Error
		}
		return nil
	})
}
