package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var registerCmd = &cobra.Command{
	Use:   "register",
	Short: "Create a new account on the dbx server",
	Run: func(cmd *cobra.Command, args []string) {
		email := promptEmail()
		body, _ := json.Marshal(map[string]string{"email": email})
		resp, err := doPostNoAuth(serverURL()+"/auth/register", body)
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

		token, _ := result["token"].(string)
		saveToken(email, token)
		fmt.Printf("✓ Registered as %s\n", email)
		fmt.Printf("  Token saved to ~/.dbx/config.yaml\n")
	},
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in and get a new API token",
	Run: func(cmd *cobra.Command, args []string) {
		email := promptEmail()
		body, _ := json.Marshal(map[string]string{"email": email})
		resp, err := doPostNoAuth(serverURL()+"/auth/login", body)
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

		token, _ := result["token"].(string)
		saveToken(email, token)
		fmt.Printf("✓ Logged in as %s\n", email)
		fmt.Printf("  Token saved to ~/.dbx/config.yaml\n")
	},
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Revoke current token",
	Run: func(cmd *cobra.Command, args []string) {
		resp, err := doDelete(serverURL() + "/auth/token")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		// clear token from config
		if globalConfig != nil {
			globalConfig.APIKey = ""
			saveConfig(globalConfig)
		}
		fmt.Println("✓ Logged out")
	},
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show current logged in user",
	Run: func(cmd *cobra.Command, args []string) {
		resp, err := doGet(serverURL() + "/auth/me")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()

		var result map[string]any
		json.NewDecoder(resp.Body).Decode(&result)

		if errMsg, ok := result["error"].(string); ok {
			fmt.Fprintf(os.Stderr, "not logged in: %s\n", errMsg)
			os.Exit(1)
		}

		fmt.Printf("Logged in as: %s\n", result["email"])
		fmt.Printf("User ID:      %s\n", result["user_id"])
		fmt.Printf("Tokens:       %v active\n", result["tokens"])
	},
}

func promptEmail() string {
	fmt.Print("Email: ")
	reader := bufio.NewReader(os.Stdin)
	email, _ := reader.ReadString('\n')
	return strings.TrimSpace(email)
}

func saveToken(email, token string) {
	cfg := globalConfig
	if cfg == nil {
		cfg = &Config{Server: serverURL()}
	}
	cfg.APIKey = token
	if err := saveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save config: %v\n", err)
		fmt.Printf("  Token: %s\n", token)
	}
}

func init() {
	rootCmd.AddCommand(registerCmd)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
	rootCmd.AddCommand(whoamiCmd)
}
