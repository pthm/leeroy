// apigen writes the generated API references from one source of truth: the function registry of a live session
// (signatures and descriptions, from the RPC method api.docs) and the data model (package types, by reflection
// plus its doc comments). Outputs:
//
//	docs/api-reference.md   every function, type and event
//	sdk/ts/src/types.ts     TypeScript interfaces for every type and event
//	sdk/ts/src/api.ts       a typed TypeScript client for every function
//
// Run it against any session (bench/dev/apidoc.sh starts one): go run ./cmd/apigen -rpc http://127.0.0.1:8112/rpc
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
	"regexp"
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
	types.TalentTree{}, types.Talent{}, types.Group{}, types.GroupMember{}, types.Trade{}, types.TradeSide{}, types.Inspect{},
	types.CombatEntry{}, types.Meter{}, types.SpellMeter{}, types.Status{}, types.TaskInfo{}, types.MobSummary{},
	types.Result{}, types.Error{}, types.Event{},
}

// runtime functions are Lua-only (tasks, waiting, events in scripts): not in the TypeScript client
var luaOnly = regexp.MustCompile(`^(wait|await|result|log|now|help|doc|ask|answer|task\..*|events\..*|client\..*|act\..*)$`)

func main() {
	rpc := flag.String("rpc", "http://127.0.0.1:8112/rpc", "a session's RPC endpoint (for the function registry)")
	root := flag.String("root", ".", "the azeroth-sdk directory")
	flag.Parse()

	fns := fetchDocs(*rpc)
	typeDocs := parseTypeDocs(*root + "/types")
	ts := collect()

	write(*root+"/docs/api-reference.md", markdown(fns, ts, typeDocs))
	write(*root+"/sdk/ts/src/types.ts", tsTypes(ts, typeDocs))
	write(*root+"/sdk/ts/src/api.ts", tsAPI(fns))
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
		log.Fatalf("api.docs: %v (start a session first: bench/dev/gm.sh)", err)
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

func fields(t reflect.Type, lang string) []field {
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
		out = append(out, field{name: name, typ: typeName(f.Type, lang), doc: f.Tag.Get("doc"),
			optional: strings.Contains(opts, "omitempty") || f.Type.Kind() == reflect.Pointer})
	}
	return out
}

func typeName(t reflect.Type, lang string) string {
	if t == guidType {
		if lang == "ts" {
			return "Guid | null"
		}
		return "GUID"
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		if lang == "ts" {
			return "boolean"
		}
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8,
		reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Pointer:
		return typeName(t.Elem(), lang)
	case reflect.Slice:
		return typeName(t.Elem(), lang) + "[]"
	case reflect.Array:
		parts := make([]string, t.Len())
		for i := range parts {
			parts[i] = typeName(t.Elem(), lang)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case reflect.Map:
		if lang == "ts" {
			return "Record<string, " + typeName(t.Elem(), lang) + ">"
		}
		return "{[string]: " + typeName(t.Elem(), lang) + "}"
	case reflect.Interface:
		if lang == "ts" {
			return "unknown"
		}
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
		fs := fields(t, "md")
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

// ---------- typescript ----------

func tsTypes(ts []reflect.Type, typeDocs map[string]string) string {
	var b strings.Builder
	b.WriteString("// Generated by azeroth-sdk/cmd/apigen from package types: don't edit by hand.\n\n")
	b.WriteString("/** A 64-bit GUID as a decimal string; null for \"none\". */\nexport type Guid = string;\n")
	for _, t := range ts {
		b.WriteString("\n")
		if d := typeDocs[t.Name()]; d != "" {
			fmt.Fprintf(&b, "/** %s */\n", d)
		}
		ext, generic := []string{}, ""
		switch t.Name() {
		case "Result":
			generic = "<T = unknown>"
		case "Event":
			generic = "<T = unknown>"
		}
		for _, f := range fields(t, "ts") {
			if f.embedded != nil {
				ext = append(ext, f.embedded.Name())
			}
		}
		fmt.Fprintf(&b, "export interface %s%s", t.Name(), generic)
		if len(ext) > 0 {
			fmt.Fprintf(&b, " extends %s", strings.Join(ext, ", "))
		}
		b.WriteString(" {\n")
		for _, f := range fields(t, "ts") {
			if f.embedded != nil {
				continue
			}
			typ := f.typ
			if generic != "" && f.name == "data" {
				typ = "T"
			}
			if f.doc != "" {
				fmt.Fprintf(&b, "  /** %s */\n", f.doc)
			}
			opt := ""
			if f.optional {
				opt = "?"
			}
			fmt.Fprintf(&b, "  %s%s: %s;\n", f.name, opt, typ)
		}
		b.WriteString("}\n")
	}
	b.WriteString("\n/** Event type -> its data. */\nexport interface EventMap {\n")
	for _, name := range sortedKeys(types.EventTypes) {
		et := types.EventTypes[name]
		fmt.Fprintf(&b, "  /** %s */\n  %s: %s;\n", et.When, name, reflect.TypeOf(et.Data).Name())
	}
	b.WriteString("}\n\n/** Any event, discriminated by type. */\nexport type AnyEvent = { [K in keyof EventMap]: Event<EventMap[K]> & { type: K } }[keyof EventMap];\n")
	return b.String()
}

var sigRe = regexp.MustCompile(`^\(([^)]*)\)\s*(?:→\s*(.*))?$`)

// param: a name from a signature like "(npc, entry [, count = 1])"
type param struct {
	name     string
	optional bool
}

func parseSig(sig string) (params []param, ret string, table bool) {
	if strings.HasPrefix(sig, "{") { // takes one table, e.g. trade.offer{items, money}
		_, r, _ := strings.Cut(sig, "→")
		return []param{{name: "opts"}}, strings.TrimSpace(r), true
	}
	m := sigRe.FindStringSubmatch(sig)
	if m == nil {
		return nil, "", false
	}
	ret = strings.TrimSpace(m[2])
	depth := 0
	for _, raw := range strings.Split(m[1], ",") {
		opt := depth > 0 || strings.HasPrefix(strings.TrimSpace(raw), "[")
		depth += strings.Count(raw, "[") - strings.Count(raw, "]")
		n := strings.Trim(raw, " []")
		n, _, _ = strings.Cut(n, "=")
		n = strings.TrimSpace(n)
		if n == "" || n == "..." {
			continue
		}
		params = append(params, param{name: strings.ReplaceAll(n, " ", "_"), optional: opt})
	}
	return params, ret, false
}

func tsParamType(name string) string {
	switch name {
	case "name", "msg", "text", "player", "method", "prefix", "question", "channel":
		return "string"
	case "guid", "npc", "unit":
		return "Guid"
	case "item":
		return "number | Guid"
	case "opts":
		return "Record<string, unknown>"
	}
	return "number"
}

var knownTS = map[string]string{"bool": "boolean", "number": "number", "ms": "number", "copper": "number", "string": "string",
	"GUID": "Guid | null", "": "void"}

func tsReturn(ret string) string {
	if strings.HasSuffix(ret, "?") { // nil when unknown
		inner := strings.Trim(strings.TrimSuffix(ret, "?"), "()")
		if t, ok := knownTS[inner]; ok {
			inner = strings.TrimSuffix(t, " | null")
		}
		return inner + " | null"
	}
	if strings.HasPrefix(ret, "request") {
		if i := strings.Index(ret, "<"); i >= 0 {
			return "Result<" + strings.TrimSuffix(ret[i+1:], ">") + ">"
		}
		return "Result<null>"
	}
	if t, ok := knownTS[ret]; ok {
		return t
	}
	if regexp.MustCompile(`^[A-Z][A-Za-z]*(\[\])?( \| [A-Z][A-Za-z]*)*$`).MatchString(ret) {
		return ret
	}
	return "unknown"
}

func tsAPI(fns []fnDoc) string {
	var b strings.Builder
	b.WriteString("// Generated by azeroth-sdk/cmd/apigen from the session's function registry: don't edit by hand.\n")
	b.WriteString("// Each function calls the Lua API function of the same name (RPC api.call); see docs/api-reference.md.\n\n")
	used := map[string]bool{}
	byNS := map[string][]fnDoc{}
	nss := []string{}
	for _, f := range fns {
		if luaOnly.MatchString(f.Name) {
			continue
		}
		ns, _, _ := strings.Cut(f.Name, ".")
		if byNS[ns] == nil {
			nss = append(nss, ns)
		}
		byNS[ns] = append(byNS[ns], f)
	}
	var body strings.Builder
	body.WriteString("export type Call = (fn: string, args: unknown[]) => Promise<unknown>;\n\n")
	body.WriteString("/** The game API, one property per namespace. */\nexport function gameApi(call: Call) {\n  return {\n")
	for _, ns := range nss {
		fmt.Fprintf(&body, "    %s: {\n", ns)
		for _, f := range byNS[ns] {
			params, ret, _ := parseSig(f.Sig)
			_, fn, _ := strings.Cut(f.Name, ".")
			args, names := []string{}, []string{}
			for _, p := range params {
				opt := ""
				if p.optional {
					opt = "?"
				}
				args = append(args, fmt.Sprintf("%s%s: %s", p.name, opt, tsParamType(p.name)))
				names = append(names, p.name)
			}
			rt := tsReturn(ret)
			for _, w := range regexp.MustCompile(`[A-Z][A-Za-z]*`).FindAllString(rt, -1) {
				if w != "Record" && w != "Guid" {
					used[w] = true
				}
			}
			if strings.Contains(rt, "Guid") || strings.Contains(strings.Join(args, ""), "Guid") {
				used["Guid"] = true
			}
			fmt.Fprintf(&body, "      /** %s%s — %s */\n", f.Name, f.Sig, strings.ReplaceAll(f.Desc, "*/", "* /"))
			fmt.Fprintf(&body, "      %s: (%s) => call(%q, [%s]) as Promise<%s>,\n", fn, strings.Join(args, ", "), f.Name, strings.Join(names, ", "), rt)
		}
		body.WriteString("    },\n")
	}
	body.WriteString("  };\n}\n\nexport type GameApi = ReturnType<typeof gameApi>;\n")
	imports := []string{}
	for k := range used {
		imports = append(imports, k)
	}
	sort.Strings(imports)
	fmt.Fprintf(&b, "import type { %s } from \"./types.ts\";\n\n", strings.Join(imports, ", "))
	b.WriteString(body.String())
	return b.String()
}
