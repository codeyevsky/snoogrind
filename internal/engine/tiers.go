package engine

import "fmt"

// Tier is one achievement step, as reddit's own badge list defines it.
type Tier struct {
	Name string
	At   int64
}

// ShareTiers are the share → copy link badges. No daily cap on these.
var ShareTiers = []Tier{
	{"New Share", 1},
	{"Sharing Enthusiast", 10},
	{"Sharing Advocate", 50},
	{"Sharing Pro", 100},
	{"Sharing Legend", 1000},
}

// Next returns the tier a count is working towards, and false once every one
// of them is behind you.
func Next(tiers []Tier, n int64) (Tier, bool) {
	for _, t := range tiers {
		if n < t.At {
			return t, true
		}
	}
	return Tier{}, false
}

// Progress describes where a count sits among its tiers, e.g.
// "312 / 1000 towards Sharing Legend".
func Progress(tiers []Tier, n int64) string {
	t, ok := Next(tiers, n)
	if !ok {
		return fmt.Sprintf("%d · every badge unlocked", n)
	}
	return fmt.Sprintf("%d / %d towards %s", n, t.At, t.Name)
}
