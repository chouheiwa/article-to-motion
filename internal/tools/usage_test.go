package tools

import "testing"

func TestAccumulateUsageReadsClaudeResultEvent(t *testing.T) {
	var u Usage
	AccumulateUsage("claude", `{"type":"assistant","message":{"content":[]}}`, &u)
	if !u.Empty() {
		t.Fatalf("assistant 事件不该产生用量：%+v", u)
	}
	AccumulateUsage("claude", `{"type":"result","num_turns":7,"total_cost_usd":0.42,"usage":{"input_tokens":100,"cache_creation_input_tokens":20,"cache_read_input_tokens":300,"output_tokens":50}}`, &u)
	if u.InputTokens != 120 || u.OutputTokens != 50 || u.CachedInputTokens != 300 || u.Turns != 7 || u.CostUSD == nil || *u.CostUSD != 0.42 {
		t.Fatalf("claude result 解析不对：%+v", u)
	}
}

func TestAccumulateUsageSumsCodexTurns(t *testing.T) {
	var u Usage
	for range 2 {
		AccumulateUsage("codex", `{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":3}}`, &u)
	}
	AccumulateUsage("codex", `not json`, &u)
	if u.InputTokens != 20 || u.OutputTokens != 6 || u.CachedInputTokens != 8 || u.Turns != 2 || u.CostUSD != nil {
		t.Fatalf("codex 逐轮累加不对：%+v", u)
	}
}
