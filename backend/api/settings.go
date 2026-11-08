package api

import (
	"net/http"
	"strings"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/gin-gonic/gin"
)

type settingsConfigView struct {
	App           config.AppConfig           `json:"app"`
	Auth          config.AuthConfig          `json:"auth"`
	ExternalAPI   externalAPISettingsView    `json:"externalAPI"`
	Scheduler     config.SchedulerConfig     `json:"scheduler"`
	Notifications config.NotificationsConfig `json:"notifications"`
	Proxy         config.ProxyConfig         `json:"proxy"`
	Upstream      config.UpstreamConfig      `json:"upstream"`
}

type settingsConfigInput struct {
	App           config.AppConfig           `json:"app" binding:"required"`
	Auth          config.AuthConfig          `json:"auth" binding:"required"`
	ExternalAPI   *externalAPISettingsInput  `json:"externalAPI"`
	Scheduler     config.SchedulerConfig     `json:"scheduler" binding:"required"`
	Notifications config.NotificationsConfig `json:"notifications" binding:"required"`
	Proxy         config.ProxyConfig         `json:"proxy"`
	Upstream      config.UpstreamConfig      `json:"upstream"`
}

type externalAPISettingsView struct {
	Enabled              bool   `json:"enabled"`
	TokenConfigured      bool   `json:"tokenConfigured"`
	ChannelIDs           []uint `json:"channelIDs"`
	OnlyCreatedKeyGroups bool   `json:"onlyCreatedKeyGroups"`
}

type externalAPISettingsInput struct {
	Enabled              bool   `json:"enabled"`
	Token                string `json:"token"`
	ChannelIDs           []uint `json:"channelIDs"`
	OnlyCreatedKeyGroups *bool  `json:"onlyCreatedKeyGroups"`
}

func registerSettings(g *gin.RouterGroup, d *Deps) {
	gs := g.Group("/settings")
	gs.GET("/config", func(c *gin.Context) { getSettingsConfig(c, d) })
	gs.PUT("/config", func(c *gin.Context) { saveSettingsConfig(c, d) })
	gs.POST("/apply", func(c *gin.Context) { applySettingsConfig(c, d) })
	gs.POST("/proxy/test", func(c *gin.Context) { testProxy(c) })
}

func getSettingsConfig(c *gin.Context, d *Deps) {
	cfg, err := config.LoadFile(d.Runtime.ConfigPath())
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	externalAPI, err := config.ExternalAPIWithEnv(cfg.ExternalAPI)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"config_path": d.Runtime.ConfigPath(),
			"config": settingsConfigView{
				App:  cfg.App,
				Auth: cfg.Auth,
				ExternalAPI: externalAPISettingsView{
					Enabled:              externalAPI.Enabled,
					TokenConfigured:      strings.TrimSpace(externalAPI.Token) != "",
					ChannelIDs:           externalAPI.ChannelIDs,
					OnlyCreatedKeyGroups: externalAPI.OnlyCreatedKeyGroups,
				},
				Scheduler:     cfg.Scheduler,
				Notifications: cfg.Notifications,
				Proxy:         cfg.Proxy,
				Upstream:      cfg.Upstream.WithDefaults(),
			},
		},
	})
}

func saveSettingsConfig(c *gin.Context, d *Deps) {
	var in settingsConfigInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}

	path := d.Runtime.ConfigPath()
	cfg, err := config.LoadFile(path)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	cfg.App.Title = in.App.Title
	cfg.App.NotificationPrefix = in.App.NotificationPrefix
	cfg.Auth = in.Auth
	cfg.Scheduler = in.Scheduler
	cfg.Notifications = in.Notifications
	cfg.Proxy = in.Proxy
	cfg.Upstream = in.Upstream.WithDefaults()
	if in.ExternalAPI != nil {
		cfg.ExternalAPI.Enabled = in.ExternalAPI.Enabled
		cfg.ExternalAPI.ChannelIDs = in.ExternalAPI.ChannelIDs
		if in.ExternalAPI.OnlyCreatedKeyGroups != nil {
			cfg.ExternalAPI.OnlyCreatedKeyGroups = *in.ExternalAPI.OnlyCreatedKeyGroups
		}
		// 空令牌保留旧值；读取设置时不回显密钥，避免普通保存覆盖已配置的令牌。
		if token := strings.TrimSpace(in.ExternalAPI.Token); token != "" {
			cfg.ExternalAPI.Token = token
		}
	}
	externalAPI, err := config.ExternalAPIWithEnv(cfg.ExternalAPI)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := externalAPI.Validate(); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}

	if err := config.Save(path, cfg); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"config_path": path,
			"message":     "已写入配置文件",
		},
	})
}

func applySettingsConfig(c *gin.Context, d *Deps) {
	result, err := d.Runtime.ApplyFromFile()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}
