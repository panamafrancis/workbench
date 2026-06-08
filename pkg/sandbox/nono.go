package sandbox

import (
	"fmt"

	"github.com/panamafrancis/workbench/pkg/config"
)

func BuildNonoArgs(worktreePath, modelKey string, cfg *config.Config) ([]string, error) {
	m, ok := cfg.Models[modelKey]
	if !ok {
		return nil, fmt.Errorf("unknown model %q (add it under 'models:' in config)", modelKey)
	}
	args := []string{"run", "--profile", m.NonoProfile, "--allow", worktreePath, "--"}
	args = append(args, m.Binary)
	args = append(args, m.Args...)
	return args, nil
}
