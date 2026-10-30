package world

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func flatRouteSource() routeSource {
	return routeSource{
		sample:  func(uint32, float64, float64) (float64, float64, uint16) { return 0, math.NaN(), 0 },
		roadAt:  func(uint32, float64, float64) uint8 { return 0 },
		roads:   func(context.Context, float64, float64, float64, float64, float64) [][][2]float64 { return nil },
		threats: func(context.Context, float64, float64, float64, float64, float64, bool) []threat { return nil },
	}
}
func routeFixture() routeRequest {
	return routeRequest{From: [3]float64{0, 0, 20}, To: [3]float64{80, 0, -20}, Map: 530, Level: 7, ExplicitZ: true}
}
func assertRouteCode(t *testing.T, r *obj, want string) {
	t.Helper()
	if r.get("status") != "failed" || r.get("reason") != want {
		t.Fatalf("wanted %s: %s", want, toJSON(r))
	}
	if e, ok := r.get("error").(*obj); !ok || e.get("code") != want {
		t.Fatalf("missing error code: %s", toJSON(r))
	}
	if r.get("waypoints") != nil {
		t.Fatal("failed planning published a usable-looking route")
	}
}
func TestTerrainPlanPreservesRequestedFloors(t *testing.T) {
	req := routeFixture()
	plan := planRoute(context.Background(), req, flatRouteSource())
	if plan.get("status") != "unverified" || plan.get("reason") != "navmesh_validation_required" || plan.get("validation_required") != true {
		t.Fatalf("%s", toJSON(plan))
	}
	points := plan.get("waypoints").([][3]float64)
	if points[0] != req.From || points[len(points)-1] != req.To {
		t.Fatalf("floors replaced: %v", points)
	}
	gaps := plan.get("terrain_endpoint_gaps").(*obj)
	if gaps.n("from") != 20 || gaps.n("destination") != 20 {
		t.Fatalf("%s", toJSON(gaps))
	}
	if plan.n("distance") < 80 || plan.n("map") != 530 {
		t.Fatalf("%s", toJSON(plan))
	}
	if !json.Valid(toJSON(plan)) {
		t.Fatal("invalid JSON")
	}
}
func TestTwoDimensionalPlanUsesTerrainWithoutCertification(t *testing.T) {
	req := routeFixture()
	req.ExplicitZ = false
	src := flatRouteSource()
	src.sample = func(uint32, float64, float64) (float64, float64, uint16) { return 42, math.NaN(), 0 }
	plan := planRoute(context.Background(), req, src)
	points := plan.get("waypoints").([][3]float64)
	if plan.get("status") != "unverified" || points[0][2] != 42 || points[len(points)-1][2] != 42 || plan.get("terrain_endpoint_gaps") != nil {
		t.Fatalf("%s", toJSON(plan))
	}
}
func TestTerrainMissingAndDisconnectedAreDistinct(t *testing.T) {
	req := routeFixture()
	req.From[0] = -40
	req.To[0] = 40
	src := flatRouteSource()
	src.sample = func(uint32, float64, float64) (float64, float64, uint16) { return math.NaN(), math.NaN(), 0 }
	assertRouteCode(t, planRoute(context.Background(), req, src), "not_available")
	src.sample = func(_ uint32, x, y float64) (float64, float64, uint16) {
		if x >= 0 {
			return 1000, math.NaN(), 0
		}
		return 0, math.NaN(), 0
	}
	assertRouteCode(t, planRoute(context.Background(), req, src), "terrain_unreachable")
}
func TestThreatCostCannotMakeFlatTerrainUnreachable(t *testing.T) {
	src := flatRouteSource()
	src.threats = func(context.Context, float64, float64, float64, float64, float64, bool) []threat {
		return []threat{{entry: 1, name: "Elite", level: 20, elite: true, x: 0, y: 0}}
	}
	plan := planRoute(context.Background(), routeFixture(), src)
	if plan.get("status") != "unverified" {
		t.Fatalf("%s", toJSON(plan))
	}
	exposure := plan.get("exposure").(*obj)
	if exposure.get("floor_checked") != false || exposure.get("los_checked") != false || exposure.get("source") != "static_spawns_2d" {
		t.Fatalf("%s", toJSON(exposure))
	}
}
func TestTerrainCancellationStopsSampling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := flatRouteSource()
	calls := 0
	src.sample = func(uint32, float64, float64) (float64, float64, uint16) { calls++; cancel(); return 0, math.NaN(), 0 }
	assertRouteCode(t, planRoute(ctx, routeFixture(), src), "cancelled")
	if calls != 1 {
		t.Fatalf("continued sampling after cancellation: %d", calls)
	}
}
func TestTerrainDeadlineReachesDataSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	src := flatRouteSource()
	entered := false
	src.roads = func(ctx context.Context, _, _, _, _, _ float64) [][][2]float64 {
		entered = true
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 20*time.Millisecond {
			t.Error("source did not inherit query deadline")
		}
		<-ctx.Done()
		panic(qerr{ctx.Err()})
	}
	assertRouteCode(t, planRoute(ctx, routeFixture(), src), "route_timeout")
	if !entered {
		t.Fatal("did not reach data query")
	}
	// The cancellation released admission; a later query can use the planner.
	if got := planRoute(context.Background(), routeFixture(), flatRouteSource()); got.get("status") != "unverified" {
		t.Fatalf("%s", toJSON(got))
	}
}
func TestTerrainDeadlineIncludesAdmission(t *testing.T) {
	routeSlot <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	src := flatRouteSource()
	src.sample = func(uint32, float64, float64) (float64, float64, uint16) {
		t.Error("sampled without admission")
		return 0, 0, 0
	}
	plan := planRoute(ctx, routeFixture(), src)
	<-routeSlot
	assertRouteCode(t, plan, "route_timeout")
}
func TestTerrainLimitsBeforeIOAndOutputBound(t *testing.T) {
	src := flatRouteSource()
	src.sample = func(uint32, float64, float64) (float64, float64, uint16) {
		t.Error("sampled an invalid query")
		return 0, 0, 0
	}
	req := routeFixture()
	req.To[0] = 5000
	assertRouteCode(t, planRoute(context.Background(), req, src), "route_limit")
	req = routeFixture()
	req.From[0] = math.NaN()
	assertRouteCode(t, planRoute(context.Background(), req, src), "invalid_arguments")
	req = routeFixture()
	req.From[2] = math.MaxFloat64
	assertRouteCode(t, planRoute(context.Background(), req, src), "invalid_arguments")
	req = routeFixture()
	req.Map = -1
	assertRouteCode(t, planRoute(context.Background(), req, src), "invalid_arguments")
	req = routeFixture()
	req.From[2], req.To[2] = 0, 0
	req.To[0] = 4096
	assertRouteCode(t, planRoute(context.Background(), req, flatRouteSource()), "route_limit")
}
func TestRouteCLIRejectsAmbiguousAndMalformedInputs(t *testing.T) {
	cases := [][]string{
		{"route"}, {"route", "0", "0", "1", "1", "5", "--level", "7"},
		{"route", "0", "0", "0", "1", "1", "0", "0", "--map", "0", "--level", "7"},
		{"route", "0", "0", "1", "1", "--level", "7", "--typo", "3"},
		{"route", "0", "0", "1", "1", "--level", "7", "--map", "-1"},
		{"route", "0", "0", "1", "1", "--level", "7", "--map", "0.5"},
		{"route", "0", "0", "1", "1"}, {"route", "0", "0", "1", "1", "--level", "0"},
		{"route", "0", "0", "NaN", "1", "--level", "7"},
		{"route", "0", "0", "0", "5000", "0", "0", "0", "--level", "7"},
	}
	for _, args := range cases {
		var out bytes.Buffer
		if code := Run(args, &out); code != 1 {
			t.Fatalf("%v: code %d: %s", args, code, out.String())
		}
		var parsed map[string]any
		if err := json.Unmarshal(out.Bytes(), &parsed); err != nil || parsed["status"] != "failed" {
			t.Fatalf("%v: %s", args, out.String())
		}
		if _, ok := parsed["error"].(map[string]any); !ok {
			t.Fatalf("unstructured error: %s", out.String())
		}
	}
	// --horde before coordinates must not consume the first coordinate as its value.
	var out bytes.Buffer
	Run([]string{"route", "--horde", "0", "0", "5000", "0", "--level", "7"}, &out)
	if !strings.Contains(out.String(), `"route_limit"`) {
		t.Fatalf("boolean flag consumed a coordinate: %s", out.String())
	}
}

func TestRouteCLIExplicitForms(t *testing.T) {
	two := parseRouteRequest([]string{"1", "2", "3", "4"}, map[string]string{"level": "7", "map": "530", "horde": ""})
	if two.ExplicitZ || two.From != [3]float64{1, 2, 0} || two.To != [3]float64{3, 4, 0} || two.Map != 530 || !two.Horde {
		t.Fatalf("%+v", two)
	}
	three := parseRouteRequest([]string{"1", "2", "-30", "4", "5", "60", "571"}, map[string]string{"level": "8"})
	if !three.ExplicitZ || three.From != [3]float64{1, 2, -30} || three.To != [3]float64{4, 5, 60} || three.Map != 571 || three.Horde {
		t.Fatalf("%+v", three)
	}
}
