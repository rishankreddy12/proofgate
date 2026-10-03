// Package main provides enterprise-grade capabilities, configuration, and structural components for the main subsystem.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Profile defines the core enterprise configuration and state for Profile.
// It is responsible for managing the lifecycle, validation, and schema of the Profile entity.
type Profile struct {
	Server   string `yaml:"server" json:"server"`
	Insecure bool   `yaml:"insecure" json:"insecure"`
}

// ProfileConfig defines the core enterprise configuration and state for ProfileConfig.
// It is responsible for managing the lifecycle, validation, and schema of the ProfileConfig entity.
type ProfileConfig struct {
	ActiveProfile string             `yaml:"active_profile" json:"active_profile"`
	Profiles      map[string]Profile `yaml:"profiles" json:"profiles"`
}

// SessionFile defines the core enterprise configuration and state for SessionFile.
// It is responsible for managing the lifecycle, validation, and schema of the SessionFile entity.
type SessionFile struct {
	Token     string    `json:"token"`
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expires_at"`
}

func configDirPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "proofgatectl")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	sessionsDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func configFilePath() (string, error) {
	dir, err := configDirPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

func sessionFilePath(profileName string) (string, error) {
	dir, err := configDirPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions", profileName+".json"), nil
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "proofgatectl-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func loadProfileConfig() (*ProfileConfig, error) {
	path, err := configFilePath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &ProfileConfig{
			Profiles: make(map[string]Profile),
		}, nil
	}
	if err != nil {
		return nil, err
	}

	var cfg ProfileConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	if cfg.Profiles == nil {
		cfg.Profiles = make(map[string]Profile)
	}
	return &cfg, nil
}

func saveProfileConfig(cfg *ProfileConfig) error {
	path, err := configFilePath()
	if err != nil {
		return err
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return atomicWriteFile(path, b, 0600)
}

func loadSession(profileName string) (*SessionFile, error) {
	path, err := sessionFilePath(profileName)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s SessionFile
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func saveSession(profileName string, s *SessionFile) error {
	path, err := sessionFilePath(profileName)
	if err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return atomicWriteFile(path, b, 0600)
}

func deleteSession(profileName string) error {
	path, err := sessionFilePath(profileName)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func runProfile(gf globalFlags, args []string) {
	if len(args) == 0 {
		die("usage: proofgatectl profile <list|add|use|delete> [flags]")
	}

	switch args[0] {
	case "list":
		cfg, err := loadProfileConfig()
		if err != nil {
			die("load profiles: %v", err)
		}
		if len(cfg.Profiles) == 0 {
			fmt.Println("No profiles configured.")
			return
		}
		names := make([]string, 0, len(cfg.Profiles))
		for name := range cfg.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			p := cfg.Profiles[name]
			prefix := "  "
			if name == cfg.ActiveProfile {
				prefix = "* "
			}
			insecureStr := ""
			if p.Insecure {
				insecureStr = " (insecure)"
			}
			fmt.Printf("%s%-16s %s%s\n", prefix, name, p.Server, insecureStr)
		}

	case "add":
		fs := flag.NewFlagSet("profile add", flag.ExitOnError)
		name := fs.String("name", "", "profile name")
		serverURL := fs.String("server", "", "server URL")
		insecure := fs.Bool("insecure", false, "skip TLS verification")
		_ = fs.Parse(args[1:])

		if *serverURL == "" && gf.server != "" {
			*serverURL = gf.server
		}
		if !*insecure && gf.insecure {
			*insecure = true
		}

		if *name == "" || *serverURL == "" {
			die("--name and --server are required")
		}

		cfg, err := loadProfileConfig()
		if err != nil {
			die("load profiles: %v", err)
		}

		cfg.Profiles[*name] = Profile{
			Server:   strings.TrimRight(*serverURL, "/"),
			Insecure: *insecure,
		}
		if cfg.ActiveProfile == "" {
			cfg.ActiveProfile = *name
		}

		if err := saveProfileConfig(cfg); err != nil {
			die("save profile: %v", err)
		}
		fmt.Printf("Profile %q added.\n", *name)

	case "use":
		fs := flag.NewFlagSet("profile use", flag.ExitOnError)
		name := fs.String("name", "", "profile name")
		_ = fs.Parse(args[1:])

		if *name == "" {
			die("--name is required")
		}

		cfg, err := loadProfileConfig()
		if err != nil {
			die("load profiles: %v", err)
		}

		if _, ok := cfg.Profiles[*name]; !ok {
			die("profile %q does not exist", *name)
		}

		cfg.ActiveProfile = *name
		if err := saveProfileConfig(cfg); err != nil {
			die("save profile: %v", err)
		}
		fmt.Printf("Switched to profile %q.\n", *name)

	case "delete":
		fs := flag.NewFlagSet("profile delete", flag.ExitOnError)
		name := fs.String("name", "", "profile name")
		_ = fs.Parse(args[1:])

		if *name == "" {
			die("--name is required")
		}

		cfg, err := loadProfileConfig()
		if err != nil {
			die("load profiles: %v", err)
		}

		if _, ok := cfg.Profiles[*name]; !ok {
			die("profile %q does not exist", *name)
		}

		delete(cfg.Profiles, *name)
		if cfg.ActiveProfile == *name {
			cfg.ActiveProfile = ""
			for remaining := range cfg.Profiles {
				cfg.ActiveProfile = remaining
				break
			}
		}

		if err := saveProfileConfig(cfg); err != nil {
			die("save profile: %v", err)
		}
		_ = deleteSession(*name)
		fmt.Printf("Profile %q deleted.\n", *name)

	default:
		die("unknown profile subcommand %q", args[0])
	}
}

func getActiveProfile(overrideProfile string) (string, Profile, error) {
	cfg, err := loadProfileConfig()
	if err != nil {
		return "", Profile{}, err
	}

	name := overrideProfile
	if name == "" {
		name = cfg.ActiveProfile
	}
	if name == "" {
		return "default", Profile{
			Server:   "http://127.0.0.1:9090",
			Insecure: false,
		}, nil
	}

	p, ok := cfg.Profiles[name]
	if !ok {
		return name, Profile{}, fmt.Errorf("profile %q not found", name)
	}
	return name, p, nil
}
