// Package health 维护服务与外部依赖（MySQL/etcd）的运行时健康状态：
// 依赖连接失败不退出进程，保持 starting 并周期重试，恢复后自动转 ready。
package health

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
)

// Phase 服务生命周期阶段。
type Phase string

const (
	// PhaseStarting 正在启动：必需依赖尚未全部就绪，readiness 探针返回失败，进程不退出。
	PhaseStarting Phase = "starting"
	// PhaseReady 启动完成：是否真正就绪还取决于必需依赖的实时探活结果。
	PhaseReady Phase = "ready"
	// PhaseStopping 正在停止。
	PhaseStopping Phase = "stopping"
)

// ComponentStatus 单个依赖组件的探活状态，会序列化到 /status 响应。
type ComponentStatus struct {
	// Name 组件名（如 mysql、etcd）。
	Name string `json:"name"`
	// Required 是否为必需组件：必需组件故障时 readiness 探针失败。
	Required bool `json:"required"`
	// Healthy 最近一次探活是否成功。
	Healthy bool `json:"healthy"`
	// Checked 是否已经完成过至少一次探活。
	Checked bool `json:"checked"`
	// LastError 最近一次失败原因。
	LastError string `json:"last_error,omitempty"`
	// CheckedAt 最近一次探活完成时间（Unix 毫秒）。
	CheckedAt int64 `json:"checked_at"`
}

// Snapshot 健康状态快照。
type Snapshot struct {
	// Phase 生命周期阶段。
	Phase Phase `json:"phase"`
	// Ready 是否可接流量：阶段为 ready 且所有必需组件当前健康。
	Ready bool `json:"ready"`
	// StartedAt 进程内 tracker 创建时间（Unix 毫秒）。
	StartedAt int64 `json:"started_at"`
	// UptimeMs 已运行时长（毫秒）。
	UptimeMs int64 `json:"uptime_ms"`
	// Components 各依赖组件状态。
	Components []ComponentStatus `json:"components"`
}

// Checker 依赖探活函数：返回 nil 表示当前可用。
type Checker func(ctx context.Context) error

// probeTimeout 单次探活超时。
const probeTimeout = 3 * time.Second

// failLogEvery 持续失败时每 N 次探活输出一条告警，避免日志刷屏。
const failLogEvery = 10

type component struct {
	name     string
	required bool
	interval time.Duration
	checker  Checker

	mu     sync.Mutex
	status ComponentStatus
	// consecutiveFailures 连续失败次数，用于节流告警。
	consecutiveFailures int
}

// Tracker 健康状态追踪器，线程安全。
type Tracker struct {
	phase     atomic.Value // Phase
	startedAt time.Time

	mu    sync.RWMutex
	comps map[string]*component

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewTracker 创建追踪器，初始阶段为 starting。
func NewTracker() *Tracker {
	t := &Tracker{
		startedAt: time.Now(),
		comps:     make(map[string]*component),
		stopCh:    make(chan struct{}),
	}
	t.phase.Store(PhaseStarting)
	return t
}

// AddComponent 注册依赖组件并启动后台探活（注册后立即探一次，之后按 interval 周期重试）。
// required 组件决定 readiness；非必需组件仅在 /status 展示。
func (t *Tracker) AddComponent(name string, required bool, interval time.Duration, checker Checker) {
	c := &component{
		name:     name,
		required: required,
		interval: interval,
		checker:  checker,
		status:   ComponentStatus{Name: name, Required: required},
	}

	t.mu.Lock()
	t.comps[name] = c
	t.mu.Unlock()

	t.wg.Add(1)
	go t.loop(c)
}

// MarkStopping 标记服务进入停止阶段。
func (t *Tracker) MarkStopping() {
	t.phase.Store(PhaseStopping)
}

// IsReady 是否可接流量：阶段为 ready，且所有必需组件当前健康。
// 在 RLock 下遍历 map 逐个读组件状态，避免每次调用分配 slice；
// 锁顺序安全：probe 先释放组件锁再进入 recompute，无循环等待。
func (t *Tracker) IsReady() bool {
	if t.phase.Load() != PhaseReady {
		return false
	}

	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, c := range t.comps {
		c.mu.Lock()
		healthy, checked, required := c.status.Healthy, c.status.Checked, c.required
		c.mu.Unlock()
		if required && (!checked || !healthy) {
			return false
		}
	}

	return true
}

// Snapshot 返回当前健康状态快照；组件按名称排序，保证输出稳定可对比。
func (t *Tracker) Snapshot() Snapshot {
	t.mu.RLock()
	statuses := make([]ComponentStatus, 0, len(t.comps))
	for _, c := range t.comps {
		c.mu.Lock()
		statuses = append(statuses, c.status)
		c.mu.Unlock()
	}
	t.mu.RUnlock()
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })

	now := time.Now()
	return Snapshot{
		Phase:      t.phase.Load().(Phase),
		Ready:      t.IsReady(),
		StartedAt:  t.startedAt.UnixMilli(),
		UptimeMs:   now.Sub(t.startedAt).Milliseconds(),
		Components: statuses,
	}
}

// Stop 停止全部后台探活 goroutine。
func (t *Tracker) Stop() {
	t.stopOnce.Do(func() {
		close(t.stopCh)
	})
	t.wg.Wait()
}

func (t *Tracker) loop(c *component) {
	defer t.wg.Done()

	// 立即探活一次，避免启动后最长一个 interval 的状态空窗。
	t.probe(c)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-t.stopCh:
			return
		case <-ticker.C:
			t.probe(c)
		}
	}
}

func (t *Tracker) probe(c *component) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	err := c.checker(ctx)
	cancel()

	now := time.Now().UnixMilli()

	c.mu.Lock()
	wasChecked := c.status.Checked
	wasHealthy := c.status.Checked && c.status.Healthy
	c.status.Checked = true
	c.status.CheckedAt = now
	if err != nil {
		c.status.Healthy = false
		c.status.LastError = err.Error()
		c.consecutiveFailures++
	} else {
		c.status.Healthy = true
		c.status.LastError = ""
		c.consecutiveFailures = 0
	}
	healthy := c.status.Healthy
	lastErr := c.status.LastError
	failures := c.consecutiveFailures
	c.mu.Unlock()

	switch {
	case healthy && wasChecked && !wasHealthy:
		logx.Infow("依赖连接已恢复",
			logx.Field("component", c.name),
			logx.Field("required", c.required),
		)
	case healthy && !wasChecked:
		logx.Infow("依赖首次探活成功",
			logx.Field("component", c.name),
			logx.Field("required", c.required),
		)
	case !healthy && wasHealthy:
		if c.required {
			logx.Errorw("依赖连接中断，readiness 已撤销，将持续重试",
				logx.Field("component", c.name),
				logx.Field("error", lastErr),
			)
		} else {
			logx.Errorw("非必需依赖连接中断（不影响 readiness），将持续重试",
				logx.Field("component", c.name),
				logx.Field("error", lastErr),
			)
		}
	case !healthy && failures == 1:
		// 首次探活即失败（启动阶段）：进程不退出，后台持续重试。
		msg := "必需依赖连接失败，服务保持启动中状态（不退出），将持续重试"
		if !c.required {
			msg = "非必需依赖连接失败（不阻断启动与 readiness），将持续重试"
		}
		logx.Errorw(msg,
			logx.Field("component", c.name),
			logx.Field("error", lastErr),
			logx.Field("retry_interval", c.interval.String()),
		)
	case !healthy && failures%failLogEvery == 0:
		logx.Errorw("依赖仍不可用，继续重试",
			logx.Field("component", c.name),
			logx.Field("consecutive_failures", failures),
			logx.Field("error", lastErr),
		)
	}

	t.recompute()
}

// recompute 根据必需组件的探活结果推进 starting -> ready。
func (t *Tracker) recompute() {
	if t.phase.Load() != PhaseStarting {
		// 运行中故障不回退阶段，由 IsReady 依据组件实时健康度反映。
		return
	}

	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, c := range t.comps {
		c.mu.Lock()
		healthy, checked, required := c.status.Healthy, c.status.Checked, c.required
		c.mu.Unlock()
		if required && (!checked || !healthy) {
			return
		}
	}

	if t.phase.CompareAndSwap(PhaseStarting, PhaseReady) {
		logx.Infow("所有必需依赖已就绪，服务进入就绪状态（ready）")
	}
}
