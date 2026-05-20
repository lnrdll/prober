package cmd

import (
	"fmt"
	"os"

	"lnrdll/prober/internal/config"

	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
)

func addConfigFlag(command *cobra.Command, target *string) {
	command.Flags().StringVarP(target, "config", "c", "", "Path to the target manifest YAML file")
	_ = command.MarkFlagRequired("config")
}

func loadConfig(path string) (config.Config, error) {
	configFile, err := os.ReadFile(path)
	if err != nil {
		return config.Config{}, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg config.Config
	if err := yaml.Unmarshal(configFile, &cfg); err != nil {
		return config.Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}

	return cfg, nil
}
