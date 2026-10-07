package tools

import "encoding/json"

// Usage 是从渲染工具输出流里读到的用量。字段缺失就保持零值或 nil，
// 不做估算：报出一个编出来的数比不报更误导。
type Usage struct {
	InputTokens       int64    `json:"input_tokens,omitempty"`
	OutputTokens      int64    `json:"output_tokens,omitempty"`
	CachedInputTokens int64    `json:"cached_input_tokens,omitempty"`
	CostUSD           *float64 `json:"cost_usd,omitempty"`
	Turns             int      `json:"turns,omitempty"`
	// Source 记下数字来自哪种事件，方便日后核对格式是否变了。
	Source string `json:"source,omitempty"`
}

// Empty 表示一个用量事件都没读到。
func (u Usage) Empty() bool { return u.Source == "" }

// AccumulateUsage 把输出流的一行并进 u。认不出的行原样忽略。
//
// 格式按各工具的流式 JSON 约定读取，没有逐个版本实测，所以这里只是尽力而为：
//   - claude 系（stream-json）：收尾的 {"type":"result"} 事件带整次调用的
//     usage、total_cost_usd 与 num_turns，是累计值，以最后一条为准。
//   - codex（exec --json）：每轮结束的 {"type":"turn.completed"} 带该轮 usage，逐轮相加。
//
// 其他工具读不到就留空，尝试记录里照常写耗时。
func AccumulateUsage(tool, line string, u *Usage) {
	var event struct {
		Type  string   `json:"type"`
		Cost  *float64 `json:"total_cost_usd"`
		Turns int      `json:"num_turns"`
		Usage *struct {
			Input       int64 `json:"input_tokens"`
			Output      int64 `json:"output_tokens"`
			Cached      int64 `json:"cached_input_tokens"`
			CacheRead   int64 `json:"cache_read_input_tokens"`
			CacheCreate int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(line), &event) != nil || event.Usage == nil {
		return
	}
	switch {
	case tool == "codex" && event.Type == "turn.completed":
		u.InputTokens += event.Usage.Input
		u.OutputTokens += event.Usage.Output
		u.CachedInputTokens += event.Usage.Cached
		u.Turns++
		u.Source = "codex turn.completed"
	case (tool == "claude" || tool == "qoder" || tool == "codebuddy") && event.Type == "result":
		u.InputTokens = event.Usage.Input + event.Usage.CacheCreate
		u.OutputTokens = event.Usage.Output
		u.CachedInputTokens = event.Usage.CacheRead
		u.CostUSD = event.Cost
		u.Turns = event.Turns
		u.Source = tool + " result"
	}
}
