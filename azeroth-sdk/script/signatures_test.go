package script

import (
	"strings"
	"testing"
)

func TestSignatureGrammar(t *testing.T) {
	for _, tc := range []struct {
		sig             string
		count, required int
		table, variadic bool
	}{
		{"(npc_guid, quest_id [, reward = 0]) → Request<QuestDone>", 3, 2, false, false},
		{"([id = last [, {map, at}]]) → string", 2, 0, false, false},
		{"{[items = {}, money = 0]} → Request", 2, 1, true, false},
		{"([name,] fn, ...) → id", 2, 1, false, true},
		{"(name [, value = \"a,b\"]) → number, string?", 2, 1, false, false},
	} {
		s, err := ParseSignature(tc.sig)
		if err != nil || len(s.Parameters) != tc.count || s.Required() != tc.required || s.Table != tc.table || s.Variadic != tc.variadic {
			t.Fatalf("%s: %+v %v", tc.sig, s, err)
		}
	}
	for _, sig := range []string{"", "(id)", "(id) →", "(id [,x) → bool", "(id,{x) → bool", "(\"x) → bool", "(..., id) → bool"} {
		if _, err := ParseSignature(sig); err == nil {
			t.Errorf("accepted malformed signature %q", sig)
		}
	}
}

func TestNativeContractRejectsBeforeSideEffects(t *testing.T) {
	e := NewEngine(nolog, nil)
	defer e.Close()
	calls := 0
	e.Func("t.id", "(id [, count = 3]) → number", "test", func(id, count uint32) uint32 { calls++; return id + count })
	e.Func("t.opts", "{required [, flag = false]} → bool", "test", func(m map[string]any) bool { calls++; return true })
	e.Func("t.nested", "([id = 0 [, {[flag = false]}]]) → bool", "test", func(id int, m map[string]any) bool { calls++; return true })
	for _, code := range []string{`t.id()`, `t.id(nil)`, `t.id("1")`, `t.id(-1)`, `t.id(1.5)`, `t.id(4294967296)`, `t.id(0/0)`, `t.id(1,2,3)`, `t.opts{typo=true}`, `t.opts{required=true, typo=true}`, `t.opts(false)`, `t.nested(nil,{typo=true})`, `t.nested(1,{flag=2})`} {
		if _, err := e.Eval(code); err == nil {
			t.Errorf("accepted %s", code)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid arguments reached native: %d", calls)
	}
	if v, err := e.Eval(`return t.id(7), t.id(7,nil)`); err != nil || len(v.([]any)) != 2 || v.([]any)[0] != 10.0 || v.([]any)[1] != 10.0 {
		t.Fatalf("documented defaults: %v %v", v, err)
	}
}

func TestNativeRegistrationRejectsInvalidContracts(t *testing.T) {
	for _, tc := range []struct {
		sig string
		fn  any
	}{{"(x) → bool", func() bool { return false }}, {"()", func() {}}, {"() → bool", 5}} {
		func() {
			e := NewEngine(nolog, nil)
			defer e.Close()
			defer func() {
				if recover() == nil {
					t.Errorf("accepted %s", tc.sig)
				}
			}()
			e.Func("t.bad", tc.sig, "test", tc.fn)
		}()
	}
	e := NewEngine(nolog, nil)
	defer e.Close()
	e.Func("t.one", "() → bool", "test", func() bool { return true })
	defer func() {
		if recover() == nil {
			t.Error("duplicate registration accepted")
		}
	}()
	e.Func("t.one", "() → bool", "test", func() bool { return false })
}

func TestRuntimeDocsValidate(t *testing.T) {
	e := NewEngine(nolog, nil)
	defer e.Close()
	if err := ValidateDocs(e.Docs()); err != nil {
		t.Fatal(err)
	}
	docs := e.Docs()
	docs = append(docs, docs[0])
	if err := ValidateDocs(docs); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatal("duplicate documentation accepted")
	}
}

func TestRuntimeOptionsRejectTypos(t *testing.T) {
	e := NewEngine(nolog, nil)
	defer e.Close()
	for _, code := range []string{`events.query{sincee=1}`, `events.query{last=-1}`, `events.query{since=0/0}`, `events.query(false)`, `ask("q",{}, {timeuot=1})`, `ask("q",{}, {timeout=0})`} {
		if _, err := e.Eval(code); err == nil {
			t.Errorf("accepted %s", code)
		}
	}
	if _, err := e.Eval(`return events.query(), events.query{last=1}`); err != nil {
		t.Fatal(err)
	}
}

func TestRequestReturnAndPostconditionContracts(t *testing.T) {
	e := NewEngine(nolog, nil)
	defer e.Close()
	e.Func("bad.request", "() → Request", "test", func() map[string]any { return map[string]any{"ok": true} })
	if _, err := e.Eval(`return bad.request()`); err == nil {
		t.Fatal("raw action returned completed result")
	}
	if err := e.DocumentActionPostconditions(map[string]string{}); err == nil {
		t.Fatal("missing postcondition accepted")
	}
	if err := e.DocumentActionPostconditions(map[string]string{"bad.request": "test completion"}); err != nil {
		t.Fatal(err)
	}
}
