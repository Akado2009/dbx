package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

var branchCmd = &cobra.Command{
	Use:   "branch",
	Short: "Manage database branches",
}

var branchCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new branch",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		pid := mustProjectID()

		fmt.Printf("Creating branch %s...\n", name)
		body, _ := json.Marshal(map[string]string{"name": name})
		resp, err := http.Post(
			fmt.Sprintf("%s/projects/%s/branches", serverURL(), pid),
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

		fmt.Printf("✓ Branch created: %s\n", name)
		if conn, ok := result["connection_string"].(string); ok {
			fmt.Printf("  Connection: %s\n", conn)
		}
	},
}

var branchListCmd = &cobra.Command{
	Use:   "ls",
	Short: "List branches",
	Run: func(cmd *cobra.Command, args []string) {
		pid := mustProjectID()

		resp, err := http.Get(fmt.Sprintf("%s/projects/%s/branches", serverURL(), pid))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		var result []map[string]any
		json.NewDecoder(resp.Body).Decode(&result)

		if len(result) == 0 {
			fmt.Println("  no branches")
			return
		}

		fmt.Printf("  %-20s  %-8s  %-10s  %s\n", "NAME", "PORT", "STATUS", "CONNECTION")
		fmt.Printf("  %-20s  %-8s  %-10s  %s\n", "----", "----", "------", "----------")
		for _, b := range result {
			port := fmt.Sprintf("%v", b["pg_port"])
			conn := fmt.Sprintf("postgresql://localhost:%s/%s", port, projectDB(pid))
			fmt.Printf("  %-20s  %-8s  %-10s  %s\n",
				b["name"], port, b["status"], conn)
		}
	},
}

var branchDiffCmd = &cobra.Command{
	Use:   "diff <name>",
	Short: "Show changes in a branch vs main",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		pid := mustProjectID()

		resp, err := http.Get(fmt.Sprintf("%s/projects/%s/branches/%s/diff", serverURL(), pid, name))
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

		changes, _ := result["changes"].([]any)
		if len(changes) == 0 {
			fmt.Println("  no changes")
			return
		}

		fmt.Printf("  %d change(s) in branch %s:\n\n", len(changes), name)
		for _, raw := range changes {
			c, _ := raw.(map[string]any)
			op := fmt.Sprintf("%v", c["operation"])
			table := fmt.Sprintf("%v", c["table"])
			pk := fmt.Sprintf("%v", c["primary_key"])

			switch op {
			case "INSERT":
				fmt.Printf("  + INSERT  %s  (id=%s)\n", table, pk)
				printRow("    ", c["new_row"])
			case "UPDATE":
				fmt.Printf("  ~ UPDATE  %s  (id=%s)\n", table, pk)
				printDiff("    ", c["old_row"], c["new_row"])
			case "DELETE":
				fmt.Printf("  - DELETE  %s  (id=%s)\n", table, pk)
			}
		}
	},
}

var branchRebaseCmd = &cobra.Command{
	Use:   "rebase <name>",
	Short: "Rebase branch on top of main",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		pid := mustProjectID()

		fmt.Printf("Rebasing %s on main...\n", name)
		resp, err := http.Post(
			fmt.Sprintf("%s/projects/%s/branches/%s/rebase", serverURL(), pid, name),
			"application/json",
			nil,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		var result map[string]any
		json.NewDecoder(resp.Body).Decode(&result)

		if conflicts, ok := result["conflicts"].([]any); ok && len(conflicts) > 0 {
			fmt.Fprintf(os.Stderr, "✗ Conflicts detected (%d):\n\n", len(conflicts))
			for _, raw := range conflicts {
				c, _ := raw.(map[string]any)
				fmt.Fprintf(os.Stderr, "  table=%v  id=%v  column=%v\n",
					c["table"], c["primary_key"], c["column"])
				fmt.Fprintf(os.Stderr, "    main:   %v\n", c["main_value"])
				fmt.Fprintf(os.Stderr, "    yours:  %v\n", c["your_value"])
				fmt.Fprintln(os.Stderr)
			}
			fmt.Fprintln(os.Stderr, "Resolve manually and run: dbx branch rebase --continue")
			os.Exit(1)
		}

		if errMsg, ok := result["error"].(string); ok {
			fmt.Fprintf(os.Stderr, "error: %s\n", errMsg)
			os.Exit(1)
		}

		fmt.Printf("✓ Branch %s rebased on main\n", name)
	},
}

var branchMergeCmd = &cobra.Command{
	Use:   "merge <name>",
	Short: "Merge branch into main",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		pid := mustProjectID()

		fmt.Printf("Merging %s into main...\n", name)
		resp, err := http.Post(
			fmt.Sprintf("%s/projects/%s/branches/%s/merge", serverURL(), pid, name),
			"application/json",
			nil,
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

		fmt.Printf("✓ Branch %s merged into main\n", name)
	},
}

var branchDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a branch and stop its PG instance",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		pid := mustProjectID()

		req, _ := http.NewRequest(
			http.MethodDelete,
			fmt.Sprintf("%s/projects/%s/branches/%s", serverURL(), pid, name),
			nil,
		)
		resp, err := http.DefaultClient.Do(req)
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

		fmt.Printf("✓ Branch %s deleted\n", name)
	},
}

// projectDB returns the database name for a project from config.
func projectDB(pid string) string {
	if globalConfig != nil {
		for _, p := range globalConfig.Projects {
			if p.ID == pid {
				return "myapp" // TODO: store db name in config
			}
		}
	}
	return "myapp"
}

func printRow(indent string, raw any) {
	row, _ := raw.(map[string]any)
	for k, v := range row {
		fmt.Printf("%s%s: %v\n", indent, k, v)
	}
}

func printDiff(indent string, oldRaw, newRaw any) {
	oldRow, _ := oldRaw.(map[string]any)
	newRow, _ := newRaw.(map[string]any)
	for k, newVal := range newRow {
		oldVal := oldRow[k]
		if fmt.Sprintf("%v", oldVal) != fmt.Sprintf("%v", newVal) {
			fmt.Printf("%s%s: %v → %v\n", indent, k, oldVal, newVal)
		}
	}
}

func init() {
	branchCmd.AddCommand(
		branchCreateCmd,
		branchListCmd,
		branchDiffCmd,
		branchRebaseCmd,
		branchMergeCmd,
		branchDeleteCmd,
	)
	rootCmd.AddCommand(branchCmd)
}
