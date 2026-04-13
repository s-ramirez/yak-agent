package types

type Message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ChatRequestTool struct {
	Type     string              `json:"type"`
	Function ChatRequestFunction `json:"function"`
}

type ChatRequestFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ChatRequest struct {
	Model    string            `json:"model"`
	Messages []Message         `json:"messages"`
	Tools    []ChatRequestTool `json:"tools,omitempty"`
}

type ChatResponse struct {
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Choice struct {
	Message ResponseMessage `json:"message"`
}

type ResponseMessage struct {
	Role      string     `json:"role"`
	Content   *string    `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

func GetResponseText(response *ChatResponse) string {
	if response == nil || len(response.Choices) == 0 {
		return ""
	}
	if response.Choices[0].Message.Content == nil {
		return ""
	}
	return *response.Choices[0].Message.Content
}

func GetToolCalls(response *ChatResponse) []ToolCall {
	if response == nil || len(response.Choices) == 0 {
		return nil
	}
	if len(response.Choices[0].Message.ToolCalls) == 0 {
		return nil
	}
	return response.Choices[0].Message.ToolCalls
}
