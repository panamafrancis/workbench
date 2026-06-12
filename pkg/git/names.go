package git

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Cities is the pool of city names used for auto-generated worktree names.
var Cities = []string{
	"abuja", "accra", "adelaide", "algiers", "amman", "amsterdam",
	"anchorage", "ankara", "antwerp", "apia", "ashgabat", "astana",
	"asuncion", "athens", "atlanta", "auckland", "austin",
	"baghdad", "baku", "bamako", "bangkok", "barcelona", "belem",
	"belgrade", "belmopan", "bergen", "berlin", "bern", "bilbao",
	"bogota", "bologna", "boston", "brasilia", "bratislava",
	"brisbane", "brussels", "bucharest", "budapest", "calgary",
	"cairo", "canberra", "caracas", "cardiff", "casablanca",
	"chennai", "chicago", "colombo", "copenhagen", "cork",
	"dakar", "dallas", "damascus", "darwin", "delhi", "denver",
	"detroit", "dhaka", "doha", "dresden", "dubai", "dublin",
	"durban", "edinburgh", "edmonton", "florence", "fortaleza",
	"frankfurt", "freetown", "fukuoka", "gdansk", "geneva",
	"genoa", "glasgow", "gothenburg", "granada", "guadalajara",
	"guatemala", "hamburg", "hanoi", "harare", "havana",
	"helsinki", "hiroshima", "houston", "hyderabad", "istanbul",
	"izmir", "jaipur", "jakarta", "jeddah", "jerusalem",
	"kampala", "karachi", "kathmandu", "kigali", "kingston",
	"kinshasa", "kolkata", "krakow", "kuching", "kyoto",
	"lagos", "lahore", "leipzig", "libreville", "lima", "lisbon",
	"liverpool", "ljubljana", "london", "luanda", "lusaka",
	"luxembourg", "lyon", "madrid", "malaga", "managua", "manama",
	"manila", "maputo", "marseille", "medellin", "melbourne",
	"memphis", "miami", "milan", "minsk", "monaco",
	"monterrey", "montevideo", "montreal", "moscow", "mumbai",
	"munich", "muscat", "nagoya", "nairobi", "nantes", "napoli",
	"nashville", "nassau", "nicosia", "nuremberg", "oakland",
	"odessa", "osaka", "oslo", "ottawa", "oxford",
	"palermo", "panama", "paris", "perth", "phoenix", "portland",
	"porto", "prague", "pretoria", "puebla", "quito",
	"rabat", "raleigh", "reykjavik", "riga", "riyadh", "rome",
	"rotterdam", "sacramento", "salzburg", "santiago", "sapporo",
	"sarajevo", "seattle", "sendai", "seoul", "seville",
	"shanghai", "singapore", "skopje", "sofia", "stockholm",
	"stuttgart", "surabaya", "suva", "sydney", "taipei",
	"tallinn", "tampere", "tbilisi", "tehran", "tijuana",
	"tirana", "tokyo", "toronto", "tripoli", "tunis", "turin",
	"valencia", "valletta", "vancouver", "venice", "vienna",
	"vilnius", "warsaw", "winnipeg", "yokohama", "zagreb",
	"zanzibar", "zurich",
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,22}[a-z0-9]$|^[a-z0-9]$`)

const maxSuffix = 99

func GenerateName(existing []string) (string, error) {
	taken := make(map[string]bool, len(existing))
	for _, n := range existing {
		taken[n] = true
	}

	rng := rand.New(rand.NewPCG(uint64(len(existing)), 0))
	ctys := make([]string, len(Cities))
	copy(ctys, Cities)
	rng.Shuffle(len(ctys), func(i, j int) { ctys[i], ctys[j] = ctys[j], ctys[i] })

	for _, city := range ctys {
		if !taken[city] {
			return city, nil
		}
		for n := 2; n <= maxSuffix; n++ {
			candidate := city + "-" + strconv.Itoa(n)
			if !taken[candidate] {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("all generated names are taken")
}

// ExtractBaseCity strips a trailing -N suffix and returns the base city name.
func ExtractBaseCity(name string) string {
	idx := strings.LastIndex(name, "-")
	if idx < 0 {
		return name
	}
	suffix := name[idx+1:]
	if _, err := strconv.Atoi(suffix); err == nil {
		return name[:idx]
	}
	return name
}

// IsCityName reports whether name (after stripping a -N suffix) is a known city.
func IsCityName(name string) bool {
	return slices.Contains(Cities, ExtractBaseCity(name))
}

func ValidateName(name string, existing []string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("name must be lowercase alphanumeric and hyphens, 1-24 chars")
	}
	if slices.Contains(existing, name) {
		return fmt.Errorf("name %q already in use", name)
	}
	return nil
}
