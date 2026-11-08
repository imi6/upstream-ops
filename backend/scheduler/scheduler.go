// Package scheduler 用 robfig/cron 触发周期性扫描。
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/bejix/upstream-ops/backend/captcha"
	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/monitor"
	"github.com/bejix/upstream-ops/backend/storage"
	"github.com/robfig/cron/v3"
)

// captchaRefreshBudget 打码平台余额刷新的总预算。
const captchaRefreshBudget = 2 * time.Minute

// Scheduler 周期性任务调度器。
type Scheduler struct {
	cfg           config.SchedulerConfig
	log           *slog.Logger
	cron          *cron.Cron
	monitor       *monitor.Service
	monLogs       *storage.MonitorLogs
	rates         *storage.Rates
	notifies      *storage.Notifications
	announcements *storage.UpstreamAnnouncements
	captchas      *storage.Captchas
	cipher        *crypto.Cipher
	proxy         config.ProxyConfig
	startupCancel context.CancelFunc
	startupDone   chan struct{}
}

// New 构造调度器。
func New(
	cfg config.SchedulerConfig,
	m *monitor.Service,
	monLogs *storage.MonitorLogs,
	rates *storage.Rates,
	notifies *storage.Notifications,
	announcements *storage.UpstreamAnnouncements,
	captchas *storage.Captchas,
	cipher *crypto.Cipher,
	proxy config.ProxyConfig,
	log *slog.Logger,
) *Scheduler {
	return &Scheduler{
		cfg: cfg,
		log: log,
		// Recover 避免单个任务 panic 拖垮调度器；SkipIfStillRunning 在上一轮还没跑完时
		// 跳过本轮，防止扫描任务叠加。
		cron: cron.New(
			cron.WithSeconds(),
			cron.WithChain(
				cron.Recover(cronLogger{log: log}),
				cron.SkipIfStillRunning(cronLogger{log: log}),
			),
		),
		monitor:       m,
		monLogs:       monLogs,
		rates:         rates,
		notifies:      notifies,
		announcements: announcements,
		captchas:      captchas,
		cipher:        cipher,
		proxy:         proxy,
	}
}

// Start 注册 cron 任务并启动。
func (s *Scheduler) Start() error {
	if s.cfg.BalanceCron != "" {
		if _, err := s.cron.AddFunc(s.cfg.BalanceCron, s.runBalance); err != nil {
			return err
		}
	}
	if s.cfg.RateCron != "" {
		if _, err := s.cron.AddFunc(s.cfg.RateCron, s.runRates); err != nil {
			return err
		}
	}
	if s.cfg.Retention.Cron != "" && s.hasRetention() {
		if _, err := s.cron.AddFunc(s.cfg.Retention.Cron, s.runRetention); err != nil {
			return err
		}
	}
	s.cron.Start()
	// 把并发度下发给 monitor：定时扫描按这个值限制同时在跑的渠道数。
	// 配置热重载会新建 Scheduler 并 Start，所以这里下发即可覆盖新配置。
	if s.monitor != nil {
		s.monitor.SetConcurrency(s.cfg.Concurrency)
		ctx, cancel := context.WithCancel(context.Background())
		s.startupCancel = cancel
		s.startupDone = make(chan struct{})
		go func() {
			defer close(s.startupDone)
			s.monitor.SyncExternalAPIKeys(ctx, true)
		}()
	}
	s.log.Info("scheduler started",
		"balanceCron", s.cfg.BalanceCron,
		"rateCron", s.cfg.RateCron,
		"retentionCron", s.cfg.Retention.Cron,
		"concurrency", s.cfg.Concurrency,
	)
	return nil
}

// Stop 停止调度器。
func (s *Scheduler) Stop() {
	if s.startupCancel != nil {
		s.startupCancel()
	}
	if s.cron != nil {
		<-s.cron.Stop().Done()
	}
	if s.startupDone != nil {
		<-s.startupDone
	}
}

// 每轮扫描的总预算由 monitor 按渠道数推算，这里不再设固定上限
// —— 固定预算会在渠道多时被前面的渠道吃光，导致后面的渠道拿到已过期的 context 而整批失败。
func (s *Scheduler) runBalance() {
	ctx := context.Background()
	s.monitor.ScanAllBalances(ctx)
	if s.captchas != nil && s.cipher != nil {
		captchaCtx, cancel := context.WithTimeout(ctx, captchaRefreshBudget)
		defer cancel()
		if _, err := captcha.RefreshAllBalancesWithProxy(captchaCtx, s.captchas, s.cipher, s.log, s.proxy); err != nil {
			s.log.Warn("refresh captcha balances failed", "err", err)
		}
	}
}

func (s *Scheduler) runRates() {
	if s.monitor != nil {
		s.monitor.ScanAllRates(context.Background())
		s.monitor.SyncExternalAPIKeys(context.Background(), false)
	}
}

// cronLogger 把 robfig/cron 的内部日志（任务跳过、panic 恢复）转发到 slog。
// log 为 nil 时静默丢弃，避免调度器构造方未传 logger 时 panic。
type cronLogger struct{ log *slog.Logger }

func (l cronLogger) Info(msg string, keysAndValues ...any) {
	if l.log == nil {
		return
	}
	l.log.Info(msg, keysAndValues...)
}

func (l cronLogger) Error(err error, msg string, keysAndValues ...any) {
	if l.log == nil {
		return
	}
	l.log.Error(msg, append([]any{"err", err}, keysAndValues...)...)
}

func (s *Scheduler) hasRetention() bool {
	r := s.cfg.Retention
	return r.MonitorLogsDays > 0 ||
		r.BalanceSnapshotsDays > 0 ||
		r.NotificationLogsDays > 0 ||
		r.AnnouncementsDays > 0
}

// runRetention 按配置删除过期历史。任一表失败不影响其它，全部错误写日志。
func (s *Scheduler) runRetention() {
	r := s.cfg.Retention
	now := time.Now()

	if r.MonitorLogsDays > 0 {
		cutoff := now.AddDate(0, 0, -r.MonitorLogsDays)
		n, err := s.monLogs.DeleteBefore(cutoff)
		if err != nil {
			s.log.Warn("retention monitor_logs failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention monitor_logs deleted", "rows", n, "before", cutoff)
		}
	}

	if r.BalanceSnapshotsDays > 0 {
		cutoff := now.AddDate(0, 0, -r.BalanceSnapshotsDays)
		n, err := s.rates.DeleteBalanceSnapshotsBefore(cutoff)
		if err != nil {
			s.log.Warn("retention balance_snapshots failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention balance_snapshots deleted", "rows", n, "before", cutoff)
		}

		n, err = s.rates.DeleteCostSnapshotsBefore(cutoff)
		if err != nil {
			s.log.Warn("retention cost_snapshots failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention cost_snapshots deleted", "rows", n, "before", cutoff)
		}
	}

	if r.NotificationLogsDays > 0 {
		cutoff := now.AddDate(0, 0, -r.NotificationLogsDays)
		n, err := s.notifies.DeleteLogsBefore(cutoff)
		if err != nil {
			s.log.Warn("retention notification_logs failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention notification_logs deleted", "rows", n, "before", cutoff)
		}
	}

	if r.AnnouncementsDays > 0 && s.announcements != nil {
		cutoff := now.AddDate(0, 0, -r.AnnouncementsDays)
		n, err := s.announcements.DeleteBefore(cutoff)
		if err != nil {
			s.log.Warn("retention announcements failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention announcements deleted", "rows", n, "before", cutoff)
		}
	}
}
