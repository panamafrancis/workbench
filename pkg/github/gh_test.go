package github

import "testing"

func TestMapStatus(t *testing.T) {
	tests := []struct {
		state   string
		isDraft bool
		want    PRStatus
	}{
		{"OPEN", false, PROpen},
		{"OPEN", true, PRDraft},
		{"MERGED", false, PRMerged},
		{"MERGED", true, PRMerged},
		{"CLOSED", false, PRClosed},
		{"CLOSED", true, PRClosed},
	}
	for _, tt := range tests {
		got := mapStatus(tt.state, tt.isDraft)
		if got != tt.want {
			t.Errorf("mapStatus(%q, %v) = %q, want %q", tt.state, tt.isDraft, got, tt.want)
		}
	}
}

func TestIsPermanentError(t *testing.T) {
	if !IsPermanentError(ErrGHNotFound) {
		t.Error("ErrGHNotFound should be permanent")
	}
	if !IsPermanentError(ErrGHAuth) {
		t.Error("ErrGHAuth should be permanent")
	}
	if IsPermanentError(nil) {
		t.Error("nil should not be permanent")
	}
}
