package internal

import (
	"fmt"
	"math"
	"strings"
)

// This file validates tool call arguments against a tool's inputSchema,
// mirroring the jsonschema validation Python's create_sdk_mcp_server runs
// before the handler (see run_tool in claude_agent_sdk/__init__.py): invalid
// arguments are reported as an isError result reading "Input validation
// error: ..." and the handler never runs. Go has no jsonschema dependency,
// so this is a deliberate subset covering the keywords tool schemas use in
// practice: type, required, properties, items, enum, minLength, maxLength,
// minimum, maximum, minItems and maxItems. Messages follow jsonschema's
// wording ("'b' is a required property", "'two' is not of type 'number'").

// validateToolArguments checks arguments against schema and returns the
// first violation's message, or "" when the arguments are valid (or there
// is no usable schema to check against).
func validateToolArguments(schema interface{}, arguments map[string]interface{}) string {
	schemaMap, ok := schema.(map[string]interface{})
	if !ok || schemaMap == nil {
		return ""
	}
	if arguments == nil {
		arguments = map[string]interface{}{}
	}
	return validateAgainstSchema(schemaMap, arguments)
}

// validateAgainstSchema validates one value against one (sub)schema.
func validateAgainstSchema(schema map[string]interface{}, value interface{}) string {
	if typeName, ok := schema["type"].(string); ok && typeName != "" {
		if message := checkJSONType(typeName, value); message != "" {
			return message
		}
	}
	if enum, ok := schema["enum"].([]interface{}); ok {
		if !matchesAnyEnum(enum, value) {
			return fmt.Sprintf("%s is not one of %s", jsonschemaRepr(value), jsonschemaRepr(enum))
		}
	}
	switch v := value.(type) {
	case map[string]interface{}:
		for _, name := range schemaRequired(schema) {
			if _, present := v[name]; !present {
				return fmt.Sprintf("'%s' is a required property", name)
			}
		}
		if properties, ok := schema["properties"].(map[string]interface{}); ok {
			for key, subRaw := range properties {
				subSchema, ok := subRaw.(map[string]interface{})
				if !ok {
					continue
				}
				subValue, present := v[key]
				if !present {
					continue
				}
				if message := validateAgainstSchema(subSchema, subValue); message != "" {
					return message
				}
			}
		}
	case string:
		if minLength, ok := schemaNumber(schema, "minLength"); ok && float64(len(v)) < minLength {
			return fmt.Sprintf("%s is too short", jsonschemaRepr(v))
		}
		if maxLength, ok := schemaNumber(schema, "maxLength"); ok && float64(len(v)) > maxLength {
			return fmt.Sprintf("%s is too long", jsonschemaRepr(v))
		}
	case []interface{}:
		if minItems, ok := schemaNumber(schema, "minItems"); ok && float64(len(v)) < minItems {
			return fmt.Sprintf("%s has too few items", jsonschemaRepr(v))
		}
		if maxItems, ok := schemaNumber(schema, "maxItems"); ok && float64(len(v)) > maxItems {
			return fmt.Sprintf("%s has too many items", jsonschemaRepr(v))
		}
		if itemSchema, ok := schema["items"].(map[string]interface{}); ok {
			for _, item := range v {
				if message := validateAgainstSchema(itemSchema, item); message != "" {
					return message
				}
			}
		}
	default:
		if number, ok := jsonNumber(value); ok {
			if minimum, ok := schemaNumber(schema, "minimum"); ok && number < minimum {
				return fmt.Sprintf("%s is less than the minimum of %v", jsonschemaRepr(v), minimum)
			}
			if maximum, ok := schemaNumber(schema, "maximum"); ok && number > maximum {
				return fmt.Sprintf("%s is greater than the maximum of %v", jsonschemaRepr(v), maximum)
			}
		}
	}
	return ""
}

// checkJSONType verifies a value against a JSON Schema "type" keyword,
// returning a jsonschema-style message on mismatch. An unknown type name is
// a schema error; its message names the type so the failure is diagnosable
// (jsonschema rejects such schemas the same way).
func checkJSONType(typeName string, value interface{}) string {
	valid := false
	switch typeName {
	case "object":
		_, valid = value.(map[string]interface{})
	case "array":
		_, valid = value.([]interface{})
	case "string":
		_, valid = value.(string)
	case "boolean":
		_, valid = value.(bool)
	case "null":
		valid = value == nil
	case "number":
		_, valid = jsonNumber(value)
	case "integer":
		if number, ok := jsonNumber(value); ok {
			valid = number == math.Trunc(number)
		}
	default:
		return fmt.Sprintf("'%s' is not a valid JSON Schema type", typeName)
	}
	if !valid {
		return fmt.Sprintf("%s is not of type '%s'", jsonschemaRepr(value), typeName)
	}
	return ""
}

// schemaRequired reads the "required" keyword, tolerating []string.
func schemaRequired(schema map[string]interface{}) []string {
	switch required := schema["required"].(type) {
	case []interface{}:
		names := make([]string, 0, len(required))
		for _, item := range required {
			if name, ok := item.(string); ok {
				names = append(names, name)
			}
		}
		return names
	case []string:
		return required
	}
	return nil
}

// schemaNumber reads a numeric schema keyword.
func schemaNumber(schema map[string]interface{}, key string) (float64, bool) {
	return jsonNumber(schema[key])
}

// jsonNumber converts any JSON numeric value to float64.
func jsonNumber(value interface{}) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case int32:
		return float64(number), true
	}
	return 0, false
}

// matchesAnyEnum reports whether value deep-equals one of the enum entries.
func matchesAnyEnum(enum []interface{}, value interface{}) bool {
	for _, entry := range enum {
		if jsonDeepEqual(entry, value) {
			return true
		}
	}
	return false
}

// jsonDeepEqual compares two JSON-decoded values, treating numeric types as
// interchangeable (an int literal equals its float64 decode).
func jsonDeepEqual(a, b interface{}) bool {
	if aNumber, ok := jsonNumber(a); ok {
		bNumber, ok := jsonNumber(b)
		return ok && aNumber == bNumber
	}
	switch av := a.(type) {
	case map[string]interface{}:
		bv, ok := b.(map[string]interface{})
		if !ok || len(av) != len(bv) {
			return false
		}
		for key, aValue := range av {
			bValue, present := bv[key]
			if !present || !jsonDeepEqual(aValue, bValue) {
				return false
			}
		}
		return true
	case []interface{}:
		bv, ok := b.([]interface{})
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !jsonDeepEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}

// jsonschemaRepr renders a value the way jsonschema's error messages do:
// strings single-quoted, everything else in its plain form.
func jsonschemaRepr(value interface{}) string {
	switch v := value.(type) {
	case string:
		return "'" + v + "'"
	case []interface{}:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = jsonschemaRepr(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case nil:
		return "None"
	default:
		return fmt.Sprintf("%v", v)
	}
}
