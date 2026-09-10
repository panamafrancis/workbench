package mcp

// Builders for a Tool's InputSchema (JSON Schema). Hand-writing the nested
// map[string]any literals repeats "type"/"description"/"properties" once per
// property across every tool in every server, which reads as noise and hides
// the one line that matters — the description the agent actually sees.

// EmptyObject is the schema for a tool that takes no arguments.
func EmptyObject() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

// ObjectSchema builds an object schema from props, with required naming the
// mandatory properties (pass none for all-optional).
func ObjectSchema(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// StringProp is a described string property.
func StringProp(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// BoolProp is a described boolean property.
func BoolProp(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

// EnumProp is a described string property restricted to the given values.
func EnumProp(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": description, "enum": values}
}
