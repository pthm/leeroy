package session

import (
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

func leaseRequest(f *movementFixture, task int, priority string, queue, max float64) int {
	return f.control.acquire(task, "owner", "call", priority, queue, max)["id"].(int)
}
func grantedLease(t *testing.T, f *movementFixture, id int) int {
	t.Helper()
	r := f.control.reqs.Result(id)
	if r["ok"] != true {
		t.Fatalf("lease not granted: %v", r)
	}
	return r["data"].(map[string]any)["lease_id"].(int)
}
func TestMovementLeaseRetainedAcrossWalksAndNestedScopes(t *testing.T) {
	f := newMovementFixture(t, false)
	m := f.control
	lease := grantedLease(t, f, leaseRequest(f, 1, "normal", 5, 600))
	nested := grantedLease(t, f, leaseRequest(f, 1, "normal", 5, 600))
	if lease != nested {
		t.Fatal("nested helper acquired a separate lease")
	}
	a := f.start(1, 100)
	f.walking(t, a)
	b := f.start(2, 200)
	f.mu.Lock()
	f.position = types.Position{X: 100}
	f.mu.Unlock()
	m.poll()
	o, q := m.snapshot()
	if o == nil || o.LeaseID != lease || o.RequestID != 0 || o.Phase != "held" || len(q) != 1 {
		t.Fatalf("owner=%v queue=%v", o, q)
	}
	if out := m.stop(2); out.Code != "movement_busy" {
		t.Fatalf("foreign stop=%v", out)
	}
	if out := m.steer(2, func() *script.Outcome { t.Error("foreign steering executed"); return script.Ok(nil) }); out.Code != "movement_busy" {
		t.Fatal(out)
	}
	if out := m.release(1, nested); !out.OK {
		t.Fatal(out)
	}
	o, _ = m.snapshot()
	if o.LeaseID != lease {
		t.Fatal("nested release dropped outer lease")
	}
	if out := m.release(1, lease); !out.OK {
		t.Fatal(out)
	}
	f.walking(t, b)
}
func TestMovementLeasePreemptionCannotResumeOldCallback(t *testing.T) {
	f := newMovementFixture(t, true)
	m := f.control
	normal := grantedLease(t, f, leaseRequest(f, 1, "normal", 5, 600))
	walk := f.start(1, 100)
	<-f.entered
	urgent := grantedLease(t, f, leaseRequest(f, 2, "urgent", 5, 600))
	f.resultCode(t, walk, "preempted")
	data := m.reqs.Result(walk)["data"].(types.MovementOwner)
	if data.TaskID != 2 || data.LeaseID != urgent {
		t.Fatalf("preemption missing new owner: %+v", data)
	}
	recovery := grantedLease(t, f, leaseRequest(f, 3, "recovery", 5, 600))
	if r := m.release(2, urgent); r.Code != "preempted" {
		t.Fatalf("urgent release=%v", r)
	}
	if r := m.release(3, recovery); !r.OK {
		t.Fatal(r)
	}
	// Even after the preemptor is gone, the old callback's movement is refused.
	again := f.start(1, 300)
	f.resultCode(t, again, "preempted")
	if out := m.steer(1, func() *script.Outcome { t.Error("preempted steering executed"); return script.Ok(nil) }); out.Code != "preempted" {
		t.Fatal(out)
	}
	if r := m.release(1, normal); r.Code != "preempted" {
		t.Fatal(r)
	}
	close(f.block)
	next := f.start(1, 400)
	f.walking(t, next)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.published) != 1 || f.published[0] != 400 {
		t.Fatalf("cancelled plans published: %v", f.published)
	}
}
func TestMovementLeaseDeadlineIsNotExtendedByNesting(t *testing.T) {
	f := newMovementFixture(t, false)
	m := f.control
	lease := grantedLease(t, f, leaseRequest(f, 1, "normal", 5, 2))
	f.clock.Add(int64(time.Second))
	grantedLease(t, f, leaseRequest(f, 1, "urgent", 5, 600))
	queued := leaseRequest(f, 2, "normal", 5, 600)
	f.clock.Add(int64(time.Second))
	m.poll()
	grantedLease(t, f, queued)
	if r := m.release(1, lease); r.Code != "lease_timeout" {
		t.Fatal(r)
	}
	if r := m.release(1, lease); r.Code != "lease_timeout" {
		t.Fatal(r)
	}
	o, _ := m.snapshot()
	if o.TaskID != 2 {
		t.Fatal("expired scope released a newer owner")
	}
}
func TestMovementLeaseQueueDeadlinesPriorityAndFIFO(t *testing.T) {
	f := newMovementFixture(t, false)
	m := f.control
	lease := grantedLease(t, f, leaseRequest(f, 1, "recovery", 5, 600))
	normal := leaseRequest(f, 2, "normal", 5, 600)
	urgent1 := leaseRequest(f, 3, "urgent", 5, 600)
	urgent2 := leaseRequest(f, 4, "urgent", 5, 600)
	immediate := leaseRequest(f, 5, "normal", 0, 600)
	f.resultCode(t, immediate, "movement_busy")
	short := leaseRequest(f, 6, "normal", 0.1, 600)
	f.clock.Add(int64(100 * time.Millisecond))
	m.poll()
	f.resultCode(t, short, "movement_busy")
	m.release(1, lease)
	l3 := grantedLease(t, f, urgent1)
	m.release(3, l3)
	l4 := grantedLease(t, f, urgent2)
	m.release(4, l4)
	grantedLease(t, f, normal)
}
func TestMovementLeasePauseAndInvalidOptions(t *testing.T) {
	f := newMovementFixture(t, false)
	m := f.control
	lease := grantedLease(t, f, leaseRequest(f, 1, "normal", 5, 600))
	m.cancelTask(1, "task_paused")
	f.resultCode(t, f.start(1, 100), "task_paused")
	if r := m.release(1, lease); r.Code != "task_paused" {
		t.Fatal(r)
	}
	for _, p := range []struct {
		priority string
		q, max   float64
	}{{"bad", 5, 600}, {"normal", -1, 600}, {"normal", 61, 600}, {"normal", 5, 601}, {"normal", 5, 0}} {
		id := leaseRequest(f, 1, p.priority, p.q, p.max)
		f.resultCode(t, id, "invalid_options")
	}
	id := leaseRequest(f, 0, "normal", 5, 600)
	f.resultCode(t, id, "task_required")
}
