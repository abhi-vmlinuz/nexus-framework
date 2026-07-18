// Package cmd defines the Cobra root command and all subcommands for nexus-cli.
package cmd

import (
	"fmt"
	"os"

	"strconv"

	"github.com/nexus-oss/nexus/nexus-cli/client"
	"github.com/nexus-oss/nexus/nexus-cli/config"
	"github.com/nexus-oss/nexus/nexus-cli/tui"
	"github.com/spf13/cobra"

	tea "github.com/charmbracelet/bubbletea"
)

// Execute is the CLI entrypoint — called from main.go.
func Execute() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var engineURL string
	var cfg *config.Config

	root := &cobra.Command{
		Use:   "nexus",
		Short: "Nexus Framework — control plane operator CLI",
		Long: `nexus is the operator CLI for the Nexus Framework infrastructure framework.

  Manage challenges, sessions, and inspect the reconciliation controller.
  Start the live TUI dashboard with: nexus tui

  Engine URL can be set via --engine flag or NEXUS_ENGINE_URL env var.`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := config.LoadConfigWithEnvFallback()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			cfg = loaded
			// Flag overrides config.
			if engineURL != "" {
				cfg.Engine.URL = engineURL
			}
			if envURL := os.Getenv("NEXUS_ENGINE_URL"); envURL != "" && engineURL == "" {
				cfg.Engine.URL = envURL
			}
			return nil
		},
	}

	root.PersistentFlags().StringVar(&engineURL, "engine", "", "Nexus engine URL (default: http://localhost:8081)")

	// All subcommands receive the client via a factory func so they get the
	// resolved cfg.Engine.URL after PersistentPreRunE runs.
	makeClient := func() *client.Client {
		if cfg == nil {
			cfg = &config.Config{}
			cfg.Engine.URL = "http://localhost:8081"
		}
		return client.New(cfg.Engine.URL, cfg.APIKey)
	}

	// ── Subcommands ──────────────────────────────────────────────────────────
	root.AddCommand(
		newTUICmd(makeClient),
		newStatusCmd(makeClient),
		newChallengeCmd(makeClient()),
		newSessionCmd(makeClient()),
		newAdminCmd(makeClient),
		newConfigCmd(makeClient),
		newVersionCmd(),
	)

	return root
}

// newTUICmd launches the live Bubbletea dashboard.
func newTUICmd(makeClient func() *client.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Launch the live TUI dashboard",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := makeClient()
			m := tui.New(c)
			p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
			_, err := p.Run()
			return err
		},
	}
}

// newStatusCmd shows a quick engine health check.
func newStatusCmd(makeClient func() *client.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show engine health and cluster overview",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := makeClient()

			h, err := c.Health()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Engine unreachable: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Engine: %s | mode=%s | time=%s\n", h.Status, h.Mode, h.Timestamp)

			if sys, err := c.SystemInfo(); err == nil {
				fmt.Printf("Sessions: %d  Pods: %d  Registry: %s\n",
					sys.SessionsTotal, sys.PodsTotal, sys.Registry)
			}
			if ctrl, err := c.ControllerStats(); err == nil {
				fmt.Printf("Controller: %s | workers=%d | queued=%d | in-flight=%d\n",
					ctrl.Status, ctrl.Workers, ctrl.Queued, ctrl.InFlight)
			}
			return nil
		},
	}
}

// newAdminCmd groups admin operations.
func newAdminCmd(makeClient func() *client.Client) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Admin operations (cluster health, reconcile trigger)",
	}
	cmd.AddCommand(
		newAdminHealthCmd(makeClient),
		newAdminReconcileCmd(makeClient),
	)
	return cmd
}

func newAdminHealthCmd(makeClient func() *client.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Full cluster health (Redis + node agent + k3s)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := makeClient()
			resp, err := c.ClusterHealth()
			if err != nil {
				return err
			}
			for k, v := range resp {
				fmt.Printf("  %-16s %v\n", k+":", v)
			}
			return nil
		},
	}
}

func newAdminReconcileCmd(makeClient func() *client.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "reconcile",
		Short: "Trigger an immediate reconcile for all active sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := makeClient()
			resp, err := c.TriggerReconcile()
			if err != nil {
				return err
			}
			fmt.Printf("Reconcile triggered: %v session(s)\n", resp["sessions"])
			return nil
		},
	}
}

// newConfigCmd shows/edits CLI configuration.
func newConfigCmd(makeClient func() *client.Client) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage nexus-cli configuration",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "view",
		Short: "View current configuration (CLI and remote Engine)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := makeClient()

			// 1. Local CLI Config
			cfg, err := config.LoadConfigWithEnvFallback()
			if err != nil {
				return err
			}
			cfg.CheckEnvMismatch()
			fmt.Printf("--- LOCAL CLI CONFIGURATION ---\n")
			cfg.Display()

			// 2. Remote Engine Config
			fmt.Printf("\n--- REMOTE ENGINE CONFIGURATION (Hot-Reloadable) ---\n")
			engineCfg, err := c.GetEngineConfig()
			if err != nil {
				fmt.Printf("  [!] Engine unreachable: %v\n", err)
				fmt.Printf("  (Verify engine is running and engine.url is correct in CLI config)\n")
			} else {
				// Display Engine Config (Soft + Hard)
				fmt.Printf("  URL:           %s\n", cfg.Engine.URL)
				fmt.Printf("  Mode:          %v\n", engineCfg["mode"])
				fmt.Printf("  Namespace:     %v\n", engineCfg["k3s_namespace"])

				if ch, ok := engineCfg["challenge"].(map[string]any); ok {
					fmt.Printf("\n  Default Challenge Limits:\n")
					fmt.Printf("    CPU:         %v\n", ch["default_cpu_limit"])
					fmt.Printf("    Memory:      %v\n", ch["default_memory_limit"])
				}

				if sess, ok := engineCfg["session"].(map[string]any); ok {
					fmt.Printf("\n  Session Lifecycle:\n")
					fmt.Printf("    Default TTL: %v min\n", sess["default_ttl_minutes"])
					fmt.Printf("    Max Per User: %v\n", sess["max_sessions_per_user"])
				}

				if rec, ok := engineCfg["reconciler"].(map[string]any); ok {
					fmt.Printf("\n  Reconciler:\n")
					fmt.Printf("    Workers:     %v\n", rec["max_workers"])
				}
			}

			return nil
		},
	})

	engineKeys := []string{
		"challenge.cpu",
		"challenge.memory",
		"session.ttl",
		"session.max_per_user",
		"reconciler.workers",
	}
	cliKeys := []string{
		"engine.url",
		"engine.mode",
		"registry.type",
		"registry.url",
		"registry.auth.type",
		"registry.auth.username",
		"registry.auth.password",
		"redis.url",
		"node_agent.addr",
		"k8s.namespace",
	}
	allKeys := append(engineKeys, cliKeys...)

	cmd.AddCommand(&cobra.Command{
		Use:       "set <key> <value>",
		Short:     "Set a configuration value (local CLI or remote Engine)",
		ValidArgs: allKeys,
		Args:      cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			val := args[1]

			// 1. Try Engine Hot-Reload if key matches
			isEngineKey := false
			for _, k := range engineKeys {
				if k == key {
					isEngineKey = true
					break
				}
			}

			if isEngineKey {
				c := makeClient()
				req := client.UpdateConfigRequest{}
				switch key {
				case "challenge.cpu":
					req.DefaultCPULimit = &val
				case "challenge.memory":
					req.DefaultMemoryLimit = &val
				case "session.ttl":
					v, _ := strconv.Atoi(val)
					req.DefaultTTLMinutes = &v
				case "session.max_per_user":
					v, _ := strconv.Atoi(val)
					req.MaxSessionsPerUser = &v
				case "reconciler.workers":
					v, _ := strconv.Atoi(val)
					req.MaxWorkers = &v
				}

				resp, err := c.UpdateConfig(req)
				if err != nil {
					return fmt.Errorf("failed to update engine config: %w", err)
				}
				fmt.Printf("✓ Engine: %s\n", resp["message"])
				return nil
			}

			// 2. Fallback to Local CLI Config
			cfg, err := config.LoadConfig()
			if err != nil {
				cfg = &config.Config{}
			}
			if err := cfg.Set(key, val); err != nil {
				return fmt.Errorf("failed to set local config %s: %w", key, err)
			}
			fmt.Printf("✓ CLI: updated %s = %s\n", key, val)
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Initialize configuration interactively",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := &config.Config{}

			prompt := func(msg, def string) string {
				fmt.Printf("%s [%s]: ", msg, def)
				var input string
				fmt.Scanln(&input)
				if input == "" {
					return def
				}
				return input
			}

			cfg.Engine.URL = prompt("Engine URL", "http://localhost:8081")
			cfg.Engine.Mode = prompt("Engine Mode", "dev")
			cfg.Registry.Type = prompt("Registry Type", "local")
			cfg.Registry.URL = prompt("Registry URL", "localhost:5000")
			cfg.Redis.URL = prompt("Redis URL", "redis://localhost:6379")
			cfg.NodeAgent.Addr = prompt("Node Agent Addr", "localhost:50051")
			cfg.K8s.Namespace = prompt("K8s Namespace", "nexus-challenges")

			if err := cfg.Save(); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			fmt.Printf("\nConfig created: %s\n", config.Path())
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "validate",
		Short: "Validate current configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadConfigWithEnvFallback()
			if err != nil {
				return err
			}
			fmt.Println("Validating config...")
			if err := cfg.Validate(); err != nil {
				fmt.Printf("\nConfig has errors. See above.\n")
				return err
			}
			fmt.Printf("\nConfig is valid!\n")
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "reset",
		Short: "Delete current configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.Path()
			fmt.Printf("WARNING: This will delete your current config at:\n  %s\n\n", path)
			fmt.Printf("Are you sure? [y/N]: ")
			var input string
			fmt.Scanln(&input)
			if input == "y" || input == "Y" {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("failed to delete config: %w", err)
				}
				fmt.Println("Config deleted.")
				fmt.Println("To create a new config: nexus config init")
			} else {
				fmt.Println("Aborted.")
			}
			return nil
		},
	})

	cmd.AddCommand(newConfigRegistryCmd(makeClient))

	return cmd
}

func newConfigRegistryCmd(makeClient func() *client.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "registry",
		Short: "Configure container registry for nexus-engine (GHCR, Docker Hub, etc.)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := makeClient()

			fmt.Println("Nexus Registry Configuration")
			fmt.Println("----------------------------")

			prompt := func(msg, def string) string {
				fmt.Printf("%s [%s]: ", msg, def)
				var input string
				fmt.Scanln(&input)
				if input == "" {
					return def
				}
				return input
			}

			fmt.Println("\nChoose registry type:")
			fmt.Println("  1) Docker Hub (index.docker.io)")
			fmt.Println("  2) GitHub Container Registry (ghcr.io)")
			fmt.Println("  3) AWS ECR")
			fmt.Println("  4) Private/Custom")
			fmt.Println("  5) Local (no auth)")

			choice := prompt("Select (1-5)", "1")
			var url, authType string

			switch choice {
			case "1":
				url = "index.docker.io"
				authType = "basic"
			case "2":
				url = "ghcr.io"
				authType = "ghcr"
			case "3":
				url = prompt("ECR URL (e.g. 123456789.dkr.ecr.us-east-1.amazonaws.com)", "")
				authType = "awsecr"
			case "4":
				url = prompt("Registry URL", "localhost:5000")
				authType = "basic"
			case "5":
				url = "localhost:5000"
				authType = "none"
			default:
				return fmt.Errorf("invalid choice")
			}

			var user, pass string
			if authType != "none" && authType != "awsecr" {
				user = prompt("Username", "")
				pass = prompt("Password/Token", "")
			}

			// For Docker Hub and GHCR, the URL often needs the username suffix for pushes
			if (choice == "1" || choice == "2") && user != "" {
				fmt.Printf("\nNexus usually pushes to %s/%s. Correct? [Y/n]: ", url, user)
				var confirm string
				fmt.Scanln(&confirm)
				if confirm == "" || confirm == "y" || confirm == "Y" {
					url = fmt.Sprintf("%s/%s", url, user)
				}
			}

			fmt.Printf("\nUpdating engine registry config to %s (%s)...\n", url, authType)

			_, err := c.UpdateRegistry(client.UpdateRegistryRequest{
				URL:      url,
				AuthType: authType,
				Username: user,
				Password: pass,
			})
			if err != nil {
				return fmt.Errorf("engine update failed: %w", err)
			}

			// Also update local CLI config to stay in sync
			if localCfg, err := config.LoadConfig(); err == nil {
				localCfg.Registry.Type = authType
				localCfg.Registry.URL = url
				localCfg.Registry.Auth.Username = user
				localCfg.Registry.Auth.Password = pass
				localCfg.Save()
			}

			fmt.Println("✓ Registry configuration updated successfully.")
			fmt.Println("✓ Docker credentials synchronized to engine.")
			fmt.Println("✓ Kubernetes imagePullSecret synchronized.")

			return nil
		},
	}
}
