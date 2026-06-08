package git

import (
	"strings"
	"testing"
)

func TestGenerateNameBasic(t *testing.T) {
	name, err := GenerateName(nil)
	if err != nil {
		t.Fatalf("GenerateName(nil) error = %v", err)
	}
	if !strings.Contains(name, "-") {
		t.Errorf("GenerateName() = %q, expected adjective-city format", name)
	}
	parts := strings.SplitN(name, "-", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		t.Errorf("GenerateName() = %q, not two non-empty parts", name)
	}
}

func TestGenerateNameSkipsExisting(t *testing.T) {
	// Take the first name, then verify generation returns something different.
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

func TestGenerateNameDeterministic(t *testing.T) {
	// Same existing set → same candidate every time (iteration order is fixed).
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
	// Build a set containing every possible generated name.
	all := make([]string, 0, len(adjectives)*len(cities))
	for _, adj := range adjectives {
		for _, city := range cities {
			all = append(all, adj+"-"+city)
		}
	}
	_, err := GenerateName(all)
	if err == nil {
		t.Error("GenerateName with all names taken should return error")
	}
}

func TestValidateNameValid(t *testing.T) {
	cases := []string{
		"a",
		"abc",
		"bold-atlanta",
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
