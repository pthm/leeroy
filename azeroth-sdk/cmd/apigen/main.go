// apigen writes the generated API references from one source of truth: the function registry of a live session
// (signatures and descriptions, from the RPC method api.docs) and the data model (package types, by reflection
// plus its doc comments): docs/api-reference.md, every function, type and event.
//
// Run it against any session (azbench apidoc starts one): go run ./cmd/apigen -rpc http://127.0.0.1:8112/rpc
package main

import (
	"bytes"
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
	"sort"
	"strings"

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
	types.Path{}, types.Resource{}, types.XP{}, types.Casting{}, types.Stats{}, types.Corpse{}, types.Achievements{},
	types.Item{}, types.Comparison{}, types.VendorItem{}, types.Spell{}, types.Quest{}, types.Objective{}, types.Talents{},
	types.TalentTree{}, types.Talent{}, types.Group{}, types.GroupMember{}, types.GroupReport{}, types.GroupMemberReport{}, types.Trade{}, types.TradeSide{}, types.Inspect{},
	types.CombatEntry{}, types.Meter{}, types.SpellMeter{}, types.Status{}, types.TaskInfo{}, types.MobSummary{},
	types.Result{}, types.Error{}, types.Event{},
}

func main() {
	rpc := flag.String("rpc", "http://127.0.0.1:8112/rpc", "a session's RPC endpoint (for the function registry)")
	root := flag.String("root", ".", "the azeroth-sdk directory")
	flag.Parse()

	fns := fetchDocs(*rpc)
	typeDocs := parseTypeDocs(*root + "/types")
	ts := collect()

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
		"Conventions are in lua-api.md. `request<T>`: an action's request, whose result's `data` is a `T` when it's ok. " +
		"`T?`: nil when unknown (out of sight, not loaded yet, none).\n\n")
	b.WriteString("## Functions\n")
	ns := ""
	for _, f := range fns {
		n, _, ok := strings.Cut(f.Name, ".")
		if !ok {
			n = "runtime"
		}
		if n != ns {
			ns = n
			fmt.Fprintf(&b, "\n### %s\n\n| Function | |\n|---|---|\n", ns)
		}
		fmt.Fprintf(&b, "| `%s%s` | %s |\n", f.Name, mdEscape(f.Sig), mdEscape(f.Desc))
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

func mdEscape(s string) string { return strings.ReplaceAll(s, "|", "\\|") }
