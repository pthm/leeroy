package session

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pthm/leeroy/azeroth-sdk/navigation"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

type cancellableNavigator struct {
	blockedMovementNavigator
	calls atomic.Int32
}

func (n *cancellableNavigator) FindPathContext(ctx context.Context, _ uint32, a, b navigation.Point3D) (*navigation.PathResult, error) {
	if n.calls.Add(1) == 1 {
		close(n.entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &navigation.PathResult{Found: true, Points: []navigation.Point3D{a, b}}, nil
}

func TestMovementCancellationReleasesPlanner(t *testing.T) {
	b := testBot()
	defer b.lua.Close()
	n := &cancellableNavigator{blockedMovementNavigator: blockedMovementNavigator{entered: make(chan struct{})}}
	b.nav = n
	b.setupMovementRequests()
	first := b.moves.start(1, "first", "", types.Position{X: 100})["id"].(int)
	select {
	case <-n.entered:
	case <-time.After(time.Second):
		t.Fatal("planner did not start")
	}
	second := b.moves.start(2, "second", "", types.Position{X: 200})["id"].(int)
	b.moves.cancel(first)
	waitMovement(t, func() bool {
		o, _ := b.moves.snapshot()
		return o != nil && o.RequestID == second && o.Phase == "walking"
	})
	if !b.IsMoving() || n.calls.Load() != 2 {
		t.Fatal("next owner did not acquire the real movement controller")
	}
}

func TestRouteGateCancellation(t *testing.T) {
	b := testBot()
	defer b.lua.Close()
	n := &cancellableNavigator{blockedMovementNavigator: blockedMovementNavigator{entered: make(chan struct{})}}
	b.nav = n
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := b.findPath(ctx, 0, navigation.Point3D{}, navigation.Point3D{}); done <- err }()
	<-n.entered
	wait, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stop()
	if _, err := b.findPath(wait, 0, navigation.Point3D{}, navigation.Point3D{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting route: %v", err)
	}
	if n.calls.Load() != 1 {
		t.Fatal("session ran simultaneous queries")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("active route: %v", err)
	}
	if _, err := b.findPath(context.Background(), 0, navigation.Point3D{}, navigation.Point3D{}); err != nil {
		t.Fatal(err)
	}
}

func TestReachBudgetExpiryRemainsUnknown(t *testing.T) {
	b := testBot()
	defer b.lua.Close()
	n := &cancellableNavigator{blockedMovementNavigator: blockedMovementNavigator{entered: make(chan struct{})}}
	b.nav = n
	reach := b.newReach()
	reach.deadline = time.Now().Add(10 * time.Millisecond)
	_, distance, reachable := reach.of(1, 10, 0, 0, 0)
	if distance != nil || reachable != nil || len(b.pathCache) != 0 {
		t.Fatal("expired route became an unreachable cache entry")
	}
	_, distance, reachable = b.newReach().of(1, 10, 0, 0, 0)
	if distance == nil || reachable == nil || !*reachable || n.calls.Load() != 2 {
		t.Fatal("subsequent route did not recover")
	}
}
