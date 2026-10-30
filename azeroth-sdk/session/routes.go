package session

import (
	"context"

	"github.com/pthm/leeroy/azeroth-sdk/navigation"
)

// A session performs at most one navmesh search at a time. Cancellation also
// bounds waiting for that slot, without leaving a lock-waiter goroutine behind.
func (b *Bot) findPath(ctx context.Context, mapID uint32, from, to navigation.Point3D) (*navigation.PathResult, error) {
	b.routeOnce.Do(func() { b.routeGate = make(chan struct{}, 1) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case b.routeGate <- struct{}{}:
	}
	defer func() { <-b.routeGate }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return b.nav.FindPathContext(ctx, mapID, from, to)
}
