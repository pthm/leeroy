package session

import (
	"context"
	"math"

	"github.com/pthm/leeroy/azeroth-sdk/navigation"
)

// Same-map teleports: area triggers that move you within the map you're on, with no loading screen (the Wizard's
// Sanctum in Stormwind, Darnassus and Rut'theran, Ebon Hold's floors). The client knows only where a trigger is
// (AreaTrigger.dbc); where it sends you is the server's areatrigger_teleport, copied here (open-world maps only).
var sameMapTeleports = []struct {
	trigger    uint32
	name       string
	tx, ty, tz float32
}{
	{527, "Teddrassil - Ruth Theran", 8786.36, 967.445, 30.197},
	{542, "Teddrassil - Darnassus", 9945.13, 2616.89, 1316.46},
	{702, "Stormwind - Wizard Sanctum Room", -9015.76, 874.94, 148.62},
	{704, "Stormwind - Wizard Sanctum Tower Portal", -9018.56, 887.664, 29.63},
	{943, "Leap of Faith - End of fall", -5187.47, -2804.32, -8.375},
	{4523, "Socrethar's Seat To Mainland", 4773.76, 3451.27, 105.15},
	{4561, "Invasion Point, Cataclysm (Return Point)", -3278.63, 2831.31, 123.01},
	{4562, "Invasion Point, Cataclysm (Return Point)", -3278.63, 2831.31, 123.01},
	{5051, "Zul'drak - Voltarus, base floor -> middle", 6159.16, -2028.6, 408.168},
	{5061, "Zul'drak - Voltarus, middle floor -> base", 6175.6, -2008.78, 245.255},
	{5079, "Zul'drak - Voltarus, middle floor -> top", 6242.67, -1972.1, 484.783},
	{5080, "Zul'drak - Voltarus, upper floor -> middle", 6159.16, -2028.6, 408.168},
	{5127, "Ebon Hold (E.K.) Top Return Target", 2462.76, -5598.19, 550.22},
	{5128, "Ebon Hold (E.K.) Top West Target", 2472.67, -5530.1, 420.642},
	{5129, "Ebon Hold (E.K.) Top Return Target", 2462.76, -5598.19, 550.22},
	{5130, "Ebon Hold (E.K.) Top South Target", 2397.37, -5581.06, 420.643},
	{5131, "Ebon Hold (E.K.) Top Return Target", 2462.76, -5598.19, 550.22},
	{5132, "Ebon Hold (E.K.) Top North Target", 2522.45, -5605.7, 420.642},
	{5133, "Ebon Hold (E.K.) Top Return Target", 2462.76, -5598.19, 550.22},
	{5134, "Ebon Hold (E.K.) Top East Target", 2448.02, -5655.82, 420.643},
	{5135, "Ebon Hold (E.K.) Middle -> Bottom Target", 2435.77, -5610.34, 366.82},
	{5136, "Ebon Hold (E.K.) Middle -> Bottom Target", 2435.77, -5610.34, 366.82},
	{5137, "Ebon Hold (E.K.) Bottom -> Middle Target", 2472.9, -5530.64, 420.643},
	{5138, "Ebon Hold (E.K.) Bottom -> Ground Target", 2415.16, -5733.04, 153.922},
	{5273, "Dalaran Well teleporter", 5878.56, 666.423, 615.294},
	{5338, "Naxxanar Top -> Down", 3733.68, 3563.25, 290.812},
}

type portalVia struct {
	Name        string
	Enter, Exit navigation.Point3D
}

// viaPortal: a same-map teleport that takes you from to to: a walkable point inside its trigger from here, and a
// complete route from where it puts you. Portals landing over 300 yd from to aren't tried.
func (b *Bot) viaPortal(ctx context.Context, m uint32, from, to navigation.Point3D) (portalVia, bool) {
	for _, t := range sameMapTeleports {
		exit := navigation.Point3D{X: t.tx, Y: t.ty, Z: t.tz}
		if math.Hypot(float64(exit.X-to.X), float64(exit.Y-to.Y)) > 300 {
			continue
		}
		var trig *areaTrigger
		for i := range b.areaTriggers {
			if b.areaTriggers[i].id == t.trigger && b.areaTriggers[i].mapID == m {
				trig = &b.areaTriggers[i]
			}
		}
		if trig == nil {
			continue
		}
		after, err := b.findPath(ctx, m, exit, to)
		if err != nil || after == nil || !after.Found || after.Partial || len(after.Points) == 0 {
			continue
		}
		// the trigger's centre is in the air, up to a few yards over the floor (the Wizard's Sanctum's basement one
		// 3.9): walk to the floor under it, if that's well inside
		centre := navigation.Point3D{X: trig.x, Y: trig.y, Z: trig.z}
		for _, down := range []float32{0, 2, 4} {
			before, err := b.findPath(ctx, m, from, navigation.Point3D{X: centre.X, Y: centre.Y, Z: centre.Z - down})
			if err != nil || before == nil || !before.Found || before.Partial || len(before.Points) == 0 {
				continue
			}
			if enter := before.Points[len(before.Points)-1]; dist3(enter, centre) < float64(trig.radius())*0.8 {
				return portalVia{t.name, enter, exit}, true
			}
		}
	}
	return portalVia{}, false
}
