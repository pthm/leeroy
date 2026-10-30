// apigen writes the generated API references from one source of truth: the function registry of a live session
// (signatures and descriptions, from the RPC method api.docs) and the data model (package types, by reflection
// plus its doc comments): docs/api-reference.md, every function, type and event.
//
// Run it against any session (azbench apidoc starts one): go run ./cmd/apigen -rpc http://127.0.0.1:8112/rpc
package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"log"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/pthm/leeroy/azeroth-sdk/script"
	"github.com/pthm/leeroy/azeroth-sdk/types"
)

type fnDoc struct {
	Name string `json:"name"`
	Sig  string `json:"sig"`
	Desc string `json:"desc"`
}

// the order types appear in the reference (anything else reachable is appended)
var roots = []any{
	types.Position{}, types.Location{}, types.Unit{}, types.Creature{}, types.Player{}, types.UnitRef{}, types.Object{},
	types.DangerCheck{}, types.PathCheck{}, types.RouteCheck{}, types.RouteLeg{}, types.Resource{}, types.XP{}, types.Casting{}, types.Stats{}, types.Corpse{}, types.Achievements{}, types.Skill{},
	types.Item{}, types.Comparison{}, types.VendorItem{}, types.Spell{}, types.Quest{}, types.Objective{}, types.Talents{},
	types.TalentTree{}, types.Talent{}, types.Group{}, types.GroupMember{}, types.GroupReport{}, types.GroupMemberReport{}, types.Trade{}, types.TradeSide{}, types.Inspect{},
	types.CombatEntry{}, types.Meter{}, types.SpellMeter{}, types.Status{}, types.TaskInfo{}, types.MobSummary{}, types.Fight{}, types.FightHeader{},
	types.Threat{}, types.Gossip{}, types.Gear{}, types.Spellbook{}, types.QuestInfo{}, types.Trainer{}, types.Reputation{},
	types.Request{}, types.ActionResult{}, types.ActResult{}, types.Error{}, types.Event{},
	types.VendorErrand{}, types.TrainErrand{},
	types.Consumed{}, types.ObjectUse{}, types.Rested{}, types.Recovery{},
	types.Diagnostic{},
}

func main() {
	rpc := flag.String("rpc", "http://127.0.0.1:8112/rpc", "a session's RPC endpoint (for the function registry)")
	root := flag.String("root", ".", "the azeroth-sdk directory")
	flag.Parse()

	fns := fetchDocs(*rpc)
	docs := make([]script.Doc, len(fns))
	for i, d := range fns {
		docs[i] = script.Doc{Name: d.Name, Sig: d.Sig, Desc: d.Desc}
	}
	if err := script.ValidateDocs(docs); err != nil {
		log.Fatalf("API contract: %v", err)
	}
	typeDocs := parseTypeDocs(*root + "/types")
	ts := collect()
	missing(fns, ts, typeDocs)

	write(*root+"/docs/api-reference.md", markdown(fns, ts, typeDocs))
}

func write(path, s string) {
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s", path)
}

func fetchDocs(url string) []fnDoc {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "api.docs", "params": map[string]any{}})
	res, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatalf("api.docs: %v (start a session first: azbench gm)", err)
	}
	defer res.Body.Close()
	var out struct {
		Result []fnDoc `json:"result"`
		Error  *struct{ Message string }
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil || out.Error != nil {
		log.Fatalf("api.docs: %v %v", err, out.Error)
	}
	return out.Result
}

// parseTypeDocs: type name -> its doc comment, from the types package source
func parseTypeDocs(dir string) map[string]string {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, nil, parser.ParseComments)
	if err != nil {
		log.Fatal(err)
	}
	out := map[string]string{}
	for _, p := range pkgs {
		files := []*ast.File{}
		for _, f := range p.Files {
			files = append(files, f)
		}
		dp, err := doc.NewFromFiles(fset, files, "types")
		if err != nil {
			log.Fatal(err)
		}
		for _, t := range dp.Types {
			out[t.Name] = strings.Join(strings.Fields(t.Doc), " ")
		}
	}
	return out
}

// collect: every struct type in the model, in reference order
func collect() []reflect.Type {
	seen, out := map[reflect.Type]bool{}, []reflect.Type{}
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || seen[t] || t.PkgPath() != reflect.TypeOf(types.Item{}).PkgPath() {
			return
		}
		seen[t] = true
		out = append(out, t)
		for i := 0; i < t.NumField(); i++ {
			walk(t.Field(i).Type)
		}
	}
	for _, r := range roots {
		walk(reflect.TypeOf(r))
	}
	for _, name := range sortedKeys(types.OpResults) {
		walk(reflect.TypeOf(types.OpResults[name]))
	}
	for _, name := range sortedKeys(types.EventTypes) {
		walk(reflect.TypeOf(types.EventTypes[name].Data))
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// ---------- field types ----------

var guidType = reflect.TypeOf(types.GUID(0))

type field struct {
	name, typ, doc string
	optional       bool
	embedded       reflect.Type
}

func fields(t reflect.Type) []field {
	out := []field{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			out = append(out, field{embedded: f.Type})
			continue
		}
		tag := f.Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		if name == "-" || !f.IsExported() {
			continue
		}
		out = append(out, field{name: name, typ: typeName(f.Type), doc: f.Tag.Get("doc"),
			optional: strings.Contains(opts, "omitempty") || f.Type.Kind() == reflect.Pointer})
	}
	return out
}

func typeName(t reflect.Type) string {
	if t == guidType {
		return "GUID"
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8,
		reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Pointer:
		return typeName(t.Elem())
	case reflect.Slice:
		return typeName(t.Elem()) + "[]"
	case reflect.Array:
		parts := make([]string, t.Len())
		for i := range parts {
			parts[i] = typeName(t.Elem())
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case reflect.Map:
		return "{[string]: " + typeName(t.Elem()) + "}"
	case reflect.Interface:
		return "any"
	}
	return t.Name()
}

// ---------- markdown ----------

func markdown(fns []fnDoc, ts []reflect.Type, typeDocs map[string]string) string {
	var b strings.Builder
	b.WriteString("# API reference\n\nGenerated by `cmd/apigen` from the function registry and package `types`: don't edit by hand.\n" +
		"Conventions are in sdk.md. `request<T>`: an action's request, whose result's `data` is a `T` when it's ok. " +
		"`T?`: nil when unknown (out of sight, not loaded yet, none).\n\n")
	b.WriteString("## Functions\n")
	// one section per namespace, the global functions (await, wait, log, ...) first as "runtime": sorted by name
	// alone they'd fall between the namespaces in several pieces
	space := func(name string) string {
		if n, _, ok := strings.Cut(name, "."); ok {
			return n
		}
		return ""
	}
	sort.SliceStable(fns, func(i, j int) bool { return space(fns[i].Name) < space(fns[j].Name) })
	known := map[string]bool{}
	for _, t := range ts {
		known[t.Name()] = true
	}
	ns := "-"
	for _, f := range fns {
		if n := space(f.Name); n != ns {
			ns = n
			fmt.Fprintf(&b, "\n### %s\n\n| Function | | Returns |\n|---|---|---|\n", cmp.Or(ns, "runtime"))
		}
		fmt.Fprintf(&b, "| `%s%s` | %s | %s |\n", f.Name, mdEscape(f.Sig), mdEscape(f.Desc), returnLinks(f.Sig, known))
	}
	b.WriteString("\n## Types\n\nIn Lua these are tables with these fields; optional fields (`?`) are nil when unset. GUIDs are strings.\n")
	for _, t := range ts {
		fmt.Fprintf(&b, "\n### %s\n\n", t.Name())
		if d := typeDocs[t.Name()]; d != "" {
			b.WriteString(d + "\n\n")
		}
		fs := fields(t)
		for _, f := range fs {
			if f.embedded != nil {
				fmt.Fprintf(&b, "Everything in [%s](#%s), plus:\n\n", f.embedded.Name(), strings.ToLower(f.embedded.Name()))
			}
		}
		if len(fs) == 1 && fs[0].embedded == nil || len(fs) > 1 {
			b.WriteString("| Field | Type | |\n|---|---|---|\n")
		}
		for _, f := range fs {
			if f.embedded != nil {
				continue
			}
			opt := ""
			if f.optional {
				opt = "?"
			}
			fmt.Fprintf(&b, "| `%s%s` | %s | %s |\n", f.name, opt, mdEscape(f.typ), mdEscape(f.doc))
		}
	}
	b.WriteString("\n## Events\n\nAn event is an [Event](#event) whose `data` is the type below.\n\n| Type | Data | When |\n|---|---|---|\n")
	for _, name := range sortedKeys(types.EventTypes) {
		et := types.EventTypes[name]
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", name, reflect.TypeOf(et.Data).Name(), mdEscape(et.When))
	}
	b.WriteString("\n## Action results\n\nThe `data` of an ok result, per op.\n\n| Op | Data |\n|---|---|\n")
	for _, op := range sortedKeys(types.OpResults) {
		fmt.Fprintf(&b, "| `%s` | %s |\n", op, reflect.TypeOf(types.OpResults[op]).Name())
	}
	return b.String()
}

// returnLinks links the types a signature returns ("→ Creature[]") to their sections.
func returnLinks(sig string, known map[string]bool) string {
	_, ret, ok := strings.Cut(sig, "→")
	if !ok {
		return ""
	}
	var links []string
	seen := map[string]bool{}
	for _, w := range regexp.MustCompile(`[A-Z][A-Za-z]+`).FindAllString(ret, -1) {
		if known[w] && !seen[w] {
			seen[w] = true
			links = append(links, fmt.Sprintf("[%s](#%s)", w, strings.ToLower(w)))
		}
	}
	return strings.Join(links, ", ")
}

func mdEscape(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// missing stops the build of the reference when a signature names a types type that isn't documented (add it to
// roots): agents otherwise guess a result's field names.
func missing(fns []fnDoc, ts []reflect.Type, typeDocs map[string]string) {
	have := map[string]bool{}
	for _, t := range ts {
		have[t.Name()] = true
	}
	var out []string
	for _, f := range fns {
		for _, n := range regexp.MustCompile(`\b[A-Z][A-Za-z]+\b`).FindAllString(f.Sig, -1) {
			if _, isType := typeDocs[n]; isType && !have[n] && n != "GUID" { // GUID is a string, documented in sdk.md
				out = append(out, n+" ("+f.Name+")")
				have[n] = true
			}
		}
	}
	if len(out) > 0 {
		log.Fatalf("types named in signatures but not in the reference (add them to roots): %s", strings.Join(out, ", "))
	}
}
