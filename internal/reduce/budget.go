package reduce

// Token counts are estimates: one token per 4 bytes of input. Real
// tokenizers split differently, so the budget keeps a safety reserve on top.
const (
	bytesPerToken = 4
	reserveTokens = 4000
)

// EstimateTokens is a rough token estimate: len(s) bytes / 4. It is an
// estimate, not an exact count.
func EstimateTokens(s string) int {
	return len(s) / bytesPerToken
}

// Budget is the transcript token budget for one LLM call: max input tokens
// minus the system prompt, the session facts and the previous handoff (all
// given as byte lengths), minus a 4000-token reserve for the answer.
func Budget(maxInputTokens, systemPromptLen, factsLen, prevLen int) int {
	return maxInputTokens - systemPromptLen/bytesPerToken - factsLen/bytesPerToken -
		prevLen/bytesPerToken - reserveTokens
}

// CapHeadTail caps s at max bytes, keeping its beginning and end (rune-safe,
// with a truncation marker between). Callers apply it to LastUser and
// LastAssistant before rendering the session facts; the plan caps those at
// 4000 chars.
func CapHeadTail(s string, max int) string {
	return capHeadTail(s, max)
}
