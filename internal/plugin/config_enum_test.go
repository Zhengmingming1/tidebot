package plugin

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConfigEnumPreservesJSONTypes(t *testing.T) {
	var sc map[string]any
	if err := yaml.Unmarshal([]byte(`
type: object
properties:
  choices: {type: array, items: {enum: ["1", "true", "[a b]", "<nil>"]}}
  enabled: {type: boolean, enum: [true]}
  count: {type: integer, enum: [1, 2]}
  groups: {type: array, items: {enum: [a, b]}, enum: [[a]]}
`), &sc); err != nil {
		t.Fatal(err)
	}
	fields, err := ParseSchema(sc)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"strings", `{"choices":["1","true","[a b]","<nil>"]}`, true},
		{"number is not string", `{"choices":[1]}`, false},
		{"bool is not string", `{"choices":[true]}`, false},
		{"array is not string", `{"choices":[["a","b"]]}`, false},
		{"null is not string", `{"choices":[null]}`, false},
		{"allowed boolean", `{"enabled":true}`, true},
		{"excluded boolean", `{"enabled":false}`, false},
		{"YAML int matches JSON number", `{"count":1}`, true},
		{"equivalent numeric spelling", `{"count":1.0}`, true},
		{"excluded number", `{"count":3}`, false},
		{"allowed whole array", `{"groups":["a"]}`, true},
		{"excluded whole array", `{"groups":["b"]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var values map[string]any
			if err := json.Unmarshal([]byte(tc.raw), &values); err != nil {
				t.Fatal(err)
			}
			_, _, err := ValidateConfig(fields, values)
			if (err == nil) != tc.valid {
				t.Fatalf("ValidateConfig(%s) error = %v, want valid %v", tc.raw, err, tc.valid)
			}
		})
	}
}

func TestEnumNumericAndStructuralEquality(t *testing.T) {
	for _, tc := range []struct {
		name           string
		value, allowed any
		want           bool
	}{
		{"int", float64(1), int(1), true},
		{"int64", float64(1), int64(1), true},
		{"uint64", float64(1), uint64(1), true},
		{"precision collision", float64(9007199254740992), int64(9007199254740993), false},
		{"exact large integer", int64(9007199254740993), uint64(9007199254740993), true},
		{"nested numbers", map[string]any{"a": []any{float64(1), true}}, map[string]any{"a": []any{int(1), true}}, true},
		{"nested string", []any{"1"}, []any{int(1)}, false},
		{"null", nil, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := inEnum(tc.value, []any{tc.allowed}); got != tc.want {
				t.Fatalf("inEnum = %v, want %v", got, tc.want)
			}
		})
	}
}
