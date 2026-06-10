package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/panamafrancis/workbench/pkg/config"
)

const (
	updateCheckInterval = 24 * time.Hour
	releaseURL          = "https://api.github.com/repos/panamafrancis/workbench/releases/latest"
)

type ghRelease struct {
	TagName string `json:"tag_name"`
}

func CheckForUpdate(currentVersion string, cfg *config.Config) string {
	if cfg != nil && !cfg.UpdateCheck() {
		return ""
	}

	state, err := config.LoadState()
	if err != nil {
		return ""
	}

	if time.Since(state.LastUpdateCheck) < updateCheckInterval && state.LatestRelease != "" {
		return compareVersions(currentVersion, state.LatestRelease)
	}

	latest := fetchLatestRelease()
	if latest == "" {
		return ""
	}

	state.LatestRelease = latest
	state.LastUpdateCheck = time.Now()
	_ = state.Save()

	return compareVersions(currentVersion, latest)
}

func fetchLatestRelease() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", releaseURL, nil)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return ""
	}

	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return ""
	}
	return release.TagName
}

func compareVersions(current, latest string) string {
	current = strings.TrimPrefix(current, "v")
	latest = strings.TrimPrefix(latest, "v")
	if current == "dev" || current == "" {
		return ""
	}
	if current == latest {
		return ""
	}
	if !isNewer(latest, current) {
		return ""
	}
	return fmt.Sprintf("workbench %s available (you have %s) — go install github.com/panamafrancis/workbench@latest",
		latest, current)
}

func isNewer(a, b string) bool {
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	for i := range max(len(ap), len(bp)) {
		ai, bi := 0, 0
		if i < len(ap) {
			fmt.Sscanf(ap[i], "%d", &ai)
		}
		if i < len(bp) {
			fmt.Sscanf(bp[i], "%d", &bi)
		}
		if ai != bi {
			return ai > bi
		}
	}
	return false
}
