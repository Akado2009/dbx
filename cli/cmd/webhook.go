package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var webhookCmd = &cobra.Command{
	Use:   "webhook",
	Short: "Manage project webhooks",
}

var webhookEvents string

var webhookAddCmd = &cobra.Command{
	Use:   "add <url>",
	Short: "Register a webhook URL",
	Long: `Register a webhook URL for one or more events.

Available events:
  created       — branch was created
  conflict      — rebase detected conflicts
  merged        — branch was merged into main
  ttl_warning   — branch expires in < 24h
  behind_main   — branch is behind main (future)

Example:
  dbx webhook add https://hooks.slack.com/... --events conflict,merged`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		url := args[0]
		pid := mustProjectID()

		events := strings.Split(webhookEvents, ",")
		for i := range events {
			events[i] = strings.TrimSpace(events[i])
		}

		body, _ := json.Marshal(map[string]any{
			"url":    url,
			"events": events,
		})
		resp, err := doPost(fmt.Sprintf("%s/projects/%s/webhooks", serverURL(), pid), body)
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

		fmt.Printf("✓ Webhook registered\n")
		fmt.Printf("  URL:    %s\n", url)
		fmt.Printf("  Events: %s\n", webhookEvents)
		fmt.Printf("  ID:     %v\n", result["id"])
	},
}

var webhookListCmd = &cobra.Command{
	Use:   "ls",
	Short: "List webhooks for current project",
	Run: func(cmd *cobra.Command, args []string) {
		pid := mustProjectID()

		resp, err := doGet(fmt.Sprintf("%s/projects/%s/webhooks", serverURL(), pid))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		var result []map[string]any
		json.NewDecoder(resp.Body).Decode(&result)

		if len(result) == 0 {
			fmt.Println("  no webhooks configured")
			return
		}

		fmt.Printf("  %-36s  %-30s  %s\n", "ID", "URL", "EVENTS")
		fmt.Printf("  %-36s  %-30s  %s\n", "--", "---", "------")
		for _, w := range result {
			id := fmt.Sprintf("%v", w["id"])
			url := fmt.Sprintf("%v", w["url"])
			if len(url) > 28 {
				url = url[:25] + "..."
			}
			eventsRaw, _ := w["events"].([]any)
			events := make([]string, len(eventsRaw))
			for i, e := range eventsRaw {
				events[i] = fmt.Sprintf("%v", e)
			}
			fmt.Printf("  %-36s  %-30s  %s\n", id, url, strings.Join(events, ","))
		}
	},
}

var webhookDeleteCmd = &cobra.Command{
	Use:   "rm <id>",
	Short: "Remove a webhook",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		pid := mustProjectID()

		resp, err := doDelete(fmt.Sprintf("%s/projects/%s/webhooks/%s", serverURL(), pid, id))
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
		fmt.Printf("✓ Webhook %s removed\n", id)
	},
}

func init() {
	webhookAddCmd.Flags().StringVar(&webhookEvents, "events", "conflict,merged", "Comma-separated list of events")
	webhookCmd.AddCommand(webhookAddCmd, webhookListCmd, webhookDeleteCmd)
	rootCmd.AddCommand(webhookCmd)
}
