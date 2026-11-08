package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ExternalAPIConfig 仅授权外部系统读取分组倍率，与后台登录令牌相互独立。
type ExternalAPIConfig struct {
	Enabled              bool   `mapstructure:"enabled" yaml:"enabled" json:"enabled"`
	Token                string `mapstructure:"token" yaml:"token" json:"-"`
	ChannelIDs           []uint `mapstructure:"channelIDs" yaml:"channelIDs" json:"channelIDs"`
	OnlyCreatedKeyGroups bool   `mapstructure:"onlyCreatedKeyGroups" yaml:"onlyCreatedKeyGroups" json:"onlyCreatedKeyGroups"`
}

func (c ExternalAPIConfig) AllowsChannel(id uint) bool {
	if len(c.ChannelIDs) == 0 {
		return true
	}
	for _, allowed := range c.ChannelIDs {
		if allowed == id {
			return true
		}
	}
	return false
}

func (c ExternalAPIConfig) Validate() error {
	token := strings.TrimSpace(c.Token)
	if c.Enabled && len(token) < 32 {
		return fmt.Errorf("外部 API 启用时必须配置至少 32 个字符的独立访问令牌")
	}
	if len(c.Token) > 4096 {
		return fmt.Errorf("外部 API 访问令牌不能超过 4096 字节")
	}
	for _, ch := range token {
		if ch < '!' || ch > '~' {
			return fmt.Errorf("外部 API 访问令牌只能包含无空白的可见 ASCII 字符，建议使用生成按钮")
		}
	}
	for _, id := range c.ChannelIDs {
		if id == 0 {
			return fmt.Errorf("外部 API 允许的渠道 ID 必须为正整数")
		}
	}
	return nil
}

// ExternalAPIWithEnv 在启动和热应用时保持相同的环境变量优先级。
// 空环境变量视为未设置，允许直接使用配置页面保存的值。
func ExternalAPIWithEnv(c ExternalAPIConfig) (ExternalAPIConfig, error) {
	c.ChannelIDs = append([]uint{}, c.ChannelIDs...)
	if raw := strings.TrimSpace(os.Getenv("EXTERNAL_API_ENABLED")); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			return c, fmt.Errorf("EXTERNAL_API_ENABLED 必须是布尔值")
		}
		c.Enabled = enabled
	}
	if raw := strings.TrimSpace(os.Getenv("EXTERNAL_API_ONLY_CREATED_KEY_GROUPS")); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			return c, fmt.Errorf("EXTERNAL_API_ONLY_CREATED_KEY_GROUPS 必须是布尔值")
		}
		c.OnlyCreatedKeyGroups = enabled
	}
	if raw := strings.TrimSpace(os.Getenv("EXTERNAL_API_TOKEN")); raw != "" {
		c.Token = raw
	}
	if raw := strings.TrimSpace(os.Getenv("EXTERNAL_API_CHANNEL_IDS")); raw != "" {
		c.ChannelIDs = []uint{}
		for _, part := range strings.Split(raw, ",") {
			id, err := strconv.ParseUint(strings.TrimSpace(part), 10, strconv.IntSize)
			if err != nil || id == 0 {
				return c, fmt.Errorf("EXTERNAL_API_CHANNEL_IDS 必须是逗号分隔的正整数")
			}
			c.ChannelIDs = append(c.ChannelIDs, uint(id))
		}
	}
	c.Token = strings.TrimSpace(c.Token)
	return c, nil
}
