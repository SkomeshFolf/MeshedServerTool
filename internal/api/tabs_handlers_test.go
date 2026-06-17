package api

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestParseGameplayConfig_EmptyAndBlank(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  map[string]string
	}{
		{"empty_string", "", map[string]string{}},
		{"whitespace_only", "   \t  ", map[string]string{}},
		{"just_parens", "()", map[string]string{}},
		{"empty_inside_parens", "(  )", map[string]string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseGameplayConfig(tc.input)
			if got == nil {
				t.Fatalf("parseGameplayConfig returned nil, want empty map")
			}
			if len(got) != 0 {
				t.Errorf("got %v, want empty map", got)
			}
		})
	}
}

func TestParseGameplayConfig_SingleEntry(t *testing.T) {
	got := parseGameplayConfig("bOverrideDefaults=True")
	want := map[string]string{"bOverrideDefaults": "True"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseGameplayConfig_MultipleEntries(t *testing.T) {
	in := "(bOverrideDefaults=True,RecoilMultiplier=0.5,MaxPlayers=32)"
	got := parseGameplayConfig(in)
	want := map[string]string{
		"bOverrideDefaults":  "True",
		"RecoilMultiplier":   "0.5",
		"MaxPlayers":         "32",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseGameplayConfig_ValueCoercionPreservedAsString(t *testing.T) {
	// The parser is string-only: bool, int, and float values all
	// round-trip as their original literal spelling. This is the
	// documented contract (see serializeGameplayConfig's comment in
	// tabs_handlers.go).
	in := "(Enabled=True,Capacity=42,Rate=1.25,Disabled=False,Empty=)"
	got := parseGameplayConfig(in)
	want := map[string]string{
		"Enabled":  "True",
		"Capacity": "42",
		"Rate":     "1.25",
		"Disabled": "False",
		"Empty":    "",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseGameplayConfig_ToleratesWhitespaceAndEmptyParts(t *testing.T) {
	// Whitespace around keys/values is stripped; consecutive commas
	// and trailing commas are tolerated (no entry produced).
	in := "(  Key1=Value1 ,  Key2 = Value2 ,, Key3=,)"
	got := parseGameplayConfig(in)
	want := map[string]string{
		"Key1": "Value1",
		"Key2": "Value2",
		"Key3": "",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseGameplayConfig_MalformedSkipped(t *testing.T) {
	// An entry with no '=' is silently skipped so a corrupt file
	// doesn't 500 the whole API.
	in := "(Good=1,NoEquals,AlsoGood=2)"
	got := parseGameplayConfig(in)
	want := map[string]string{"Good": "1", "AlsoGood": "2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSerializeGameplayConfig_Empty(t *testing.T) {
	got := serializeGameplayConfig(map[string]string{})
	if got != "" {
		t.Errorf("serializeGameplayConfig({}) = %q, want empty string", got)
	}
}

func TestSerializeGameplayConfig_KeysAreSorted(t *testing.T) {
	in := map[string]string{
		"bOverrideDefaults": "True",
		"MaxPlayers":        "32",
		"RecoilMultiplier":  "0.5",
	}
	got := serializeGameplayConfig(in)
	want := "(MaxPlayers=32,RecoilMultiplier=0.5,bOverrideDefaults=True)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseSerializeGameplayConfig_RoundTrip(t *testing.T) {
	// parse → mutate → serialize → parse again, verify the set of
	// keys/values matches what we put in. The serializer sorts keys
	// alphabetically, so the literal byte form changes — we compare
	// the maps, not the strings.
	original := "(bOverrideDefaults=True,RecoilMultiplier=0.5,MaxPlayers=32)"
	first := parseGameplayConfig(original)

	// Mutate one entry, add a brand new one.
	first["RecoilMultiplier"] = "0.75"
	first["NewFlag"] = "False"

	blob := serializeGameplayConfig(first)
	if !strings.HasPrefix(blob, "(") || !strings.HasSuffix(blob, ")") {
		t.Errorf("blob = %q, must be wrapped in parens", blob)
	}

	second := parseGameplayConfig(blob)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("round-trip mismatch:\n first=%v\n second=%v", first, second)
	}
}

func TestParseSerializeGameplayConfig_OriginalPreservesData(t *testing.T) {
	// The serializer sorts keys, so parse → serialize won't be
	// byte-identical to the input, but the resulting blob must still
	// parse back to the same map.
	original := "(bOverrideDefaults=True,RecoilMultiplier=0.5,MaxPlayers=32)"
	parsed := parseGameplayConfig(original)
	blob := serializeGameplayConfig(parsed)
	// Sort the input's keys the same way and build the expected blob.
	keys := make([]string, 0, len(parsed))
	for k := range parsed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('(')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(parsed[k])
	}
	b.WriteByte(')')
	if blob != b.String() {
		t.Errorf("serialize mismatch:\n got=%q\n want=%q", blob, b.String())
	}
}

func TestParseSerializeGameplayConfig_EmptyStringHandling(t *testing.T) {
	// An entry with an empty value (e.g. "SomeKey=") should
	// round-trip cleanly.
	in := map[string]string{
		"Enabled": "True",
		"Note":    "",
		"Rate":    "1.5",
	}
	blob := serializeGameplayConfig(in)
	parsed := parseGameplayConfig(blob)
	if !reflect.DeepEqual(parsed, in) {
		t.Errorf("empty-value round-trip mismatch:\n in=%v\n parsed=%v", in, parsed)
	}
}

func TestParseGameplayConfig_ValuesContainingCommasAndEquals(t *testing.T) {
	// The v2 blob format has no quoting/escaping: a comma splits
	// entries, '=' splits key/value. We document that behaviour by
	// pinning the exact split here. A value containing a comma
	// would be split across two keys — that's a v2 bug, not ours.
	// "Note=key=val" splits on the first '=', yielding key="Note",
	// value="key=val" — that one is fine.
	in := "(Host=1.2.3.4,Port=7777,Note=key=val)"
	got := parseGameplayConfig(in)
	want := map[string]string{
		"Host": "1.2.3.4",
		"Port": "7777",
		"Note": "key=val",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// Round-trip via the map survives alphabetical sort.
	if got := parseGameplayConfig(serializeGameplayConfig(got)); !reflect.DeepEqual(got, want) {
		t.Errorf("map round-trip: got %v want %v", got, want)
	}
}

func TestSortStrings_SortsAlphabetically(t *testing.T) {
	in := []string{"banana", "apple", "cherry"}
	sortStrings(in)
	want := []string{"apple", "banana", "cherry"}
	if !reflect.DeepEqual(in, want) {
		t.Errorf("got %v, want %v", in, want)
	}
	// Idempotent.
	sortStrings(in)
	if !reflect.DeepEqual(in, want) {
		t.Errorf("not idempotent: got %v", in)
	}
	// Already-sorted no-op check (just make sure we don't crash).
	empty := []string{}
	sortStrings(empty)
	if len(empty) != 0 {
		t.Errorf("empty input got %v, want []", empty)
	}
	single := []string{"only"}
	sortStrings(single)
	if !reflect.DeepEqual(single, []string{"only"}) {
		t.Errorf("single element mutated: %v", single)
	}
}

func TestSerializeGameplayConfig_StableAcrossRuns(t *testing.T) {
	// Build the same map twice via different key insertion orders
	// and assert the serialized form is byte-identical. This
	// guards against accidental map-iteration order regressions.
	a := map[string]string{}
	a["A"] = "1"
	a["B"] = "2"
	a["C"] = "3"

	b := map[string]string{}
	b["C"] = "3"
	b["A"] = "1"
	b["B"] = "2"

	if serializeGameplayConfig(a) != serializeGameplayConfig(b) {
		t.Errorf("serialization is order-dependent: %q vs %q",
			serializeGameplayConfig(a), serializeGameplayConfig(b))
	}

	// And again with sort.Strings as a cross-check.
	keys := []string{"C", "A", "B"}
	sort.Strings(keys)
	var b2 strings.Builder
	b2.WriteByte('(')
	for i, k := range keys {
		if i > 0 {
			b2.WriteByte(',')
		}
		b2.WriteString(k)
		b2.WriteByte('=')
		b2.WriteString(a[k])
	}
	b2.WriteByte(')')
	if serializeGameplayConfig(a) != b2.String() {
		t.Errorf("does not match sort.Strings output: got %q want %q",
			serializeGameplayConfig(a), b2.String())
	}
}
