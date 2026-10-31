package session

import "testing"

func TestPortalNearInstanceEntrance(t *testing.T) {
	// the Deadmines: the server reports the corpse at (-11207.8, 1681.2, 60.1), the ground above the tunnel
	triggers := []areaTrigger{{id: 99, mapID: 0, x: -11150, y: 1681}, {id: 78, mapID: 0, x: -11208.5, y: 1685.3, z: 25.8, r: 7}, {id: 5, mapID: 1, x: -11207, y: 1681}}
	if p, ok := portalNear(triggers, 0, 36, -11207.8, 1681.2); !ok || p.id != 78 {
		t.Fatalf("portal = %+v %v", p, ok)
	}
	if _, ok := portalNear(triggers, 0, 36, -10000, 1681.2); ok {
		t.Fatal("found a portal far from the entrance")
	}
	// two portals yards apart into different instances (Molten Core's inside Blackrock Depths): the corpse's instance
	// picks, not distance
	near := []areaTrigger{{id: 2886, mapID: 230, x: 1098, y: -466.5, r: 20}, {id: 1466, mapID: 230, x: 1090, y: -466.5, r: 13}}
	if p, ok := portalNear(near, 230, 409, 1091, -466.5); !ok || p.id != 2886 {
		t.Fatalf("molten core portal = %+v %v", p, ok)
	}
}
