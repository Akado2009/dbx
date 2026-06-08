package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var branchCmd = &cobra.Command{
	Use:   "branch",
	Short: "Manage database branches",
}

var createFrom string
var createTTL string

var branchCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new branch",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		pid := mustProjectID()

		fmt.Printf("Creating branch %s...\n", name)
		reqBody := map[string]string{"name": name}
		if createFrom != "" {
			reqBody["from"] = createFrom
		}
		if createTTL != "" {
			reqBody["ttl"] = createTTL
		}
		body, _ := json.Marshal(reqBody)
		resp, err := doPost(fmt.Sprintf("%s/projects/%s/branches", serverURL(), pid), body)
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

		resp, err := doGet(fmt.Sprintf("%s/projects/%s/branches", serverURL(), pid))
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
			conn, _ := b["connection_string"].(string)
			fmt.Printf("  %-20s  %-8v  %-10s  %s\n",
				b["name"], b["pg_port"], b["status"], conn)
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

		resp, err := doGet(fmt.Sprintf("%s/projects/%s/branches/%s/diff", serverURL(), pid, name))
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

var rebaseContinue bool

var branchRebaseCmd = &cobra.Command{
	Use:   "rebase <name>",
	Short: "Rebase branch on top of main",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		pid := mustProjectID()

		url := fmt.Sprintf("%s/projects/%s/branches/%s/rebase", serverURL(), pid, name)
		if rebaseContinue {
			url += "/continue"
			fmt.Printf("Continuing rebase of %s...\n", name)
		} else {
			fmt.Printf("Rebasing %s on main...\n", name)
		}

		resp, err := doPost(url, nil)
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
			fmt.Fprintf(os.Stderr, "Resolve manually in the branch DB, then run:\n")
			fmt.Fprintf(os.Stderr, "  dbx branch rebase %s --continue\n", name)
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
		resp, err := doPost(fmt.Sprintf("%s/projects/%s/branches/%s/merge", serverURL(), pid, name), nil)
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

		resp, err := doDelete(fmt.Sprintf("%s/projects/%s/branches/%s", serverURL(), pid, name))
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

var branchStatusCmd = &cobra.Command{
	Use:   "status <name>",
	Short: "Show branch status vs main",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		pid := mustProjectID()

		resp, err := doGet(fmt.Sprintf("%s/projects/%s/branches/%s/status", serverURL(), pid, name))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		var s map[string]any
		json.NewDecoder(resp.Body).Decode(&s)

		if errMsg, ok := s["error"].(string); ok {
			fmt.Fprintf(os.Stderr, "error: %s\n", errMsg)
			os.Exit(1)
		}

		behindMain, _ := s["behind_main"].(bool)
		pendingMain := int(s["pending_main_changes"].(float64))
		pendingBranch := int(s["pending_branch_changes"].(float64))
		conflicts := int(s["conflicts"].(float64))

		fmt.Printf("Branch:  %s\n", name)
		fmt.Printf("Status:  %v\n", s["status"])

		if behindMain {
			fmt.Printf("Main:    ⚠  behind by %d change(s) — run: dbx branch rebase %s\n", pendingMain, name)
		} else {
			fmt.Printf("Main:    ✓  up to date\n")
		}

		if pendingBranch > 0 {
			fmt.Printf("Changes: %d change(s) ready to merge\n", pendingBranch)
		} else {
			fmt.Printf("Changes: none\n")
		}

		if conflicts > 0 {
			fmt.Printf("Conflicts: ✗ %d conflict(s) detected\n", conflicts)
		}
	},
}

func init() {
	branchCreateCmd.Flags().StringVar(&createFrom, "from", "", "Branch from another branch (default: main)")
	branchCreateCmd.Flags().StringVar(&createTTL, "ttl", "", "Auto-delete after duration, e.g. 24h, 7d")
	branchRebaseCmd.Flags().BoolVar(&rebaseContinue, "continue", false, "Continue rebase after resolving conflicts")
	branchCmd.AddCommand(
		branchCreateCmd,
		branchListCmd,
		branchDiffCmd,
		branchStatusCmd,
		branchRebaseCmd,
		branchMergeCmd,
		branchDeleteCmd,
	)
	rootCmd.AddCommand(branchCmd)
}
