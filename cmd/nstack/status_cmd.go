package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/todd-chamberlain/nstack/pkg/state"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show deployment status",
	Long:  "Display the current deployment status of all stages or a specific stage.",
	RunE:  runStatus,
}

func init() {
	statusCmd.Flags().Int("stage", 0, "show detailed status for a specific stage number")
}

func runStatus(cmd *cobra.Command, args []string) error {
	if err := requireSite(); err != nil {
		return err
	}

	site, _, err := loadSiteAndProfile()
	if err != nil {
		return err
	}

	kc, _, err := createClients(site)
	if err != nil {
		return err
	}

	printer := createPrinter()
	registry := buildRegistry()
	store := state.NewStore(kc.Clientset())
	stageNum, _ := cmd.Flags().GetInt("stage")

	format := viper.GetString("output")

	// If a specific stage is requested, query live status.
	if stageNum > 0 {
		s, ok := registry.Get(stageNum)
		if !ok {
			return fmt.Errorf("stage %d not found in registry", stageNum)
		}

		status, err := s.Status(cmd.Context(), kc)
		if err != nil {
			return fmt.Errorf("querying stage %d status: %w", stageNum, err)
		}

		if format == "json" {
			data, err := json.MarshalIndent(status, "", "  ")
			if err != nil {
				return fmt.Errorf("marshaling stage %d status: %w", stageNum, err)
			}
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("\nStage %d: %s\n", status.Stage, status.Name)
		fmt.Printf("  Status:  %s\n", status.Status)
		if status.Version != "" {
			fmt.Printf("  Version: %s\n", status.Version)
		}
		if !status.Applied.IsZero() {
			fmt.Printf("  Applied: %s\n", status.Applied.Format("2006-01-02 15:04:05"))
		}
		if status.Error != "" {
			fmt.Printf("  Error:   %s\n", status.Error)
		}

		if len(status.Components) > 0 {
			fmt.Println("  Components:")
			for _, c := range status.Components {
				fmt.Printf("    %-25s %-15s %d/%d ready  (%s)\n",
					c.Name, c.Status, c.Ready, c.Pods, c.Namespace)
			}
		}

		return nil
	}

	// Overview mode: show all stages from state.
	currentState, err := store.Load(cmd.Context())
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	if format == "json" {
		data, err := json.MarshalIndent(currentState, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling deployment state: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	printer.Infof("NStack status for site %q:", site.Name)
	fmt.Println()

	stages := registry.All()
	for _, s := range stages {
		num := s.Number()
		name := s.Name()

		ss, ok := currentState.Stages[num]
		if !ok {
			fmt.Printf("  Stage %d: %-25s not-installed\n", num, name)
			continue
		}

		applied := ""
		if !ss.Applied.IsZero() {
			applied = ss.Applied.Format("2006-01-02 15:04:05")
		}
		fmt.Printf("  Stage %d: %-25s %-15s %s\n", num, name, ss.Status, applied)
		if ss.Error != "" {
			fmt.Printf("           Error: %s\n", ss.Error)
		}
	}

	return nil
}
