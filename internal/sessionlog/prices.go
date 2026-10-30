package sessionlog

import "strings"

// Prices are list API rates in dollars per million tokens, checked
// 2026-10-30: Claude from Anthropic's model table, GPT from OpenAI's launch
// post and rate card. They estimate what the usage would cost at API rates;
// subscription plans are billed differently.
// shortcut: a hand-kept table; a new model shows tokens without a cost until
// it is added here.
type price struct{ in, out, cacheRead float64 }

// Longest prefix wins, so dated IDs such as claude-haiku-4-5-20251001 match.
var claudePrices = map[string]price{
	"claude-fable-5-1":  {10, 50, 0.25},
	"claude-fable-5":    {10, 50, 1.00},
	"claude-mythos-5-1": {10, 50, 0.25},
	"claude-opus-5-5":   {4, 20, 0.20},
	"claude-opus-5":     {5, 25, 0.50},
	"claude-opus-4-8":   {5, 25, 0.50},
	"claude-opus-4-7":   {5, 25, 0.50},
	"claude-opus-4-6":   {5, 25, 0.50},
	"claude-sonnet-5-5": {2, 10, 0.20},
	"claude-sonnet-5":   {2, 10, 0.20},
	"claude-sonnet-4-6": {3, 15, 0.30},
	"claude-haiku-5-5":  {0.10, 0.50, 0.01},
	"claude-haiku-4-5":  {1, 5, 0.10},
}

var codexPrices = map[string]price{
	"gpt-6.1-sol":   {2, 10, 0.10},
	"gpt-6.1-terra": {1, 5, 0.05},
}

func lookup(table map[string]price, model string) (price, bool) {
	best, found := "", false
	for k := range table {
		if strings.HasPrefix(model, k) && len(k) > len(best) {
			best, found = k, true
		}
	}
	return table[best], found
}

// claudeCost prices one response: cache writes at 1.25x input for the 5
// minute cache and 2x for the hour cache; fast mode at twice the rate.
func claudeCost(model string, in, out, read, write5m, write1h int64, fast bool) (float64, bool) {
	p, ok := lookup(claudePrices, model)
	if !ok {
		return 0, false
	}
	c := (float64(in)*p.in + float64(out)*p.out + float64(read)*p.cacheRead +
		float64(write5m)*p.in*1.25 + float64(write1h)*p.in*2) / 1e6
	if fast {
		c *= 2
	}
	return c, true
}

// codexCost prices one response; cache writes are charged as input.
func codexCost(model string, in, out, read, write int64) (float64, bool) {
	p, ok := lookup(codexPrices, model)
	if !ok {
		return 0, false
	}
	return (float64(in+write)*p.in + float64(out)*p.out + float64(read)*p.cacheRead) / 1e6, true
}
