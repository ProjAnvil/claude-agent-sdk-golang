package internal

import (
	"strings"
	"testing"
)

// Tests for validateToolArguments, the jsonschema-subset validator behind
// "Input validation error: ..." tool results.

func TestValidateToolArgumentsNoSchema(t *testing.T) {
	if msg := validateToolArguments(nil, map[string]interface{}{"x": 1.0}); msg != "" {
		t.Errorf("Expected no violation without a schema, got %q", msg)
	}
	if msg := validateToolArguments("not a schema", map[string]interface{}{}); msg != "" {
		t.Errorf("Expected no violation for a non-map schema, got %q", msg)
	}
}

func TestValidateToolArgumentsNilArguments(t *testing.T) {
	schema := map[string]interface{}{
		"type":     "object",
		"required": []interface{}{"a"},
	}
	if msg := validateToolArguments(schema, nil); msg != "'a' is a required property" {
		t.Errorf("Expected required violation for nil arguments, got %q", msg)
	}
	// No required properties: nil arguments validate as an empty object.
	if msg := validateToolArguments(map[string]interface{}{"type": "object"}, nil); msg != "" {
		t.Errorf("Expected no violation, got %q", msg)
	}
}

func TestValidateTypes(t *testing.T) {
	cases := []struct {
		typeName string
		value    interface{}
		valid    bool
	}{
		{"object", map[string]interface{}{}, true},
		{"object", "x", false},
		{"array", []interface{}{1.0}, true},
		{"array", "x", false},
		{"string", "x", true},
		{"string", 1.0, false},
		{"boolean", true, true},
		{"boolean", "true", false},
		{"null", nil, true},
		{"null", 0.0, false},
		{"number", 1.5, true},
		{"number", 2, true},
		{"number", "1", false},
		{"integer", 2.0, true},
		{"integer", 2, true},
		{"integer", 2.5, false},
		{"integer", "2", false},
	}
	for _, tc := range cases {
		schema := map[string]interface{}{"type": tc.typeName}
		msg := validateAgainstSchema(schema, tc.value)
		if tc.valid && msg != "" {
			t.Errorf("type %q with %v: expected valid, got %q", tc.typeName, tc.value, msg)
		}
		if !tc.valid && msg == "" {
			t.Errorf("type %q with %v: expected violation", tc.typeName, tc.value)
		}
	}
}

func TestValidateUnknownSchemaTypeIsASchemaError(t *testing.T) {
	msg := validateAgainstSchema(map[string]interface{}{"type": "bogus"}, 1.0)
	if !strings.Contains(msg, "bogus") {
		t.Errorf("Expected the bogus type named in the message, got %q", msg)
	}
}

func TestValidateTypeMismatchMessage(t *testing.T) {
	msg := validateAgainstSchema(map[string]interface{}{"type": "number"}, "two")
	if msg != "'two' is not of type 'number'" {
		t.Errorf("Unexpected message: %q", msg)
	}
}

func TestValidateNestedProperties(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"user": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"age": map[string]interface{}{"type": "integer"},
				},
				"required": []interface{}{"age"},
			},
		},
	}
	if msg := validateAgainstSchema(schema, map[string]interface{}{
		"user": map[string]interface{}{"age": 3.0},
	}); msg != "" {
		t.Errorf("Expected valid, got %q", msg)
	}
	if msg := validateAgainstSchema(schema, map[string]interface{}{
		"user": map[string]interface{}{},
	}); msg != "'age' is a required property" {
		t.Errorf("Expected nested required violation, got %q", msg)
	}
	if msg := validateAgainstSchema(schema, map[string]interface{}{
		"user": map[string]interface{}{"age": "three"},
	}); msg != "'three' is not of type 'integer'" {
		t.Errorf("Expected nested type violation, got %q", msg)
	}
	// Absent optional properties are not checked.
	if msg := validateAgainstSchema(schema, map[string]interface{}{}); msg != "" {
		t.Errorf("Expected valid without optional properties, got %q", msg)
	}
	// A non-map subschema is skipped.
	lax := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"x": "not a schema"},
	}
	if msg := validateAgainstSchema(lax, map[string]interface{}{"x": 1.0}); msg != "" {
		t.Errorf("Expected non-map subschema to be skipped, got %q", msg)
	}
}

func TestValidateRequiredToleratesStringSlice(t *testing.T) {
	schema := map[string]interface{}{
		"type":     "object",
		"required": []string{"a"},
	}
	if msg := validateAgainstSchema(schema, map[string]interface{}{}); msg != "'a' is a required property" {
		t.Errorf("Expected required violation, got %q", msg)
	}
}

func TestValidateStringConstraints(t *testing.T) {
	schema := map[string]interface{}{"type": "string", "minLength": 2.0, "maxLength": 3.0}
	if msg := validateAgainstSchema(schema, "ab"); msg != "" {
		t.Errorf("Expected valid, got %q", msg)
	}
	if msg := validateAgainstSchema(schema, "a"); msg != "'a' is too short" {
		t.Errorf("Expected too short, got %q", msg)
	}
	if msg := validateAgainstSchema(schema, "abcd"); msg != "'abcd' is too long" {
		t.Errorf("Expected too long, got %q", msg)
	}
}

func TestValidateNumberConstraints(t *testing.T) {
	schema := map[string]interface{}{"type": "number", "minimum": 1.0, "maximum": 10.0}
	if msg := validateAgainstSchema(schema, 5.0); msg != "" {
		t.Errorf("Expected valid, got %q", msg)
	}
	if msg := validateAgainstSchema(schema, 0.5); !strings.Contains(msg, "less than the minimum") {
		t.Errorf("Expected minimum violation, got %q", msg)
	}
	if msg := validateAgainstSchema(schema, 11.0); !strings.Contains(msg, "greater than the maximum") {
		t.Errorf("Expected maximum violation, got %q", msg)
	}
}

func TestValidateArrayConstraints(t *testing.T) {
	schema := map[string]interface{}{
		"type":     "array",
		"minItems": 1.0,
		"maxItems": 2.0,
		"items":    map[string]interface{}{"type": "string"},
	}
	if msg := validateAgainstSchema(schema, []interface{}{"a", "b"}); msg != "" {
		t.Errorf("Expected valid, got %q", msg)
	}
	if msg := validateAgainstSchema(schema, []interface{}{}); !strings.Contains(msg, "too few items") {
		t.Errorf("Expected minItems violation, got %q", msg)
	}
	if msg := validateAgainstSchema(schema, []interface{}{"a", "b", "c"}); !strings.Contains(msg, "too many items") {
		t.Errorf("Expected maxItems violation, got %q", msg)
	}
	if msg := validateAgainstSchema(schema, []interface{}{"a", 1.0}); msg != "1 is not of type 'string'" {
		t.Errorf("Expected items violation, got %q", msg)
	}
}

func TestValidateEnum(t *testing.T) {
	schema := map[string]interface{}{"enum": []interface{}{"red", "green", 1.0}}
	if msg := validateAgainstSchema(schema, "red"); msg != "" {
		t.Errorf("Expected valid, got %q", msg)
	}
	// Numeric enum entries match across Go numeric types.
	if msg := validateAgainstSchema(schema, 1); msg != "" {
		t.Errorf("Expected numeric enum match, got %q", msg)
	}
	if msg := validateAgainstSchema(schema, "blue"); !strings.Contains(msg, "is not one of") {
		t.Errorf("Expected enum violation, got %q", msg)
	}
}

func TestJSONDeepEqual(t *testing.T) {
	if !jsonDeepEqual(map[string]interface{}{"a": 1.0}, map[string]interface{}{"a": 1}) {
		t.Error("Expected deep equality for maps with numeric values")
	}
	if jsonDeepEqual(map[string]interface{}{"a": 1.0}, map[string]interface{}{"a": 2.0}) {
		t.Error("Expected inequality for differing map values")
	}
	if jsonDeepEqual(map[string]interface{}{"a": 1.0}, map[string]interface{}{"b": 1.0}) {
		t.Error("Expected inequality for differing keys")
	}
	if !jsonDeepEqual([]interface{}{1.0, "x"}, []interface{}{1, "x"}) {
		t.Error("Expected deep equality for arrays")
	}
	if jsonDeepEqual([]interface{}{1.0}, []interface{}{1.0, 2.0}) {
		t.Error("Expected inequality for differing array lengths")
	}
	if jsonDeepEqual("x", 1.0) {
		t.Error("Expected inequality across kinds")
	}
	if jsonDeepEqual(map[string]interface{}{}, "x") {
		t.Error("Expected inequality across kinds")
	}
	if jsonDeepEqual([]interface{}{}, "x") {
		t.Error("Expected inequality across kinds")
	}
}

func TestJSONSchemaRepr(t *testing.T) {
	if got := jsonschemaRepr("x"); got != "'x'" {
		t.Errorf("Expected 'x', got %q", got)
	}
	if got := jsonschemaRepr(nil); got != "None" {
		t.Errorf("Expected None, got %q", got)
	}
	if got := jsonschemaRepr([]interface{}{"a", 1.0}); got != "['a', 1]" {
		t.Errorf("Expected ['a', 1], got %q", got)
	}
}
