package httpapi

// machineError adds shared recovery hints without exposing private causes or
// implying that an uncertain mutation is safe to repeat with a fresh key.
func machineError(status int, data any) any {
	if status < 400 {
		return data
	}
	fields, ok := data.(map[string]string)
	if !ok {
		return data
	}
	out := map[string]any{}
	for k, v := range fields {
		out[k] = v
	}
	code := "request_rejected"
	retryable := false
	actions := []string{"inspect_schema"}
	switch status {
	case 401:
		code = "unauthorized"
		actions = []string{"authenticate"}
	case 403:
		code = "forbidden"
		actions = []string{"inspect_capabilities", "request_owner_grant"}
	case 404:
		code = "not_found"
		actions = []string{"check_context"}
	case 409:
		code = "conflict"
		actions = []string{"refresh_application", "inspect_operation", "create_new_plan"}
	case 429:
		code = "rate_limited"
		retryable = true
		actions = []string{"wait", "retry_same_key"}
	default:
		if status >= 500 {
			code = "server_unavailable"
			retryable = true
			actions = []string{"inspect_operation", "retry_same_key"}
		}
	}
	if fields["code"] == "" {
		out["code"] = code
	}
	out["retryable"] = retryable
	out["nextActions"] = actions
	return out
}
