package api

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bejix/upstream-ops/backend/connector"
	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/gin-gonic/gin"
)

type rankingFilter struct {
	query         string
	channelType   storage.ChannelType
	tag           string
	includePaused bool
	limit         int // 0 表示全部。
	balanceOrder  string
}

type rankingItem struct {
	Rank           int                 `json:"rank"`
	ChannelID      uint                `json:"channel_id"`
	Name           string              `json:"name"`
	Type           storage.ChannelType `json:"type"`
	Tags           storage.ChannelTags `json:"tags"`
	MonitorEnabled bool                `json:"monitor_enabled"`
	Amount         float64             `json:"amount"`
	Share          *float64            `json:"share"`
	SampledAt      *time.Time          `json:"sampled_at"`
	IsPreviousDay  bool                `json:"is_previous_day"`
	LowBalance     bool                `json:"low_balance"`
	LastError      string              `json:"last_error,omitempty"`
}

type unrankedChannel struct {
	ChannelID      uint                `json:"channel_id"`
	Name           string              `json:"name"`
	Type           storage.ChannelType `json:"type"`
	Tags           storage.ChannelTags `json:"tags"`
	MonitorEnabled bool                `json:"monitor_enabled"`
	LastError      string              `json:"last_error,omitempty"`
	CostStatus     string              `json:"cost_status"`
	BalanceStatus  string              `json:"balance_status"`
	TodayCostAt    *time.Time          `json:"today_cost_at"`
	LastBalanceAt  *time.Time          `json:"last_balance_at"`
}

type rankingSummary struct {
	TotalChannels            int        `json:"total_channels"`
	FilteredChannels         int        `json:"filtered_channels"`
	TodayCostTotal           float64    `json:"today_cost_total"`
	BalanceTotal             float64    `json:"balance_total"`
	TodayCostChannels        int        `json:"today_cost_channels"`
	BalanceChannels          int        `json:"balance_channels"`
	TodayCostPendingChannels int        `json:"today_cost_pending_channels"`
	BalanceMissingChannels   int        `json:"balance_missing_channels"`
	LatestCostAt             *time.Time `json:"latest_cost_at"`
	LatestBalanceAt          *time.Time `json:"latest_balance_at"`
}

type rankingResponse struct {
	Date             string            `json:"date"`
	TimeZone         string            `json:"time_zone"`
	GeneratedAt      time.Time         `json:"generated_at"`
	AvailableTags    []string          `json:"available_tags"`
	Summary          rankingSummary    `json:"summary"`
	CostRanking      []rankingItem     `json:"cost_ranking"`
	BalanceRanking   []rankingItem     `json:"balance_ranking"`
	UnrankedChannels []unrankedChannel `json:"unranked_channels"`
}

func rankingFilterFromQuery(c *gin.Context) (rankingFilter, error) {
	f := rankingFilter{includePaused: true, limit: 10, balanceOrder: storage.ChannelOrderDesc}
	query := c.Request.URL.Query()
	for _, name := range []string{"q", "type", "tag", "include_paused", "limit", "balance_order"} {
		if values, exists := query[name]; exists && len(values) != 1 {
			return f, fmt.Errorf("%s 只能提供一个值", name)
		}
	}
	text, err := (storage.ChannelListFilter{Query: query.Get("q"), Tag: query.Get("tag")}).Normalize()
	if err != nil {
		return f, err
	}
	f.query, f.tag = text.Query, text.Tag
	f.channelType = storage.ChannelType(strings.ToLower(strings.TrimSpace(query.Get("type"))))
	switch f.channelType {
	case "", storage.ChannelTypeNewAPI, storage.ChannelTypeSub2API:
	default:
		return f, fmt.Errorf("type 仅支持 newapi 或 sub2api")
	}
	if values, exists := query["include_paused"]; exists {
		f.includePaused, err = strconv.ParseBool(strings.TrimSpace(values[0]))
		if err != nil {
			return f, fmt.Errorf("include_paused 必须是布尔值或 0/1")
		}
	}
	if values, exists := query["limit"]; exists {
		switch strings.ToLower(strings.TrimSpace(values[0])) {
		case "10":
			f.limit = 10
		case "20":
			f.limit = 20
		case "all":
			f.limit = 0
		default:
			return f, fmt.Errorf("limit 仅支持 10、20 或 all")
		}
	}
	if values, exists := query["balance_order"]; exists {
		f.balanceOrder = strings.ToLower(strings.TrimSpace(values[0]))
		if f.balanceOrder != storage.ChannelOrderAsc && f.balanceOrder != storage.ChannelOrderDesc {
			return f, fmt.Errorf("balance_order 仅支持 asc 或 desc")
		}
	}
	return f, nil
}

// rankingFoldASCII 与现有渠道标签去重、筛选使用同一大小写范围。
func rankingFoldASCII(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, value)
}

func (f rankingFilter) matches(ch storage.Channel) bool {
	if !f.includePaused && !ch.MonitorEnabled {
		return false
	}
	if f.channelType != "" && ch.Type != f.channelType {
		return false
	}
	if f.query != "" && !strings.Contains(rankingFoldASCII(ch.Name), rankingFoldASCII(f.query)) {
		return false
	}
	if f.tag != "" {
		for _, tag := range ch.Tags {
			if rankingFoldASCII(tag) == rankingFoldASCII(f.tag) {
				return true
			}
		}
		return false
	}
	return true
}

func rankingFinite(value *float64) bool {
	return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0)
}

func rankingCostStatus(ch storage.Channel, start, end time.Time) string {
	if !rankingFinite(ch.TodayCost) {
		return "missing"
	}
	if ch.TodayCostAt == nil || ch.TodayCostAt.IsZero() {
		return "unknown_time"
	}
	if ch.TodayCostAt.Before(start) {
		return "not_synced_today"
	}
	if !ch.TodayCostAt.Before(end) {
		return "invalid_time"
	}
	return "ready"
}

func newerRankingTime(current, candidate *time.Time) *time.Time {
	if candidate == nil || candidate.IsZero() {
		return current
	}
	if current == nil || candidate.After(*current) {
		return candidate
	}
	return current
}

func rankItems(items []rankingItem, ascending bool, limit int) []rankingItem {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Amount == items[j].Amount {
			return items[i].ChannelID < items[j].ChannelID
		}
		if ascending {
			return items[i].Amount < items[j].Amount
		}
		return items[i].Amount > items[j].Amount
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	for i := range items {
		items[i].Rank = i + 1
	}
	return items
}

// buildRankings 只聚合本地采集结果；先求当前筛选全集的合计，再截取榜单。
func buildRankings(channels []storage.Channel, f rankingFilter, now time.Time) rankingResponse {
	start := connector.CostDayStart(now)
	end := start.AddDate(0, 0, 1)
	data := rankingResponse{
		Date: start.Format("2006-01-02"), TimeZone: connector.CostTimeZone, GeneratedAt: now,
		AvailableTags: []string{}, CostRanking: []rankingItem{}, BalanceRanking: []rankingItem{},
		UnrankedChannels: []unrankedChannel{},
		Summary:          rankingSummary{TotalChannels: len(channels)},
	}
	allTags := make([]string, 0)
	hasNegativeCost := false
	for _, ch := range channels {
		allTags = append(allTags, ch.Tags...)
		if !f.matches(ch) {
			continue
		}
		data.Summary.FilteredChannels++
		item := rankingItem{
			ChannelID: ch.ID, Name: ch.Name, Type: ch.Type, Tags: ch.Tags,
			MonitorEnabled: ch.MonitorEnabled, LastError: ch.LastError,
		}
		costStatus := rankingCostStatus(ch, start, end)
		balanceStatus := "missing"
		if costStatus == "ready" {
			cost := item
			cost.Amount, cost.SampledAt = *ch.TodayCost, ch.TodayCostAt
			data.CostRanking = append(data.CostRanking, cost)
			data.Summary.TodayCostTotal += cost.Amount
			data.Summary.TodayCostChannels++
			data.Summary.LatestCostAt = newerRankingTime(data.Summary.LatestCostAt, cost.SampledAt)
			hasNegativeCost = hasNegativeCost || cost.Amount < 0
		} else {
			data.Summary.TodayCostPendingChannels++
		}
		if rankingFinite(ch.LastBalance) {
			balanceStatus = "ready"
			balance := item
			balance.Amount, balance.SampledAt = *ch.LastBalance, ch.LastBalanceAt
			if balance.SampledAt != nil && balance.SampledAt.IsZero() {
				balance.SampledAt = nil
			}
			balance.IsPreviousDay = balance.SampledAt != nil && balance.SampledAt.Before(start)
			balance.LowBalance = ch.BalanceThreshold > 0 && balance.Amount < ch.BalanceThreshold
			data.BalanceRanking = append(data.BalanceRanking, balance)
			data.Summary.BalanceTotal += balance.Amount
			data.Summary.BalanceChannels++
			data.Summary.LatestBalanceAt = newerRankingTime(data.Summary.LatestBalanceAt, balance.SampledAt)
		} else {
			data.Summary.BalanceMissingChannels++
		}
		if costStatus != "ready" || balanceStatus != "ready" {
			data.UnrankedChannels = append(data.UnrankedChannels, unrankedChannel{
				ChannelID: ch.ID, Name: ch.Name, Type: ch.Type, Tags: ch.Tags,
				MonitorEnabled: ch.MonitorEnabled, LastError: ch.LastError,
				CostStatus: costStatus, BalanceStatus: balanceStatus,
				TodayCostAt: ch.TodayCostAt, LastBalanceAt: ch.LastBalanceAt,
			})
		}
	}
	if tags := storage.NormalizeChannelTags(allTags); len(tags) > 0 {
		data.AvailableTags = tags
	}
	// 含负消费或总消费为零时不计算占比，避免退款使比例产生误导。
	if data.Summary.TodayCostTotal > 0 && !hasNegativeCost {
		for i := range data.CostRanking {
			share := data.CostRanking[i].Amount / data.Summary.TodayCostTotal
			data.CostRanking[i].Share = &share
		}
	}
	data.CostRanking = rankItems(data.CostRanking, false, f.limit)
	data.BalanceRanking = rankItems(data.BalanceRanking, f.balanceOrder == storage.ChannelOrderAsc, f.limit)
	return data
}

func dashboardRankings(c *gin.Context, d *Deps) {
	filter, err := rankingFilterFromQuery(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	channels, err := d.Channels.ListForRankings(c.Request.Context())
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"data": buildRankings(channels, filter, costNow())})
}
