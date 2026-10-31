package session

import (
	"context"

	"github.com/pthm/leeroy/azeroth-sdk/navigation"
)

type routeKey struct {
	mapID    uint32
	from, to navigation.Point3D
}

func copyPath(p *navigation.PathResult) *navigation.PathResult {
	return p.Clone()
}

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
	key := routeKey{mapID, from, to}
	if cached := b.routeCache[key]; cached != nil {
		return copyPath(cached), nil
	}
	result, err := b.nav.FindPathContext(ctx, mapID, from, to)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err == nil && result != nil && result.Found && !result.Partial && len(result.Points) > 0 && dist3(from, result.Points[0]) <= 3 && dist3(to, result.Points[len(result.Points)-1]) <= 3 {
		if b.routeCache == nil || len(b.routeCache) >= 128 {
			b.routeCache = map[routeKey]*navigation.PathResult{}
		}
		b.routeCache[key] = copyPath(result)
	}
	return result, err
}
