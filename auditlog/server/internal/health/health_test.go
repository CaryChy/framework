package health

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// controlledChecker 可在运行期切换健康结果的探活函数，便于模拟依赖抖动。
type controlledChecker struct {
	mu      sync.Mutex
	healthy bool
}

func (c *controlledChecker) set(h bool) { c.mu.Lock(); c.healthy = h; c.mu.Unlock() }
func (c *controlledChecker) check(context.Context) error {
	c.mu.Lock()
	h := c.healthy
	c.mu.Unlock()
	if h {
		return nil
	}
	return errors.New("unhealthy")
}

// waitForChecked 轮询直到名为 name 的组件完成至少一次探活，超时则失败。
func waitForChecked(t *testing.T, tr *Tracker, name string) ComponentStatus {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range tr.Snapshot().Components {
			if s.Name == name && s.Checked {
				return s
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("组件 %s 未在超时内完成探活", name)
	return ComponentStatus{}
}

// waitForReady 轮询直到 IsReady 返回 want，超时则失败。
func waitForReady(t *testing.T, tr *Tracker, want bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if tr.IsReady() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("IsReady 未在超时内变为 %v", want)
}

func TestTracker_InitialStartingNotReady(t *testing.T) {
	tr := NewTracker()
	defer tr.Stop()
	if tr.IsReady() {
		t.Fatal("新建 tracker 不应就绪")
	}
	if tr.Snapshot().Phase != PhaseStarting {
		t.Fatalf("初始阶段应为 starting，实际 %s", tr.Snapshot().Phase)
	}
}

func TestTracker_RequiredHealthyBecomesReady(t *testing.T) {
	tr := NewTracker()
	defer tr.Stop()
	c := &controlledChecker{healthy: true}
	tr.AddComponent("mysql", true, time.Millisecond, c.check)

	waitForChecked(t, tr, "mysql")
	waitForReady(t, tr, true)

	if got := tr.Snapshot().Phase; got != PhaseReady {
		t.Fatalf("阶段应为 ready，实际 %s", got)
	}
}

func TestTracker_RequiredUnhealthyStaysNotReady(t *testing.T) {
	tr := NewTracker()
	defer tr.Stop()
	c := &controlledChecker{healthy: false}
	tr.AddComponent("mysql", true, time.Millisecond, c.check)

	s := waitForChecked(t, tr, "mysql")
	if s.Healthy {
		t.Fatal("故障组件不应标记健康")
	}
	if tr.IsReady() {
		t.Fatal("必需组件故障时不应就绪")
	}
	if tr.Snapshot().Phase != PhaseStarting {
		t.Fatal("必需组件未就绪时阶段应保持 starting")
	}
}

func TestTracker_RequiredRecoversToReady(t *testing.T) {
	tr := NewTracker()
	defer tr.Stop()
	c := &controlledChecker{healthy: false}
	tr.AddComponent("mysql", true, time.Millisecond, c.check)
	waitForChecked(t, tr, "mysql")
	if tr.IsReady() {
		t.Fatal("故障期不应就绪")
	}

	// 依赖恢复
	c.set(true)
	waitForReady(t, tr, true)
	if tr.Snapshot().Phase != PhaseReady {
		t.Fatal("恢复后阶段应为 ready")
	}
}

func TestTracker_RequiredFailsAfterReady_IsReadyFalse(t *testing.T) {
	tr := NewTracker()
	defer tr.Stop()
	c := &controlledChecker{healthy: true}
	tr.AddComponent("mysql", true, time.Millisecond, c.check)
	waitForReady(t, tr, true)

	// 运行期必需组件故障：阶段不回退，但 IsReady 应为 false。
	c.set(false)
	waitForReady(t, tr, false)
	if tr.Snapshot().Phase != PhaseReady {
		t.Fatalf("运行期故障不应回退阶段，实际 %s", tr.Snapshot().Phase)
	}
}

func TestTracker_NonRequiredFailureDoesNotBlockReady(t *testing.T) {
	tr := NewTracker()
	defer tr.Stop()
	ok := &controlledChecker{healthy: true}
	bad := &controlledChecker{healthy: false}
	tr.AddComponent("mysql", true, time.Millisecond, ok.check)
	tr.AddComponent("etcd", false, time.Millisecond, bad.check)

	waitForChecked(t, tr, "mysql")
	waitForChecked(t, tr, "etcd")
	waitForReady(t, tr, true)
}

func TestTracker_MultipleRequiredAllMustBeHealthy(t *testing.T) {
	tr := NewTracker()
	defer tr.Stop()
	a := &controlledChecker{healthy: true}
	b := &controlledChecker{healthy: false}
	tr.AddComponent("a", true, time.Millisecond, a.check)
	tr.AddComponent("b", true, time.Millisecond, b.check)

	waitForChecked(t, tr, "a")
	waitForChecked(t, tr, "b")
	if tr.IsReady() {
		t.Fatal("两个必需组件之一故障时不应就绪")
	}

	b.set(true)
	waitForReady(t, tr, true)
}

func TestTracker_MarkStoppingNotReady(t *testing.T) {
	tr := NewTracker()
	defer tr.Stop()
	c := &controlledChecker{healthy: true}
	tr.AddComponent("mysql", true, time.Millisecond, c.check)
	waitForReady(t, tr, true)

	tr.MarkStopping()
	if tr.IsReady() {
		t.Fatal("MarkStopping 后不应就绪")
	}
	if tr.Snapshot().Phase != PhaseStopping {
		t.Fatalf("阶段应为 stopping，实际 %s", tr.Snapshot().Phase)
	}
}

func TestTracker_SnapshotContainsComponents(t *testing.T) {
	tr := NewTracker()
	defer tr.Stop()
	c := &controlledChecker{healthy: true}
	tr.AddComponent("mysql", true, time.Millisecond, c.check)
	waitForChecked(t, tr, "mysql")

	snap := tr.Snapshot()
	if len(snap.Components) != 1 {
		t.Fatalf("快照应含 1 个组件，实际 %d", len(snap.Components))
	}
	comp := snap.Components[0]
	if comp.Name != "mysql" || !comp.Required || !comp.Healthy || !comp.Checked {
		t.Fatalf("组件状态异常: %+v", comp)
	}
	if snap.StartedAt <= 0 || snap.UptimeMs < 0 {
		t.Fatalf("时间字段异常: started_at=%d uptime=%d", snap.StartedAt, snap.UptimeMs)
	}
}

// TestTracker_SnapshotComponentsSorted 快照中的组件必须按名称排序，保证 /status 输出稳定。
func TestTracker_SnapshotComponentsSorted(t *testing.T) {
	tr := NewTracker()
	defer tr.Stop()
	c := &controlledChecker{healthy: true}
	tr.AddComponent("zeta", false, time.Millisecond, c.check)
	tr.AddComponent("mysql", true, time.Millisecond, c.check)
	tr.AddComponent("etcd", false, time.Millisecond, c.check)
	waitForChecked(t, tr, "mysql")

	snap := tr.Snapshot()
	if len(snap.Components) != 3 {
		t.Fatalf("快照应含 3 个组件，实际 %d", len(snap.Components))
	}
	names := []string{snap.Components[0].Name, snap.Components[1].Name, snap.Components[2].Name}
	want := []string{"etcd", "mysql", "zeta"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("组件未按名称排序: %v，期望 %v", names, want)
		}
	}
}
