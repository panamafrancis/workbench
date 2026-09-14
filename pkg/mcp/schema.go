package mcp

// JSON-schema builders for tool input schemas. Tools describe their arguments
// with the same handful of shapes, so the schema keys are written once here
// rather than spelled out in every literal.

// EmptyObject is the schema for a tool that takes no arguments.
func EmptyObject() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

// ObjectSchema builds an object schema from its properties, marking required
// the names in required (pass nil for none).
func ObjectSchema(props map[string]any, required []string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// StringProp is a string property described by desc.
func StringProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// EnumProp is a string property restricted to values.
func EnumProp(desc string, values ...string) map[string]any {
	p := StringProp(desc)
	p["enum"] = values
	return p
}

// BoolProp is a boolean property described by desc.
func BoolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
