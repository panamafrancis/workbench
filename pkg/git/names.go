package git

import (
	"fmt"
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
	for _, adj := range adjectives {
		for _, city := range cities {
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
