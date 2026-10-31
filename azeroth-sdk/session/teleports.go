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

// portalMaps: the map each cross-map area trigger takes you to (areatrigger_teleport), for finding the portal into the
// instance a corpse is in: several can be yards apart (Blackrock Mountain's, the Scarlet Monastery wings)
var portalMaps = map[uint32]uint32{
	45:   189,
	78:   36,
	101:  34,
	107:  35,
	109:  0,
	119:  0,
	121:  0,
	145:  33,
	194:  0,
	226:  1,
	228:  43,
	242:  1,
	244:  47,
	257:  48,
	259:  1,
	286:  70,
	288:  0,
	322:  0,
	324:  90,
	442:  129,
	444:  1,
	446:  109,
	448:  0,
	503:  0,
	523:  90,
	525:  0,
	602:  0,
	604:  0,
	606:  0,
	608:  0,
	610:  189,
	612:  189,
	614:  189,
	882:  0,
	902:  70,
	922:  1,
	924:  209,
	1064: 1,
	1466: 230,
	1468: 229,
	1470: 0,
	1472: 0,
	2068: 0,
	2166: 0,
	2171: 0,
	2173: 369,
	2175: 369,
	2214: 329,
	2216: 329,
	2217: 329,
	2221: 0,
	2226: 1,
	2230: 389,
	2406: 0,
	2407: 0,
	2408: 0,
	2409: 0,
	2410: 0,
	2411: 0,
	2530: 1,
	2534: 0,
	2547: 0,
	2548: 0,
	2549: 0,
	2567: 289,
	2568: 0,
	2606: 0,
	2608: 0,
	2848: 249,
	2886: 409,
	2890: 0,
	3126: 1,
	3131: 1,
	3133: 349,
	3134: 349,
	3183: 429,
	3184: 429,
	3185: 429,
	3186: 429,
	3187: 429,
	3189: 429,
	3190: 1,
	3191: 1,
	3193: 1,
	3194: 1,
	3195: 1,
	3196: 1,
	3197: 1,
	3528: 409,
	3529: 409,
	3726: 469,
	3728: 229,
	3928: 309,
	3930: 0,
	3948: 0,
	3949: 0,
	4006: 1,
	4008: 509,
	4010: 531,
	4012: 1,
	4131: 532,
	4135: 532,
	4145: 530,
	4147: 530,
	4149: 530,
	4150: 543,
	4151: 540,
	4152: 542,
	4153: 544,
	4297: 530,
	4304: 530,
	4319: 534,
	4320: 269,
	4321: 560,
	4322: 1,
	4323: 1,
	4324: 1,
	4352: 0,
	4354: 530,
	4363: 546,
	4364: 545,
	4365: 547,
	4366: 530,
	4367: 530,
	4379: 530,
	4386: 0,
	4397: 530,
	4399: 530,
	4401: 530,
	4403: 530,
	4404: 558,
	4405: 557,
	4406: 556,
	4407: 555,
	4409: 530,
	4416: 548,
	4418: 530,
	4436: 0,
	4455: 530,
	4457: 530,
	4459: 530,
	4461: 530,
	4467: 553,
	4468: 552,
	4469: 554,
	4470: 550,
	4487: 1,
	4520: 0,
	4534: 530,
	4535: 565,
	4598: 564,
	4612: 530,
	4614: 530,
	4619: 530,
	4738: 568,
	4739: 530,
	4741: 571,
	4743: 571,
	4745: 574,
	4747: 575,
	4885: 530,
	4887: 585,
	4889: 580,
	4891: 530,
	4981: 571,
	4983: 576,
	4998: 600,
	5000: 571,
	5001: 571,
	5010: 599,
	5012: 571,
	5091: 571,
	5093: 602,
	5113: 571,
	5115: 571,
	5117: 601,
	5148: 1,
	5150: 595,
	5181: 1,
	5191: 533,
	5192: 533,
	5193: 533,
	5194: 533,
	5196: 571,
	5197: 571,
	5198: 571,
	5199: 571,
	5205: 604,
	5206: 604,
	5209: 608,
	5211: 571,
	5213: 571,
	5215: 619,
	5231: 571,
	5233: 571,
	5235: 571,
	5241: 571,
	5243: 615,
	5246: 578,
	5258: 624,
	5262: 571,
	5276: 571,
	5277: 571,
	5290: 616,
	5379: 603,
	5381: 571,
	5503: 649,
	5505: 650,
	5508: 571,
	5510: 571,
	5635: 632,
	5636: 668,
	5637: 658,
	5642: 571,
	5643: 571,
	5646: 571,
	5668: 571,
	5670: 631,
	5683: 668,
	5688: 658,
	5869: 724,
	5872: 571,
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
