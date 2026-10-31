package pathfinding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/o0olele/detour-go/detour"
)

const QueryTimeout = 15 * time.Second
const MaxLegYards = 4096
const MaxQueryTiles = 1024
const maxTileBytes = 16 << 20

var ErrQueryLimit = errors.New("route exceeds query limits: use legs of at most 4096 yards and at most 1024 tiles")

// ErrSearchLimit: the navmesh search gave up before it reached the destination (a long, winding route)
var ErrSearchLimit = fmt.Errorf("%w: the navmesh search ran out before reaching the destination; use shorter legs (a few hundred yards each)", ErrQueryLimit)

func validateLeg(start, dest Point3D) error {
	for _, p := range []Point3D{start, dest} {
		for _, v := range []float32{p.X, p.Y, p.Z} {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return ErrQueryLimit
			}
		}
		if math.Abs(float64(p.X)) > 17066 || math.Abs(float64(p.Y)) > 17066 {
			return ErrQueryLimit
		}
	}
	dx, dy, dz := float64(start.X-dest.X), float64(start.Y-dest.Y), float64(start.Z-dest.Z)
	if dx*dx+dy*dy+dz*dz > MaxLegYards*MaxLegYards {
		return ErrQueryLimit
	}
	return nil
}

// No waiting goroutine is left behind when a lock waiter is cancelled.
func lockContext(ctx context.Context, try func() bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if try() {
		return nil
	}
	timer := time.NewTicker(time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if err := ctx.Err(); err != nil {
				return err
			}
			if try() {
				return nil
			}
		}
	}
}

// A read already in the kernel is not interruptible, but no further reads are
// issued after cancellation. Map files are local regular files of bounded size.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > 64<<10 {
		p = p[:64<<10]
	}
	return r.r.Read(p)
}

// Finish a search in small batches so cancellation stops work on this query.
func runSlicedSearch(ctx context.Context, status detour.DtStatus, update func(int, *int) detour.DtStatus) (detour.DtStatus, error) {
	for detour.DtStatusInProgress(status) {
		if err := ctx.Err(); err != nil {
			return status, err
		}
		status = update(32, nil)
	}
	if err := ctx.Err(); err != nil {
		return status, err
	}
	return status, nil
}
