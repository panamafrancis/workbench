package github

import (
	"strings"
)

// RemoteRef identifies a repository by host, owner and name, for any git host
// — not just GitHub. It is the key of supatree's repo cache: one clone per
// RemoteRef, at <host>/<owner>/<name>.
type RemoteRef struct {
	Host  string
	Owner string
	Name  string
}

// Key is the cache key and on-disk path: host/owner/name.
func (r RemoteRef) Key() string { return r.Host + "/" + r.Owner + "/" + r.Name }

// Repo is the owner/name pair.
func (r RemoteRef) Repo() RepoRef { return RepoRef{Owner: r.Owner, Name: r.Name} }

// IsGitHub reports whether the repo is on github.com.
func (r RemoteRef) IsGitHub() bool { return strings.EqualFold(r.Host, "github.com") }

// SSHURL is the canonical scp-style URL, git@host:owner/name.git.
func (r RemoteRef) SSHURL() string { return "git@" + r.Host + ":" + r.Owner + "/" + r.Name + ".git" }

// ParseRemote reads host, owner and name out of any common git remote form —
// scp-style, ssh:// and http(s):// — taking the host as written.
func ParseRemote(raw string) (RemoteRef, bool) {
	host, ref, ok := splitRemoteURL(raw)
	if !ok {
		return RemoteRef{}, false
	}
	return normalizeRemote(RemoteRef{Host: host, Owner: ref.Owner, Name: ref.Name}), true
}

// ResolveRemote is ParseRemote with an ssh config alias resolved to the host
// it stands for, so `git@github-work:owner/repo.git` and
// `git@github.com:owner/repo.git` are the same repository. Only ssh-form
// remotes are resolved: an https host is what it says.
func ResolveRemote(raw string) (RemoteRef, bool) {
	r, ok := ParseRemote(raw)
	if !ok {
		return RemoteRef{}, false
	}
	if isSSHRemote(raw) {
		if resolved := sshResolve(r.Host); resolved != "" && !strings.EqualFold(resolved, r.Host) {
			r.Host = resolved
			r = normalizeRemote(r)
		}
	}
	return r, true
}

// SSHAlias returns the ssh config alias an ssh-form remote goes through, or ""
// when its host is the real one (or it is not an ssh remote).
func SSHAlias(raw string) string {
	r, ok := ParseRemote(raw)
	if !ok || !isSSHRemote(raw) {
		return ""
	}
	if resolved := sshResolve(r.Host); resolved != "" && !strings.EqualFold(resolved, r.Host) {
		return r.Host
	}
	return ""
}

func isSSHRemote(raw string) bool {
	s := strings.TrimSpace(raw)
	return strings.HasPrefix(s, "ssh://") || (strings.Contains(s, ":") && !strings.Contains(s, "://"))
}

// normalizeRemote lowercases the host, and on github.com the owner and name
// too: GitHub is case-insensitive there, and two spellings of one repo must not
// become two clones.
func normalizeRemote(r RemoteRef) RemoteRef {
	r.Host = strings.ToLower(r.Host)
	// A user part (git@) or a port is not part of the identity.
	if i := strings.LastIndex(r.Host, "@"); i >= 0 {
		r.Host = r.Host[i+1:]
	}
	if r.IsGitHub() {
		r.Owner = strings.ToLower(r.Owner)
		r.Name = strings.ToLower(r.Name)
	}
	return r
}
