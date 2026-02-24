package claude

// ParseMessage parses a raw JSON message into a typed Message.
// Returns nil for unknown message types for forward compatibility.
func ParseMessage(data map[string]interface{}) (Message, error) {
	if data == nil {
		return nil, NewMessageParseError("message data is nil", nil)
	}

	msgType, ok := data["type"].(string)
	if !ok {
		return nil, NewMessageParseError("message missing 'type' field", data)
	}

	switch msgType {
	case "user":
		return parseUserMessage(data)
	case "assistant":
		return parseAssistantMessage(data)
	case "system":
		return parseSystemMessage(data)
	case "result":
		return parseResultMessage(data)
	case "stream_event":
		return parseStreamEvent(data)
	default:
		// Forward-compatible: skip unrecognized message types so newer
		// CLI versions don't crash older SDK versions.
		return nil, nil
	}
}

func parseUserMessage(data map[string]interface{}) (*UserMessage, error) {
	msg := &UserMessage{}

	// Extract UUID
	if uuid, ok := data["uuid"].(string); ok {
		msg.UUID = uuid
	}

	// Extract parent_tool_use_id
	if parentID, ok := data["parent_tool_use_id"].(string); ok {
		msg.ParentToolUseID = parentID
	}

	if tur, ok := data["tool_use_result"].(map[string]interface{}); ok {
		msg.ToolUseResult = tur
	}

	// Extract content from nested message
	messageData, ok := data["message"].(map[string]interface{})
	if !ok {
		return nil, NewMessageParseError("user message missing 'message' field", data)
	}

	content := messageData["content"]
	switch c := content.(type) {
	case string:
		msg.Content = c
	case []interface{}:
		blocks, err := ParseContentBlocks(c)
		if err != nil {
			return nil, err
		}
		msg.Content = blocks
	default:
		msg.Content = content
	}

	return msg, nil
}

func parseAssistantMessage(data map[string]interface{}) (*AssistantMessage, error) {
	msg := &AssistantMessage{}

	// Extract parent_tool_use_id
	if parentID, ok := data["parent_tool_use_id"].(string); ok {
		msg.ParentToolUseID = parentID
	}

	// Extract from nested message
	messageData, ok := data["message"].(map[string]interface{})
	if !ok {
		return nil, NewMessageParseError("assistant message missing 'message' field", data)
	}

	// Extract model
	if model, ok := messageData["model"].(string); ok {
		msg.Model = model
	}

	if errStr, ok := messageData["error"].(string); ok {
		msg.Error = errStr
	}

	if errStr, ok := data["error"].(string); ok {
		msg.Error = errStr
	}

	// Extract content blocks
	contentRaw, ok := messageData["content"].([]interface{})
	if !ok {
		return nil, NewMessageParseError("assistant message missing 'content' field", data)
	}

	blocks, err := ParseContentBlocks(contentRaw)
	if err != nil {
		return nil, err
	}
	msg.Content = blocks

	return msg, nil
}

func parseSystemMessage(data map[string]interface{}) (*SystemMessage, error) {
	msg := &SystemMessage{
		Data: data,
	}

	if subtype, ok := data["subtype"].(string); ok {
		msg.Subtype = subtype
	} else {
		return nil, NewMessageParseError("missing required field in system message: subtype", data)
	}

	return msg, nil
}

func parseResultMessage(data map[string]interface{}) (*ResultMessage, error) {
	msg := &ResultMessage{}

	if subtype, ok := data["subtype"].(string); ok {
		msg.Subtype = subtype
	} else {
		return nil, NewMessageParseError("missing required field in result message: subtype", data)
	}

	if durationMS, ok := data["duration_ms"].(float64); ok {
		msg.DurationMS = int(durationMS)
	} else {
		return nil, NewMessageParseError("missing required field in result message: duration_ms", data)
	}

	if durationAPIMS, ok := data["duration_api_ms"].(float64); ok {
		msg.DurationAPIMS = int(durationAPIMS)
	} else {
		return nil, NewMessageParseError("missing required field in result message: duration_api_ms", data)
	}

	if isError, ok := data["is_error"].(bool); ok {
		msg.IsError = isError
	} else {
		return nil, NewMessageParseError("missing required field in result message: is_error", data)
	}

	if numTurns, ok := data["num_turns"].(float64); ok {
		msg.NumTurns = int(numTurns)
	} else {
		return nil, NewMessageParseError("missing required field in result message: num_turns", data)
	}

	if sessionID, ok := data["session_id"].(string); ok {
		msg.SessionID = sessionID
	} else {
		return nil, NewMessageParseError("missing required field in result message: session_id", data)
	}

	if totalCost, ok := data["total_cost_usd"].(float64); ok {
		msg.TotalCostUSD = totalCost
	}

	if usage, ok := data["usage"].(map[string]interface{}); ok {
		msg.Usage = usage
	}

	if result, ok := data["result"].(string); ok {
		msg.Result = result
	}

	if structuredOutput := data["structured_output"]; structuredOutput != nil {
		msg.StructuredOutput = structuredOutput
	}

	return msg, nil
}

func parseStreamEvent(data map[string]interface{}) (*StreamEvent, error) {
	msg := &StreamEvent{}

	if uuid, ok := data["uuid"].(string); ok {
		msg.UUID = uuid
	} else {
		return nil, NewMessageParseError("missing required field in stream_event message: uuid", data)
	}

	if sessionID, ok := data["session_id"].(string); ok {
		msg.SessionID = sessionID
	} else {
		return nil, NewMessageParseError("missing required field in stream_event message: session_id", data)
	}

	if event, ok := data["event"].(map[string]interface{}); ok {
		msg.Event = event
	} else {
		return nil, NewMessageParseError("missing required field in stream_event message: event", data)
	}

	if parentID, ok := data["parent_tool_use_id"].(string); ok {
		msg.ParentToolUseID = parentID
	}

	return msg, nil
}
