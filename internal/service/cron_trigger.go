package service

import (
	"ci-platform/internal/pkg/logger"
	"context"
	"strings"
	"sync"
	"time"

	"ci-platform/internal/model"
)

// CronTrigger 定时构建触发器（对齐 Jenkins Build periodically）。
// 每 interval 扫描一次任务列表，cron 规格到期即触发构建。
// 计划触发时间仅保存在内存中：重启后按规格重新计算（Jenkins 持久化，
// 但内存方案实现简单且重启后最多延迟一个扫描周期，可接受）。
type CronTrigger struct {
	pipelines PipelineStore
	triggerer BuildTriggerer
	interval  time.Duration

	// pipelineID → 已解析的规格（变更时重算下次触发时间）与计划触发时刻。
	mu    sync.Mutex
	specs map[string]string
	next  map[string]time.Time
	// 一年内无命中的规格（如 2 月 30 日），记录后跳过并只告警一次。
	skipped map[string]bool
}

// BuildTriggerer 与 BuildService.Trigger 对齐（避免依赖具体类型）。
type BuildTriggerer interface {
	Trigger(ctx context.Context, pipelineID string, trigger model.TriggerType, triggeredBy string, opts TriggerOptions) (*model.Build, error)
}

// NewCronTrigger 创建定时触发器；interval 为扫描周期，<=0 时取默认 30s。
func NewCronTrigger(pipelines PipelineStore, triggerer BuildTriggerer, interval time.Duration) *CronTrigger {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &CronTrigger{
		pipelines: pipelines,
		triggerer: triggerer,
		interval:  interval,
		specs:     map[string]string{},
		next:      map[string]time.Time{},
		skipped:   map[string]bool{},
	}
}

// Start 启动扫描循环；ctx 取消时退出（随进程优雅停机）。
func (c *CronTrigger) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.scan(ctx)
			}
		}
	}()
}

// scan 扫描全部任务，触发到期的定时构建。
func (c *CronTrigger) scan(ctx context.Context) {
	pipelines, err := c.pipelines.List(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	seen := map[string]bool{}
	for _, p := range pipelines {
		seen[p.ID] = true
		if p.Disabled {
			c.forget(p.ID)
			continue
		}
		spec := strings.TrimSpace(p.CronSpec)
		if spec == "" {
			c.forget(p.ID)
			continue
		}
		// 规格变更或首次见到：重新计算计划触发时刻。
		c.mu.Lock()
		changed := c.specs[p.ID] != spec
		c.mu.Unlock()
		if changed {
			c.reschedule(p.ID, spec, now)
			continue
		}
		c.mu.Lock()
		next, planned := c.next[p.ID]
		c.mu.Unlock()
		// 静默模式（准备关机）时不触发，计划时刻顺延到下个周期重试。
		if planned && !now.Before(next) {
			if c.triggerer != nil {
				if _, err := c.triggerer.Trigger(ctx, p.ID, model.TriggerTimer, "timer", TriggerOptions{}); err != nil {
					logger.Errorf("[cron] trigger %s failed: %v", p.ID, err)
				} else {
					logger.Infof("[cron] triggered %s (spec: %s)", p.ID, spec)
				}
			}
			c.reschedule(p.ID, spec, now)
		}
	}
	// 已删除的任务：清理内存状态。
	c.mu.Lock()
	for id := range c.specs {
		if !seen[id] {
			delete(c.specs, id)
			delete(c.next, id)
			delete(c.skipped, id)
		}
	}
	c.mu.Unlock()
}

// reschedule 计算下次触发时刻；规格在一年内无命中时记录并告警一次。
func (c *CronTrigger) reschedule(pipelineID, spec string, now time.Time) {
	nextTime, err := NextCron(spec, pipelineID, now)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.specs[pipelineID] = spec
	if err != nil {
		if !c.skipped[pipelineID] {
			logger.Infof("[cron] pipeline %s spec %q skipped: %v", pipelineID, spec, err)
			c.skipped[pipelineID] = true
		}
		delete(c.next, pipelineID)
		return
	}
	delete(c.skipped, pipelineID)
	c.next[pipelineID] = nextTime
}

// forget 清理任务的内存状态（规格被清空或任务删除）。
func (c *CronTrigger) forget(pipelineID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.specs, pipelineID)
	delete(c.next, pipelineID)
	delete(c.skipped, pipelineID)
}
