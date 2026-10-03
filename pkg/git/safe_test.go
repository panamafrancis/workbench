package git

import (
	"reflect"
	"testing"
)

func TestRiskyConfig(t *testing.T) {
	list := "global\tfilter.lfs.smudge=git-lfs smudge\n" +
		"local\tcore.repositoryformatversion=0\n" +
		"local\tremote.origin.url=git@github.com:o/r.git\n" +
		"local\tbranch.main.remote=origin\n" +
		"local\tfilter.x.smudge=sh -c evil\n" +
		"local\tremote.origin.uploadpack=evil\n" +
		"worktree\tinclude.path=/tmp/x\n" +
		"local\tcore.hookspath=.husky\n"
	got := riskyConfig(list)
	want := []string{"filter.x.smudge", "remote.origin.uploadpack", "include.path"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("riskyConfig = %v, want %v (global scope and pinned keys are not risks)", got, want)
	}
}
