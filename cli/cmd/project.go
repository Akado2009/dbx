package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
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
		resp, err := http.Post(
			fmt.Sprintf("%s/projects", serverURL()),
			"application/json",
			bytes.NewReader(body),
		)
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

		fmt.Printf("✓ Project created: %s (id: %s)\n", name, result["id"])
		fmt.Printf("\nAdd to ~/.dbx/config.yaml:\n")
		fmt.Printf("  projects:\n    %s:\n      id: %s\n", name, result["id"])
	},
}

var projectListCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all projects",
	Run: func(cmd *cobra.Command, args []string) {
		resp, err := http.Get(fmt.Sprintf("%s/projects", serverURL()))
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

		fmt.Printf("  %-36s  %s\n", "ID", "NAME")
		fmt.Printf("  %-36s  %s\n", "--", "----")
		for _, p := range result {
			fmt.Printf("  %-36s  %s\n", p["id"], p["name"])
		}
	},
}

func init() {
	projectCmd.AddCommand(projectInitCmd, projectListCmd)
	rootCmd.AddCommand(projectCmd)
}
