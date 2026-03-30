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
	case "rate_limit_event":
		return parseRateLimitEvent(data)
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

	// Extract optional message-level fields
	if usage, ok := messageData["usage"].(map[string]interface{}); ok {
		msg.Usage = usage
	}
	if messageID, ok := messageData["id"].(string); ok {
		msg.MessageID = messageID
	}
	if stopReason, ok := messageData["stop_reason"].(string); ok {
		msg.StopReason = stopReason
	}

	if errStr, ok := messageData["error"].(string); ok {
		msg.Error = errStr
	}

	if errStr, ok := data["error"].(string); ok {
		msg.Error = errStr
	}

	// Extract optional fields from outer data
	if sessionID, ok := data["session_id"].(string); ok {
		msg.SessionID = sessionID
	}
	if uuid, ok := data["uuid"].(string); ok {
		msg.UUID = uuid
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

func parseSystemMessage(data map[string]interface{}) (Message, error) {
	// Check for specific system message subtypes
	if subtype, ok := data["subtype"].(string); ok {
		switch subtype {
		case "task_started":
			return parseTaskStartedMessage(data)
		case "task_progress":
			return parseTaskProgressMessage(data)
		case "task_notification":
			return parseTaskNotificationMessage(data)
		}
	}

	// Generic system message for other subtypes
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

func parseTaskStartedMessage(data map[string]interface{}) (*TaskStartedMessage, error) {
	msg := &TaskStartedMessage{}

	if taskID, ok := data["task_id"].(string); ok {
		msg.TaskID = taskID
	} else {
		return nil, NewMessageParseError("missing required field in task_started message: task_id", data)
	}

	if description, ok := data["description"].(string); ok {
		msg.Description = description
	} else {
		return nil, NewMessageParseError("missing required field in task_started message: description", data)
	}

	if uuid, ok := data["uuid"].(string); ok {
		msg.UUID = uuid
	} else {
		return nil, NewMessageParseError("missing required field in task_started message: uuid", data)
	}

	if sessionID, ok := data["session_id"].(string); ok {
		msg.SessionID = sessionID
	} else {
		return nil, NewMessageParseError("missing required field in task_started message: session_id", data)
	}

	if toolUseID, ok := data["tool_use_id"].(string); ok {
		msg.ToolUseID = toolUseID
	}

	if taskType, ok := data["task_type"].(string); ok {
		msg.TaskType = taskType
	}

	return msg, nil
}

func parseTaskProgressMessage(data map[string]interface{}) (*TaskProgressMessage, error) {
	msg := &TaskProgressMessage{}

	if taskID, ok := data["task_id"].(string); ok {
		msg.TaskID = taskID
	} else {
		return nil, NewMessageParseError("missing required field in task_progress message: task_id", data)
	}

	if description, ok := data["description"].(string); ok {
		msg.Description = description
	} else {
		return nil, NewMessageParseError("missing required field in task_progress message: description", data)
	}

	if uuid, ok := data["uuid"].(string); ok {
		msg.UUID = uuid
	} else {
		return nil, NewMessageParseError("missing required field in task_progress message: uuid", data)
	}

	if sessionID, ok := data["session_id"].(string); ok {
		msg.SessionID = sessionID
	} else {
		return nil, NewMessageParseError("missing required field in task_progress message: session_id", data)
	}

	// Parse usage
	if usageRaw, ok := data["usage"].(map[string]interface{}); ok {
		msg.Usage.TotalTokens = parseIntField(usageRaw, "total_tokens")
		msg.Usage.ToolUses = parseIntField(usageRaw, "tool_uses")
		msg.Usage.DurationMS = parseIntField(usageRaw, "duration_ms")
	} else {
		return nil, NewMessageParseError("missing required field in task_progress message: usage", data)
	}

	if toolUseID, ok := data["tool_use_id"].(string); ok {
		msg.ToolUseID = toolUseID
	}

	if lastToolName, ok := data["last_tool_name"].(string); ok {
		msg.LastToolName = lastToolName
	}

	return msg, nil
}

func parseTaskNotificationMessage(data map[string]interface{}) (*TaskNotificationMessage, error) {
	msg := &TaskNotificationMessage{}

	if taskID, ok := data["task_id"].(string); ok {
		msg.TaskID = taskID
	} else {
		return nil, NewMessageParseError("missing required field in task_notification message: task_id", data)
	}

	if status, ok := data["status"].(string); ok {
		msg.Status = TaskNotificationStatus(status)
	} else {
		return nil, NewMessageParseError("missing required field in task_notification message: status", data)
	}

	if outputFile, ok := data["output_file"].(string); ok {
		msg.OutputFile = outputFile
	} else {
		return nil, NewMessageParseError("missing required field in task_notification message: output_file", data)
	}

	if summary, ok := data["summary"].(string); ok {
		msg.Summary = summary
	} else {
		return nil, NewMessageParseError("missing required field in task_notification message: summary", data)
	}

	if uuid, ok := data["uuid"].(string); ok {
		msg.UUID = uuid
	} else {
		return nil, NewMessageParseError("missing required field in task_notification message: uuid", data)
	}

	if sessionID, ok := data["session_id"].(string); ok {
		msg.SessionID = sessionID
	} else {
		return nil, NewMessageParseError("missing required field in task_notification message: session_id", data)
	}

	if toolUseID, ok := data["tool_use_id"].(string); ok {
		msg.ToolUseID = toolUseID
	}

	// Parse optional usage
	if usageRaw, ok := data["usage"].(map[string]interface{}); ok {
		msg.Usage = &TaskUsage{
			TotalTokens: parseIntField(usageRaw, "total_tokens"),
			ToolUses:    parseIntField(usageRaw, "tool_uses"),
			DurationMS:  parseIntField(usageRaw, "duration_ms"),
		}
	}

	return msg, nil
}

// parseIntField safely parses an int field from a map, handling both float64 and int types.
func parseIntField(m map[string]interface{}, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
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

	if stopReason, ok := data["stop_reason"].(string); ok {
		msg.StopReason = stopReason
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

	if modelUsage, ok := data["modelUsage"].(map[string]interface{}); ok {
		msg.ModelUsage = modelUsage
	}

	if permDenials, ok := data["permission_denials"].([]interface{}); ok {
		msg.PermissionDenials = permDenials
	}

	if errors, ok := data["errors"].([]interface{}); ok {
		errStrings := make([]string, 0, len(errors))
		for _, e := range errors {
			if s, ok := e.(string); ok {
				errStrings = append(errStrings, s)
			}
		}
		msg.Errors = errStrings
	}

	if uuid, ok := data["uuid"].(string); ok {
		msg.UUID = uuid
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

func parseRateLimitEvent(data map[string]interface{}) (*RateLimitEvent, error) {
	msg := &RateLimitEvent{}

	infoRaw, ok := data["rate_limit_info"].(map[string]interface{})
	if !ok {
		return nil, NewMessageParseError("missing required field in rate_limit_event message: rate_limit_info", data)
	}

	statusStr, ok := infoRaw["status"].(string)
	if !ok {
		return nil, NewMessageParseError("missing required field in rate_limit_event message: rate_limit_info.status", data)
	}

	info := RateLimitInfo{
		Status: RateLimitStatus(statusStr),
		Raw:    infoRaw,
	}

	if resetsAt, ok := infoRaw["resetsAt"].(float64); ok {
		v := int64(resetsAt)
		info.ResetsAt = &v
	}
	if rlType, ok := infoRaw["rateLimitType"].(string); ok {
		info.RateLimitType = RateLimitType(rlType)
	}
	if utilization, ok := infoRaw["utilization"].(float64); ok {
		info.Utilization = &utilization
	}
	if overageStatus, ok := infoRaw["overageStatus"].(string); ok {
		info.OverageStatus = RateLimitStatus(overageStatus)
	}
	if overageResetsAt, ok := infoRaw["overageResetsAt"].(float64); ok {
		v := int64(overageResetsAt)
		info.OverageResetsAt = &v
	}
	if overageDisabledReason, ok := infoRaw["overageDisabledReason"].(string); ok {
		info.OverageDisabledReason = overageDisabledReason
	}

	msg.RateLimitInfo = info

	if uuid, ok := data["uuid"].(string); ok {
		msg.UUID = uuid
	} else {
		return nil, NewMessageParseError("missing required field in rate_limit_event message: uuid", data)
	}

	if sessionID, ok := data["session_id"].(string); ok {
		msg.SessionID = sessionID
	} else {
		return nil, NewMessageParseError("missing required field in rate_limit_event message: session_id", data)
	}

	return msg, nil
}
