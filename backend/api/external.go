package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bejix/upstream-ops/backend/channel"
	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const externalAPIConfigKey = "external_api_config"

// externalGroupRate 仅输出分组展示所需字段，禁止携带渠道登录凭据或上游密钥。
type externalGroupRate struct {
	GroupID     *int64                      `json:"group_id,omitempty"`
	GroupName   string                      `json:"group_name"`
	Description string                      `json:"description,omitempty"`
	Ratio       float64                     `json:"ratio"`
	UpdatedAt   time.Time                   `json:"updated_at"`
	APIKeys     []externalAPIKeyFingerprint `json:"api_keys"`
}

type externalAPIKeyFingerprint struct {
	APIKeyID    int64  `json:"api_key_id"`
	Fingerprint string `json:"fingerprint"`
}

type externalChannelRates struct {
	ChannelID                   uint                `json:"channel_id"`
	ChannelName                 string              `json:"channel_name"`
	ChannelType                 storage.ChannelType `json:"channel_type"`
	SiteURL                     string              `json:"site_url"`
	OnlyWithKeys                bool                `json:"only_with_keys"`
	KeyFingerprintAlgorithm     string              `json:"key_fingerprint_algorithm"`
	KeyFingerprintNormalization string              `json:"key_fingerprint_normalization"`
	KeyFingerprintsComplete     bool                `json:"key_fingerprints_complete"`
	KeySyncedAt                 *time.Time          `json:"key_synced_at"`
	KeySyncAttemptedAt          *time.Time          `json:"key_sync_attempted_at"`
	KeySyncStatus               string              `json:"key_sync_status"`
	KeySyncError                string              `json:"key_sync_error,omitempty"`
	UnavailableAPIKeyIDs        []int64             `json:"unavailable_api_key_ids"`
	Groups                      []externalGroupRate `json:"groups"`
}

type externalChannelError struct {
	ChannelID uint   `json:"channel_id"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

type externalRateFilter struct {
	GroupName    string
	GroupID      int64
	OnlyWithKeys bool
	ForceRefresh bool
}

func registerExternalAPI(r *gin.Engine, d *Deps) {
	g := r.Group("/api/external/v1", externalAPIAuth(d))
	g.GET("/channels/:id/group-rates", func(c *gin.Context) { externalGroupRates(c, d) })
	g.GET("/channel-rates", func(c *gin.Context) { externalAllChannelRates(c, d) })
}

func externalAPIFail(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"success": false,
		"code":    code,
		"message": message,
	})
}

func externalAPIAuth(d *Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if d.Runtime == nil {
			externalAPIFail(c, http.StatusNotFound, "api_disabled", "外部 API 未启用")
			return
		}
		cfg := d.Runtime.CurrentExternalAPI()
		if !cfg.Enabled {
			externalAPIFail(c, http.StatusNotFound, "api_disabled", "外部 API 未启用")
			return
		}
		if err := cfg.Validate(); err != nil {
			externalAPIFail(c, http.StatusServiceUnavailable, "api_unavailable", "外部 API 配置无效")
			return
		}
		header := strings.Fields(c.GetHeader("Authorization"))
		if len(header) != 2 || !strings.EqualFold(header[0], "Bearer") {
			c.Header("WWW-Authenticate", `Bearer realm="upstream-ops-external"`)
			externalAPIFail(c, http.StatusUnauthorized, "unauthorized", "需要独立的 Bearer 访问令牌")
			return
		}
		// 固定长度摘要参与恒定时间比较，避免直接比较令牌泄露长度与前缀信息。
		expected := sha256.Sum256([]byte(strings.TrimSpace(cfg.Token)))
		provided := sha256.Sum256([]byte(header[1]))
		if subtle.ConstantTimeCompare(expected[:], provided[:]) != 1 {
			c.Header("WWW-Authenticate", `Bearer realm="upstream-ops-external"`)
			externalAPIFail(c, http.StatusUnauthorized, "unauthorized", "访问令牌无效")
			return
		}
		// 将同一份授权快照传递给 handler，避免热更新时鉴权与渠道范围不一致。
		c.Set(externalAPIConfigKey, cfg)
		c.Next()
	}
}

func externalRateFilterFromQuery(c *gin.Context, cfg config.ExternalAPIConfig) (externalRateFilter, bool) {
	filter := externalRateFilter{OnlyWithKeys: cfg.OnlyCreatedKeyGroups}
	query := c.Request.URL.Query()
	if _, present := query["api_key"]; present {
		externalAPIFail(c, http.StatusBadRequest, "unsupported_parameter", "本接口不接收上游 API Key，请在 NewAPI 本地匹配下发的指纹")
		return filter, false
	}
	for _, name := range []string{"group_name", "group_id", "only_with_keys", "force_refresh"} {
		if values, present := query[name]; present && (len(values) != 1 || strings.TrimSpace(values[0]) == "") {
			externalAPIFail(c, http.StatusBadRequest, "invalid_filter", name+" 必须提供唯一的非空值")
			return filter, false
		}
	}
	filter.GroupName = strings.TrimSpace(query.Get("group_name"))
	if raw, present := c.GetQuery("group_id"); present {
		groupID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || groupID <= 0 {
			externalAPIFail(c, http.StatusBadRequest, "invalid_group_id", "分组 ID 必须是正整数")
			return filter, false
		}
		filter.GroupID = groupID
	}
	if raw, present := c.GetQuery("only_with_keys"); present {
		force, err := strconv.ParseBool(raw)
		if err != nil {
			externalAPIFail(c, http.StatusBadRequest, "invalid_filter", "only_with_keys 必须是布尔值或 0/1")
			return filter, false
		}
		filter.OnlyWithKeys = filter.OnlyWithKeys || force
	}
	if raw, present := c.GetQuery("force_refresh"); present {
		force, err := strconv.ParseBool(raw)
		if err != nil {
			externalAPIFail(c, http.StatusBadRequest, "invalid_filter", "force_refresh 必须是布尔值或 0/1")
			return filter, false
		}
		filter.ForceRefresh = force
	}
	return filter, true
}

func externalChannelAllowed(cfg config.ExternalAPIConfig, id uint) bool {
	return cfg.AllowsChannel(id)
}

func externalGroupRates(c *gin.Context, d *Deps) {
	value, _ := c.Get(externalAPIConfigKey)
	cfg, ok := value.(config.ExternalAPIConfig)
	if !ok {
		externalAPIFail(c, http.StatusUnauthorized, "unauthorized", "访问未授权")
		return
	}
	parsedID, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || parsedID == 0 {
		externalAPIFail(c, http.StatusBadRequest, "invalid_channel_id", "渠道 ID 必须是正整数")
		return
	}
	id := uint(parsedID)
	if !externalChannelAllowed(cfg, id) {
		externalAPIFail(c, http.StatusForbidden, "channel_forbidden", "该渠道未授权对外读取")
		return
	}
	filter, ok := externalRateFilterFromQuery(c, cfg)
	if !ok {
		return
	}
	ch, err := d.Channels.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			externalAPIFail(c, http.StatusNotFound, "channel_not_found", "渠道不存在")
		} else {
			externalAPIFail(c, http.StatusInternalServerError, "storage_error", "读取渠道失败")
		}
		return
	}
	timeout := 15 * time.Second
	if filter.ForceRefresh {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	data, err := buildExternalChannelRates(ctx, d, ch, filter)
	if err != nil {
		if filter.ForceRefresh {
			externalAPIFail(c, http.StatusBadGateway, "refresh_failed", "强制刷新失败，已保留本地已有快照；可移除 force_refresh 参数读取")
			return
		}
		externalAPIFail(c, http.StatusBadGateway, "channel_data_unavailable", "读取渠道倍率或 Key 指纹失败，请稍后重试")
		return
	}
	if (filter.GroupName != "" || filter.GroupID > 0) && len(data.Groups) == 0 {
		externalAPIFail(c, http.StatusNotFound, "group_not_found", "分组不存在、尚未采集或未满足密钥筛选条件")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

func buildExternalChannelRates(ctx context.Context, d *Deps, ch *storage.Channel, filter externalRateFilter) (*externalChannelRates, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 只有显式强制刷新才访问上游；普通请求包括缺少快照时都只读取数据库。
	if filter.ForceRefresh {
		if d.Monitor == nil {
			return nil, errors.New("倍率刷新服务不可用")
		}
		rateErr := d.Monitor.RefreshRates(ctx, ch)
		keyErr := d.ChannelSvc.SyncAPIKeySnapshots(ctx, ch.ID, true)
		if rateErr != nil || keyErr != nil {
			return nil, errors.New("强制刷新上游数据失败")
		}
	}
	snapshots, err := d.Rates.ListByChannel(ch.ID)
	if err != nil {
		return nil, err
	}
	data := &externalChannelRates{
		ChannelID:                   ch.ID,
		ChannelName:                 ch.Name,
		ChannelType:                 ch.Type,
		SiteURL:                     externalSiteURL(ch.SiteURL),
		OnlyWithKeys:                filter.OnlyWithKeys,
		KeyFingerprintAlgorithm:     "sha256",
		KeyFingerprintNormalization: channel.APIKeyFingerprintNormalization(ch.Type),
		KeyFingerprintsComplete:     false,
		UnavailableAPIKeyIDs:        []int64{},
		Groups:                      make([]externalGroupRate, 0, len(snapshots)),
	}
	fingerprints, err := d.ChannelSvc.ExportAPIKeyFingerprints(ctx, ch.ID)
	if err != nil {
		return nil, err
	}
	if fingerprints == nil {
		return nil, errors.New("密钥指纹数据不可用")
	}
	if fingerprints.ChannelSource != storage.APIKeySnapshotSource(ch) {
		return nil, errors.New("渠道账号已变更，请重新读取")
	}
	data.UnavailableAPIKeyIDs = fingerprints.UnavailableKeyIDs
	data.KeyFingerprintsComplete = fingerprints.SyncStatus == "ready" && len(fingerprints.UnavailableKeyIDs) == 0
	data.KeySyncedAt = fingerprints.SyncedAt
	data.KeySyncAttemptedAt = fingerprints.AttemptedAt
	data.KeySyncStatus = fingerprints.SyncStatus
	data.KeySyncError = fingerprints.SyncError
	for _, snap := range snapshots {
		if filter.OnlyWithKeys && !fingerprints.Groups.ContainsSnapshot(snap) {
			continue
		}
		if filter.GroupName != "" && snap.ModelName != filter.GroupName {
			continue
		}
		if filter.GroupID > 0 && (snap.RemoteGroupID == nil || *snap.RemoteGroupID != filter.GroupID) {
			continue
		}
		keys := make([]externalAPIKeyFingerprint, 0)
		for _, key := range fingerprints.Keys {
			if key.ContainsSnapshot(snap) {
				keys = append(keys, externalAPIKeyFingerprint{APIKeyID: key.APIKeyID, Fingerprint: key.Fingerprint})
			}
		}
		data.Groups = append(data.Groups, externalGroupRate{
			GroupID:     snap.RemoteGroupID,
			GroupName:   snap.ModelName,
			Description: snap.Description,
			Ratio:       snap.Ratio,
			UpdatedAt:   snap.LastSeenAt,
			APIKeys:     keys,
		})
	}
	return data, nil
}

func externalSiteURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	// 对外仅提供站点标识，不能通过 URL 用户信息或查询参数带出凭据。
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.Host = strings.ToLower(parsed.Host)
	return strings.TrimRight(parsed.String(), "/")
}

func externalAllChannelRates(c *gin.Context, d *Deps) {
	value, _ := c.Get(externalAPIConfigKey)
	cfg, ok := value.(config.ExternalAPIConfig)
	if !ok {
		externalAPIFail(c, http.StatusUnauthorized, "unauthorized", "访问未授权")
		return
	}
	filter, ok := externalRateFilterFromQuery(c, cfg)
	if !ok {
		return
	}
	list, err := d.Channels.List()
	if err != nil {
		externalAPIFail(c, http.StatusInternalServerError, "storage_error", "读取渠道列表失败")
		return
	}
	channels := make([]externalChannelRates, 0)
	channelErrors := make([]externalChannelError, 0)
	complete := true
	found := make(map[uint]struct{}, len(list))
	timeout := 30 * time.Second
	if filter.ForceRefresh {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()
	results := make([]*externalChannelRates, len(list))
	resultErrors := make([]error, len(list))
	indexes := make([]int, 0, len(list))
	for i, ch := range list {
		found[ch.ID] = struct{}{}
		if externalChannelAllowed(cfg, ch.ID) {
			indexes = append(indexes, i)
		}
	}
	load := func(i int) {
		defer func() {
			if recover() != nil {
				resultErrors[i] = errors.New("渠道数据读取异常")
				if d.Log != nil {
					d.Log.Error("external API channel refresh panic", "channel_id", list[i].ID)
				}
			}
		}()
		channelTimeout := 10 * time.Second
		if filter.ForceRefresh {
			channelTimeout = 45 * time.Second
		}
		channelCtx, channelCancel := context.WithTimeout(ctx, channelTimeout)
		defer channelCancel()
		results[i], resultErrors[i] = buildExternalChannelRates(channelCtx, d, &list[i], filter)
	}
	if filter.ForceRefresh {
		// 批量强刷限制为最多四个渠道并发，避免一次请求瞬间打满全部上游。
		jobs := make(chan int, len(indexes))
		for _, i := range indexes {
			jobs <- i
		}
		close(jobs)
		var wg sync.WaitGroup
		for worker := 0; worker < 4 && worker < len(indexes); worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					load(i)
				}
			}()
		}
		wg.Wait()
	} else {
		for _, i := range indexes {
			load(i)
		}
	}
	for _, i := range indexes {
		data, err := results[i], resultErrors[i]
		if err != nil {
			complete = false
			code, message := "channel_data_unavailable", "该渠道本地倍率或密钥快照暂不可用"
			if filter.ForceRefresh {
				code, message = "refresh_failed", "该渠道强制刷新失败，本地已有快照仍保留"
			}
			channelErrors = append(channelErrors, externalChannelError{
				ChannelID: list[i].ID, Code: code, Message: message,
			})
			continue
		}
		if !data.KeyFingerprintsComplete {
			complete = false
		}
		channels = append(channels, *data)
	}
	for _, id := range cfg.ChannelIDs {
		if _, exists := found[id]; !exists {
			complete = false
			channelErrors = append(channelErrors, externalChannelError{ChannelID: id, Code: "channel_not_found", Message: "授权渠道不存在"})
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"generated_at": time.Now().UTC(),
			"complete":     complete,
			"channels":     channels,
			"errors":       channelErrors,
		},
	})
}
