package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage projects",
}

var projectInitCmd = &cobra.Command{
	Use:   "init <name> <conn-string>",
	Short: "Connect a Postgres database as a new project",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		name, connStr := args[0], args[1]

		body, _ := json.Marshal(map[string]string{
			"name":              name,
			"connection_string": connStr,
		})
		resp, err := doPost(fmt.Sprintf("%s/projects", serverURL()), body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		var result map[string]any
		json.NewDecoder(resp.Body).Decode(&result)

		if errMsg, ok := result["error"].(string); ok {
			fmt.Fprintf(os.Stderr, "error: %s\n", errMsg)
			os.Exit(1)
		}

		id := fmt.Sprintf("%v", result["id"])

		// save to ~/.dbx/config.yaml automatically
		cfg := globalConfig
		if cfg == nil {
			cfg = &Config{}
		}
		if cfg.Projects == nil {
			cfg.Projects = map[string]Project{}
		}
		cfg.Projects[name] = Project{ID: id, Name: name}
		// if this is the only project, set as active
		if len(cfg.Projects) == 1 {
			cfg.ProjectID = id
		}
		if err := saveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save config: %v\n", err)
		}

		// also write .dbx in current dir
		os.WriteFile(".dbx", []byte(id+"\n"), 0644)

		fmt.Printf("✓ Project created: %s\n", name)
		fmt.Printf("  ID: %s\n", id)
		fmt.Printf("  Saved to ~/.dbx/config.yaml and .dbx\n")
		fmt.Printf("\nYou can now run dbx commands without -p flag.\n")
	},
}

var projectUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Set active project (writes .dbx in current dir)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		// look up by name in config
		if globalConfig != nil {
			if p, ok := globalConfig.Projects[name]; ok {
				os.WriteFile(".dbx", []byte(p.ID+"\n"), 0644)
				fmt.Printf("✓ Active project: %s (%s)\n", name, p.ID)
				fmt.Printf("  .dbx written to current directory\n")
				return
			}
		}

		// maybe name is actually an ID
		os.WriteFile(".dbx", []byte(name+"\n"), 0644)
		fmt.Printf("✓ Active project set to: %s\n", name)
		fmt.Printf("  .dbx written to current directory\n")
	},
}

var projectListCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all projects",
	Run: func(cmd *cobra.Command, args []string) {
		resp, err := doGet(fmt.Sprintf("%s/projects", serverURL()))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		var result []map[string]any
		json.NewDecoder(resp.Body).Decode(&result)

		if len(result) == 0 {
			fmt.Println("  no projects")
			return
		}

		// mark active project
		active := ""
		if data, err := os.ReadFile(".dbx"); err == nil {
			active = string(data)
			for len(active) > 0 && (active[len(active)-1] == '\n' || active[len(active)-1] == '\r') {
				active = active[:len(active)-1]
			}
		}

		fmt.Printf("  %-36s  %-20s\n", "ID", "NAME")
		fmt.Printf("  %-36s  %-20s\n", "--", "----")
		for _, p := range result {
			marker := "  "
			if fmt.Sprintf("%v", p["id"]) == active {
				marker = "* "
			}
			fmt.Printf("%s%-36s  %s\n", marker, p["id"], p["name"])
		}
	},
}

func init() {
	projectCmd.AddCommand(projectInitCmd, projectListCmd, projectUseCmd)
	rootCmd.AddCommand(projectCmd)
}
