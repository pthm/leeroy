package run

import (
	"context"
	"fmt"
	"github.com/pthm/leeroy/azbench/config"
	"github.com/pthm/leeroy/azeroth-sdk/navigation"
	"strings"
	"testing"
)

func TestScenarioPreflightQuestCaveAndHogger(t *testing.T) {
	for _, quest := range []int{62, 47, 176} {
		t.Run(fmt.Sprint(quest), func(t *testing.T) {
			s := &config.Scenario{Name: "fixture", Goal: fmt.Sprintf("quest:%d", quest), Level: 5, Minutes: 60, Start: &config.Place{}, Agents: []config.Agent{{Name: "mage", Race: "human", Class: "mage"}}}
			floors := 0
			source := preflightSource{path: func(_ context.Context, _ uint32, a, b navigation.Point3D) (*navigation.PathResult, error) {
				floors++
				return &navigation.PathResult{Found: true, Points: []navigation.Point3D{a, b}}, nil
			}, query: func(_ context.Context, q string, args ...any) ([]map[string]string, error) {
				switch {
				case strings.Contains(q, "q.MinLevel"):
					return []map[string]string{{"MinLevel": "1"}}, nil
				case strings.Contains(q, "estimated_health") && quest == 176:
					return []map[string]string{{"entry": "448", "name": "Hogger", "maxlevel": "11", "rank": "1", "estimated_health": "666"}}, nil
				case strings.Contains(q, "areatrigger_involvedrelation") && quest == 47:
					return []map[string]string{{"entry": "123", "map": "0", "x": "1", "y": "2", "z": "-30"}}, nil
				case strings.Contains(q, "queststarter") || strings.Contains(q, "questender"):
					return []map[string]string{{"entry": "240", "spawns": "1"}}, nil
				default:
					return []map[string]string{}, nil
				}
			}}
			report := checkScenario(context.Background(), s, source)
			warning := false
			for _, f := range report.Findings {
				if f.Check == "level_difference" {
					warning = true
				}
				if f.Check == "quest_eligibility" && f.Status != "verified" {
					t.Fatal(f)
				}
			}
			if warning != (quest == 176) {
				t.Fatal("combat evidence missing or invented", report)
			}
			if quest == 47 && floors != 2 {
				t.Fatal("cave floor not checked")
			}
		})
	}
}
func TestPreflightUnavailableDoesNotCertifyFloor(t *testing.T) {
	src := preflightSource{query: func(context.Context, string, ...any) ([]map[string]string, error) { return nil, fmt.Errorf("offline") }, path: func(context.Context, uint32, navigation.Point3D, navigation.Point3D) (*navigation.PathResult, error) {
		return nil, context.DeadlineExceeded
	}}
	r := checkScenario(context.Background(), &config.Scenario{Goal: "quest:62", Start: &config.Place{}, Agents: []config.Agent{{Name: "mage", Race: "human", Class: "mage"}}}, src)
	for _, f := range r.Findings {
		if f.Status == "verified" {
			t.Fatal("missing evidence certified", f)
		}
	}
}
