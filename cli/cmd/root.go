package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server  string            `yaml:"server"`
	Projects map[string]Project `yaml:"projects"`
}

type Project struct {
	ID   string `yaml:"id"`
	Main string `yaml:"main"`
}

var (
	cfgFile    string
	projectID  string
	globalConfig *Config
)

var rootCmd = &cobra.Command{
	Use:   "dbx",
	Short: "Git-like branching for Postgres",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/.dbx/config.yaml)")
	rootCmd.PersistentFlags().StringVarP(&projectID, "project", "p", "", "project ID")
	cobra.OnInitialize(loadConfig)
}

func loadConfig() {
	path := cfgFile
	if path == "" {
		home, _ := os.UserHomeDir()
		path = home + "/.dbx/config.yaml"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	globalConfig = &Config{}
	yaml.Unmarshal(data, globalConfig)
}

func serverURL() string {
	if globalConfig != nil && globalConfig.Server != "" {
		return globalConfig.Server
	}
	return "http://localhost:7070"
}

func mustProjectID() string {
	if projectID != "" {
		return projectID
	}
	if globalConfig != nil {
		for id := range globalConfig.Projects {
			return id
		}
	}
	fmt.Fprintln(os.Stderr, "error: project not specified. Use -p <project-id> or set in ~/.dbx/config.yaml")
	os.Exit(1)
	return ""
}
