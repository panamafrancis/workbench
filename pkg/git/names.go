package git

import (
	"fmt"
	"math/rand/v2"
	"regexp"
)

var adjectives = []string{
	"bold", "swift", "calm", "bright", "clear", "sharp", "quiet", "brave",
	"clean", "crisp", "dark", "deep", "dry", "fair", "fast", "firm",
	"flat", "free", "full", "good", "great", "hard", "high", "keen",
	"kind", "late", "lean", "long", "loud", "neat", "nice", "open",
	"pure", "rare", "rich", "safe", "slim", "slow", "smart", "smooth",
	"soft", "still", "strong", "tall", "thin", "true", "warm", "wide",
	"wild", "wise",
}

var cities = []string{
	"atlanta", "oslo", "kyoto", "lisbon", "nairobi", "dubai", "seoul",
	"vienna", "prague", "cairo", "lima", "accra", "bogota", "manila",
	"jakarta", "tehran", "lagos", "dhaka", "karachi", "kolkata",
	"mumbai", "delhi", "taipei", "osaka", "berlin", "madrid", "rome",
	"paris", "sydney", "toronto", "chicago", "denver", "austin",
	"miami", "boston", "seattle", "phoenix", "detroit", "nashville",
	"portland", "dallas", "houston", "montreal", "havana", "santiago",
	"budapest", "warsaw", "athens", "zagreb", "dublin",
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,22}[a-z0-9]$|^[a-z0-9]$`)

func GenerateName(existing []string) (string, error) {
	taken := make(map[string]bool, len(existing))
	for _, n := range existing {
		taken[n] = true
	}
	// Shuffle deterministically based on the number of existing names so the
	// result is stable for the same input but varies as worktrees are added.
	rng := rand.New(rand.NewPCG(uint64(len(existing)), 0))
	adjs := make([]string, len(adjectives))
	copy(adjs, adjectives)
	rng.Shuffle(len(adjs), func(i, j int) { adjs[i], adjs[j] = adjs[j], adjs[i] })
	ctys := make([]string, len(cities))
	copy(ctys, cities)
	rng.Shuffle(len(ctys), func(i, j int) { ctys[i], ctys[j] = ctys[j], ctys[i] })

	for _, adj := range adjs {
		for _, city := range ctys {
			candidate := adj + "-" + city
			if !taken[candidate] {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("all generated names are taken")
}

func ValidateName(name string, existing []string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("name must be lowercase alphanumeric and hyphens, 1-24 chars")
	}
	for _, n := range existing {
		if n == name {
			return fmt.Errorf("name %q already in use", name)
		}
	}
	return nil
}
