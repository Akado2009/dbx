package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    string             `yaml:"server"`
	APIKey    string             `yaml:"api_key"`
	Projects  map[string]Project `yaml:"projects"`
	ProjectID string             `yaml:"project_id"` // active project for this dir
}

type Project struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
}

var (
	cfgFile      string
	projectID    string
	globalConfig *Config
	globalCfgPath string
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

func configPath() string {
	if cfgFile != "" {
		return cfgFile
	}
	home, _ := os.UserHomeDir()
	return home + "/.dbx/config.yaml"
}

func loadConfig() {
	globalCfgPath = configPath()
	data, err := os.ReadFile(globalCfgPath)
	if err != nil {
		return
	}
	globalConfig = &Config{}
	yaml.Unmarshal(data, globalConfig)
}

func saveConfig(cfg *Config) error {
	path := configPath()
	if err := os.MkdirAll(path[:len(path)-len("/config.yaml")], 0755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func serverURL() string {
	if globalConfig != nil && globalConfig.Server != "" {
		return globalConfig.Server
	}
	return "http://localhost:7070"
}

// apiKey returns the API key from config or DBX_API_KEY env var.
func apiKey() string {
	if k := os.Getenv("DBX_API_KEY"); k != "" {
		return k
	}
	if globalConfig != nil {
		return globalConfig.APIKey
	}
	return ""
}

// newRequest creates an HTTP request with auth header if API key is set.
func newRequest(method, url string, body []byte) (*http.Request, error) {
	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequest(method, url, bytes.NewReader(body))
	} else {
		req, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		return nil, err
	}
	if key := apiKey(); key != "" {
		req.Header.Set("X-API-Key", key)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// doPostNoAuth sends a POST without attaching the API key (for register/login).
func doPostNoAuth(url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

func doGet(url string) (*http.Response, error) {
	req, err := newRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

func doPost(url string, body []byte) (*http.Response, error) {
	req, err := newRequest(http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

func doDelete(url string) (*http.Response, error) {
	req, err := newRequest(http.MethodDelete, url, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

// mustProjectID returns the active project ID from:
// 1. -p flag
// 2. .dbx file in current directory
// 3. global config active project
// 4. only project in config (if just one)
func mustProjectID() string {
	if projectID != "" {
		return projectID
	}

	// check .dbx file in current dir (like .git)
	if data, err := os.ReadFile(".dbx"); err == nil {
		id := string(data)
		if len(id) > 0 {
			// trim newline
			for len(id) > 0 && (id[len(id)-1] == '\n' || id[len(id)-1] == '\r') {
				id = id[:len(id)-1]
			}
			if id != "" {
				return id
			}
		}
	}

	if globalConfig != nil {
		// active project set explicitly
		if globalConfig.ProjectID != "" {
			return globalConfig.ProjectID
		}
		// only one project — use it automatically
		if len(globalConfig.Projects) == 1 {
			for _, p := range globalConfig.Projects {
				return p.ID
			}
		}
	}

	fmt.Fprintln(os.Stderr, "error: no project selected.")
	fmt.Fprintln(os.Stderr, "  run: dbx project use <name>")
	fmt.Fprintln(os.Stderr, "  or:  dbx project init <name> <conn-string>")
	fmt.Fprintln(os.Stderr, "  or:  use -p <project-id>")
	os.Exit(1)
	return ""
}
