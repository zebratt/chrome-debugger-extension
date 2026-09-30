package automation

func InputSchema() map[string]any {
	text := func(limit int) map[string]any { return map[string]any{"type": "string", "maxLength": limit} }
	field := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"target", "text"}, "properties": map[string]any{"target": text(512), "text": text(4096)}}
	checks := func() map[string]any {
		return map[string]any{
			"expectText": text(1024), "expectURL": text(4096),
			"verifyFields": map[string]any{"type": "array", "maxItems": 10, "items": field},
		}
	}
	action := checks()
	for key, value := range map[string]any{
		"type":   map[string]any{"type": "string", "enum": []string{"click", "type", "select", "key", "scroll", "wait", "assert"}},
		"target": text(512), "role": text(64), "href": text(4096), "text": text(4096), "option": text(4096),
		"key":       map[string]any{"type": "string", "enum": []string{"Enter", "Tab", "Escape", "Backspace", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight"}},
		"deltaY":    map[string]any{"type": "number", "minimum": -100000, "maximum": 100000},
		"timeoutMs": map[string]any{"type": "integer", "minimum": 0, "maximum": 10000},
	} {
		action[key] = value
	}
	props := checks()
	for key, value := range map[string]any{
		"profileId": map[string]any{"type": "string", "minLength": 1}, "tabId": map[string]any{"type": "integer", "minimum": 1},
		"workflow": map[string]any{"type": "string", "enum": []string{"search", "navigate", "fill", "sequence"}},
		"target":   text(512), "text": text(4096), "emptyText": text(1024), "pressEnter": map[string]any{"type": "boolean", "default": false},
		"leaseToken":     map[string]any{"type": "string", "description": "Optional caller-owned claim; renewed but not released by the workflow."},
		"timeoutMs":      map[string]any{"type": "integer", "minimum": 100, "maximum": 60000, "description": "Defaults to 60000 for sequence, 15000 otherwise."},
		"fields":         map[string]any{"type": "array", "minItems": 1, "maxItems": 10, "items": field},
		"actions":        map[string]any{"type": "array", "minItems": 1, "maxItems": 60, "description": "Explicit ordered actions planned and authorized by the caller. Each target must uniquely match the current observation.", "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"type"}, "properties": action}},
		"allowedOrigins": map[string]any{"type": "array", "maxItems": 8, "items": text(4096), "description": "Sequence only: additional exact origins authorized by the caller."},
		"followNewTabs":  map[string]any{"type": "boolean", "default": false},
	} {
		props[key] = value
	}
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"profileId", "tabId", "workflow"}, "properties": props}
}
