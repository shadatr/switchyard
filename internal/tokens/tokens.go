// Package tokens estimates token counts when an upstream does not report
// usage. The estimate (≈4 bytes per token for English text) is intentionally
// simple; records using it are flagged tokens_estimated.
package tokens

// Estimate returns an approximate token count for text.
func Estimate(text string) int {
	return EstimateBytes(len(text))
}

// EstimateBytes estimates tokens from a byte length.
func EstimateBytes(n int) int {
	if n <= 0 {
		return 0
	}
	return (n + 3) / 4
}
