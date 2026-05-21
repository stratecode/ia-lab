package cognitivegateway

import "encoding/json"

func (r *ChatCompletionRequest) UnmarshalJSON(data []byte) error {
	type alias ChatCompletionRequest
	var base alias
	if err := json.Unmarshal(data, &base); err != nil {
		return err
	}
	var extra map[string]any
	if err := json.Unmarshal(data, &extra); err != nil {
		return err
	}
	for _, key := range []string{"model", "messages", "stream", "temperature", "max_tokens", "response_format"} {
		delete(extra, key)
	}
	*r = ChatCompletionRequest(base)
	r.Extra = extra
	return nil
}

func (r ChatCompletionRequest) MarshalJSON() ([]byte, error) {
	out := map[string]any{}
	for key, value := range r.Extra {
		out[key] = value
	}
	if r.Model != "" {
		out["model"] = r.Model
	}
	out["messages"] = r.Messages
	if r.Stream {
		out["stream"] = r.Stream
	}
	if r.Temperature != nil {
		out["temperature"] = r.Temperature
	}
	if r.MaxTokens != nil {
		out["max_tokens"] = r.MaxTokens
	}
	if r.ResponseFormat != nil {
		out["response_format"] = r.ResponseFormat
	}
	return json.Marshal(out)
}
