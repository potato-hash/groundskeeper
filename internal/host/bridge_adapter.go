package host

// bridge_adapter.go adapts host.Bridge to runtime.HostHandler so the OMP
// adapter can route host_tool_call and host_uri_request frames through the
// bridge and send the result frames back to OMP.

// HandleToolCall implements runtime.HostHandler. It wraps the Bridge's
// tool-call handling and returns the result in the shape OMP expects.
func (b *Bridge) HandleToolCall(id, toolCallID, toolName string, args map[string]any) (map[string]any, bool) {
	call := &ToolCall{ID: id, ToolCallID: toolCallID, ToolName: toolName, Arguments: args}
	res := b.HandleToolCallInternal(call)
	return res.Result.(map[string]any), res.IsError
}

// HandleURIRequest implements runtime.HostHandler.
func (b *Bridge) HandleURIRequest(id, operation, url, content string) (string, string, bool, string) {
	req := &URIRequest{ID: id, Operation: operation, URL: url, Content: content}
	res := b.HandleURIRequestInternal(req)
	return res.Content, res.ContentType, res.IsError, res.Error
}
