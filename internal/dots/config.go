package dots

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	configEnv  = "DOTS_CONFIG"
	profileEnv = "DOTS_PROFILE"
)

// Config is the on-disk dots configuration.
type Config struct {
	DefaultProfile string            `toml:"default_profile"`
	Profiles       map[string]string `toml:"profiles"`
}

// RuntimeProfile is a resolved configured profile.
type RuntimeProfile struct {
	Repo string
}

// Runtime is the resolved configuration for one command invocation.
type Runtime struct {
	ConfigPath         string
	Repo               string
	Profile            string
	Home               string
	StateDir           string
	ConfiguredProfiles map[string]RuntimeProfile
	ConfiguredRepos    []string
}

func (a *App) resolveRuntime() (*Runtime, error) {
	configPath, err := a.resolveConfigPath()
	if err != nil {
		return nil, err
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, err
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	profiles, repos, err := resolveConfiguredProfiles(cfg)
	if err != nil {
		return nil, err
	}

	profile := a.resolveProfileOverride()
	if profile == "" {
		profile = cfg.DefaultProfile
	}
	if err := validateProfile(profile); err != nil {
		return nil, err
	}

	profileConfig, ok := profiles[profile]
	if !ok {
		return nil, fmt.Errorf("profile %q is not configured", profile)
	}

	home, err := resolveHomeDir()
	if err != nil {
		return nil, err
	}

	stateDir, err := defaultStateDir()
	if err != nil {
		return nil, err
	}

	return &Runtime{
		ConfigPath:         configPath,
		Repo:               profileConfig.Repo,
		Profile:            profile,
		Home:               home,
		StateDir:           stateDir,
		ConfiguredProfiles: profiles,
		ConfiguredRepos:    repos,
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
	home, err := resolveHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "dots"), nil
}

func resolveHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return home, nil
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

func loadConfig(path string) (Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return Config{}, fmt.Errorf("load config %s: %w", path, err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]string{}
	}
	return cfg, nil
}

func validateConfig(cfg Config) error {
	if len(cfg.Profiles) == 0 {
		return errors.New("config profiles are required")
	}
	if err := validateProfile(cfg.DefaultProfile); err != nil {
		return fmt.Errorf("default_profile: %w", err)
	}
	if _, ok := cfg.Profiles[cfg.DefaultProfile]; !ok {
		return fmt.Errorf("default_profile %q is not configured", cfg.DefaultProfile)
	}

	for _, profile := range profileConfigNames(cfg.Profiles) {
		if err := validateProfile(profile); err != nil {
			return err
		}
		if strings.TrimSpace(cfg.Profiles[profile]) == "" {
			return fmt.Errorf("config repo is required for profile %q", profile)
		}
	}
	return nil
}

func resolveConfiguredProfiles(cfg Config) (map[string]RuntimeProfile, []string, error) {
	profiles := make(map[string]RuntimeProfile, len(cfg.Profiles))
	repos := make([]string, 0, len(cfg.Profiles))
	for _, profile := range profileConfigNames(cfg.Profiles) {
		repo, err := resolveRepoPath(cfg.Profiles[profile])
		if err != nil {
			return nil, nil, fmt.Errorf("resolve repo for profile %q: %w", profile, err)
		}
		profiles[profile] = RuntimeProfile{Repo: repo}
		repos = append(repos, repo)
	}
	return profiles, repos, nil
}

func resolveRepoPath(raw string) (string, error) {
	repo, err := expandPath(raw)
	if err != nil {
		return "", err
	}
	repo, err = filepath.Abs(repo)
	if err != nil {
		return "", fmt.Errorf("resolve repo path: %w", err)
	}
	return repo, nil
}

func profileConfigNames(profiles map[string]string) []string {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func runtimeProfileNames(profiles map[string]RuntimeProfile) []string {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func createConfig(path string, cfg Config) error {
	return writeConfig(path, cfg, true)
}

func replaceConfig(path string, cfg Config) error {
	return writeConfig(path, cfg, false)
}

func writeConfig(path string, cfg Config, exclusive bool) error {
	cleaned := filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(cleaned), 0o750); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	flags := os.O_WRONLY | os.O_CREATE
	if exclusive {
		flags |= os.O_EXCL
	} else {
		flags |= os.O_TRUNC
	}
	file, err := os.OpenFile(cleaned, flags, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("config already exists: %s", cleaned)
		}
		return fmt.Errorf("open config %s: %w", cleaned, err)
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
