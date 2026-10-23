// Package pricing computes request cost from token usage.
package pricing

import "github.com/shadatr/switchyard/internal/config"

// Table maps "provider/model" to USD per one million tokens.
type Table map[string]config.Price

// Cost returns the USD cost of a request; unknown targets cost 0.
func (t Table) Cost(target string, promptTokens, completionTokens int) float64 {
	p, ok := t[target]
	if !ok {
		return 0
	}
	return (float64(promptTokens)*p.Input + float64(completionTokens)*p.Output) / 1e6
}
