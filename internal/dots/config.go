package dots

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	configEnv  = "DOTS_CONFIG"
	profileEnv = "DOTS_PROFILE"
)

// Config is the on-disk dots configuration.
type Config struct {
	Repo    string `toml:"repo"`
	Profile string `toml:"profile"`
}

// Runtime is the resolved configuration for one command invocation.
type Runtime struct {
	ConfigPath string
	Repo       string
	Profile    string
	Home       string
	StateDir   string
}

func (a *App) resolveRuntime() (*Runtime, error) {
	configPath, err := a.resolveConfigPath()
	if err != nil {
		return nil, err
	}

	var cfg Config
	if _, err := toml.DecodeFile(configPath, &cfg); err != nil {
		return nil, fmt.Errorf("load config %s: %w", configPath, err)
	}

	profile := a.resolveProfileOverride()
	if profile == "" {
		profile = cfg.Profile
	}
	if err := validateProfile(profile); err != nil {
		return nil, err
	}

	if strings.TrimSpace(cfg.Repo) == "" {
		return nil, errors.New("config repo is required")
	}

	repo, err := expandPath(cfg.Repo)
	if err != nil {
		return nil, err
	}
	repo, err = filepath.Abs(repo)
	if err != nil {
		return nil, fmt.Errorf("resolve repo path: %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}

	stateDir, err := defaultStateDir()
	if err != nil {
		return nil, err
	}

	return &Runtime{
		ConfigPath: configPath,
		Repo:       repo,
		Profile:    profile,
		Home:       home,
		StateDir:   stateDir,
	}, nil
}

func (a *App) resolveConfigPath() (string, error) {
	configPath := strings.TrimSpace(a.configPath)
	if configPath == "" {
		configPath = strings.TrimSpace(os.Getenv(configEnv))
	}
	if configPath == "" {
		var err error
		configPath, err = defaultConfigPath()
		if err != nil {
			return "", err
		}
	}

	path, err := expandPath(configPath)
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve config path: %w", err)
	}
	return path, nil
}

func (a *App) resolveProfileOverride() string {
	if strings.TrimSpace(a.profile) != "" {
		return strings.TrimSpace(a.profile)
	}
	return strings.TrimSpace(os.Getenv(profileEnv))
}

func defaultConfigPath() (string, error) {
	if xdgConfig := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdgConfig != "" {
		return filepath.Join(xdgConfig, "dots", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "dots", "config.toml"), nil
}

func defaultStateDir() (string, error) {
	if xdgState := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); xdgState != "" {
		stateDir, err := expandPath(xdgState)
		if err != nil {
			return "", err
		}
		return filepath.Abs(filepath.Join(stateDir, "dots"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "dots"), nil
}

func expandPath(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", errors.New("path is required")
	}
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

func validateProfile(profile string) error {
	if strings.TrimSpace(profile) == "" {
		return errors.New("profile is required; pass --profile or set DOTS_PROFILE")
	}
	if profile == "." || profile == ".." {
		return fmt.Errorf("invalid profile %q", profile)
	}
	if strings.ContainsAny(profile, `/\`) {
		return fmt.Errorf("invalid profile %q: path separators are not allowed", profile)
	}
	return nil
}

func writeConfig(path string, cfg Config) error {
	cleaned := filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(cleaned), 0o750); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	file, err := os.OpenFile(cleaned, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("config already exists: %s", cleaned)
		}
		return fmt.Errorf("create config %s: %w", cleaned, err)
	}

	encoder := toml.NewEncoder(file)
	if err := encoder.Encode(cfg); err != nil {
		return errors.Join(fmt.Errorf("write config %s: %w", cleaned, err), file.Close())
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close config %s: %w", cleaned, err)
	}
	return nil
}
