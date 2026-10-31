package session

import "testing"

func TestPortalNearInstanceEntrance(t *testing.T) {
	// the Deadmines: the server reports the corpse at (-11207.8, 1681.2, 60.1), the ground above the tunnel
	triggers := []areaTrigger{{id: 99, mapID: 0, x: -11150, y: 1681}, {id: 78, mapID: 0, x: -11208.5, y: 1685.3, z: 25.8, r: 7}, {id: 5, mapID: 1, x: -11207, y: 1681}}
	if p, ok := portalNear(triggers, 0, -11207.8, 1681.2); !ok || p.id != 78 {
		t.Fatalf("portal = %+v %v", p, ok)
	}
	if _, ok := portalNear(triggers, 0, -10000, 1681.2); ok {
		t.Fatal("found a portal far from the entrance")
	}
}
