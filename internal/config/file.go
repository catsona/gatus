package config

import (
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// Load reads settings without changing an existing file.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	return Parse(data)
}

// Save validates and replaces the file atomically with private permissions.
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}

	file, err := os.CreateTemp(directory, ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())

	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}

	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}

	if err := file.Close(); err != nil {
		return err
	}

	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}

	return nil
}
