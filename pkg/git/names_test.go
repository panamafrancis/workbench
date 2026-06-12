package git

import (
	"strconv"
	"strings"
	"testing"
)

func TestGenerateNameBasic(t *testing.T) {
	name, err := GenerateName(nil)
	if err != nil {
		t.Fatalf("GenerateName(nil) error = %v", err)
	}
	if !nameRe.MatchString(name) {
		t.Errorf("GenerateName() = %q, does not match nameRe", name)
	}
	if !IsCityName(name) {
		t.Errorf("GenerateName() = %q, not a known city", name)
	}
}

func TestGenerateNameBarePreferred(t *testing.T) {
	name, err := GenerateName(nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(name, "-") {
		t.Errorf("GenerateName(nil) = %q, expected bare city name without suffix", name)
	}
}

func TestGenerateNameSkipsExisting(t *testing.T) {
	first, err := GenerateName(nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateName([]string{first})
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Errorf("GenerateName skipped %q but returned it again", first)
	}
}

func TestGenerateNameCollisionSuffix(t *testing.T) {
	first, err := GenerateName(nil)
	if err != nil {
		t.Fatal(err)
	}
	// Take the bare name — next call with same seed characteristics should
	// eventually produce city-2 when the bare name is taken.
	// We force the issue by taking ALL bare city names except none,
	// so instead just take the first name and check the second is different.
	// More targeted: take just the first city and verify suffix appears.
	allBare := make([]string, len(Cities))
	copy(allBare, Cities)
	suffixed, err := GenerateName(allBare)
	if err != nil {
		t.Fatal(err)
	}
	base := ExtractBaseCity(suffixed)
	if base == suffixed {
		t.Fatalf("expected suffixed name when all bare names taken, got %q", suffixed)
	}
	if !IsCityName(suffixed) {
		t.Errorf("suffixed name %q not recognized as city", suffixed)
	}
	_ = first // used above
}

func TestGenerateNameDeterministic(t *testing.T) {
	a, err := GenerateName(nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateName(nil)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("GenerateName(nil) not deterministic: %q vs %q", a, b)
	}
}

func TestGenerateNameExhausted(t *testing.T) {
	all := make([]string, 0, len(Cities)*(maxSuffix))
	for _, city := range Cities {
		all = append(all, city)
		for n := 2; n <= maxSuffix; n++ {
			all = append(all, city+"-"+strconv.Itoa(n))
		}
	}
	_, err := GenerateName(all)
	if err == nil {
		t.Error("GenerateName with all names taken should return error")
	}
}

func TestExtractBaseCity(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"tokyo", "tokyo"},
		{"tokyo-2", "tokyo"},
		{"tokyo-99", "tokyo"},
		{"bold-atlanta", "bold-atlanta"},
		{"new-york", "new-york"},
		{"new-york-3", "new-york"},
		{"a", "a"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := ExtractBaseCity(tc.input)
			if got != tc.want {
				t.Errorf("ExtractBaseCity(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestIsCityName(t *testing.T) {
	if !IsCityName("tokyo") {
		t.Error("IsCityName(tokyo) = false")
	}
	if !IsCityName("tokyo-2") {
		t.Error("IsCityName(tokyo-2) = false")
	}
	if IsCityName("bold-atlanta") {
		t.Error("IsCityName(bold-atlanta) = true, want false")
	}
	if IsCityName("nonexistent") {
		t.Error("IsCityName(nonexistent) = true, want false")
	}
}

func TestValidateNameValid(t *testing.T) {
	cases := []string{
		"a",
		"abc",
		"tokyo",
		"tokyo-2",
		"my-worktree-1",
		"ab",
		strings.Repeat("a", 24),
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateName(name, nil); err != nil {
				t.Errorf("ValidateName(%q) = %v, want nil", name, err)
			}
		})
	}
}

func TestValidateNameInvalidPattern(t *testing.T) {
	cases := []string{
		"",
		"-leading-hyphen",
		"trailing-hyphen-",
		"UPPER",
		"has space",
		"has.dot",
		strings.Repeat("a", 25),
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateName(name, nil); err == nil {
				t.Errorf("ValidateName(%q) = nil, want error", name)
			}
		})
	}
}

func TestValidateNameDuplicate(t *testing.T) {
	existing := []string{"taken", "other"}
	if err := ValidateName("taken", existing); err == nil {
		t.Error("ValidateName(taken, [taken other]) = nil, want error")
	}
}

func TestValidateNameUniqueAmongExisting(t *testing.T) {
	existing := []string{"taken", "other"}
	if err := ValidateName("fresh", existing); err != nil {
		t.Errorf("ValidateName(fresh, [taken other]) = %v, want nil", err)
	}
}
