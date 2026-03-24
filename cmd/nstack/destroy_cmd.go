package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/todd-chamberlain/nstack/pkg/engine"
	"github.com/todd-chamberlain/nstack/pkg/state"
)

var destroyCmd = &cobra.Command{
	Use:   "destroy",
	Short: "Remove deployed components",
	Long:  "Destroy deployed NStack components in reverse order. Use --stage to remove a specific stage.",
	RunE:  runDestroy,
}

func init() {
	destroyCmd.Flags().Int("stage", 0, "destroy only this stage number")
	destroyCmd.Flags().Int("from", 0, "destroy stages starting from this number (inclusive, in reverse)")
}

func runDestroy(cmd *cobra.Command, args []string) error {
	if err := requireSite(); err != nil {
		return err
	}

	site, _, err := loadSiteAndProfile()
	if err != nil {
		return err
	}

	kc, hc, err := createClients(site)
	if err != nil {
		return err
	}

	printer := createPrinter()
	registry := buildRegistry()
	store := state.NewStore(kc.Clientset())

	stageNum, _ := cmd.Flags().GetInt("stage")
	fromNum, _ := cmd.Flags().GetInt("from")

	// Resolve which stages to destroy.
	var stages []engine.Stage
	switch {
	case stageNum > 0:
		s, ok := registry.Get(stageNum)
		if !ok {
			return fmt.Errorf("stage %d not found in registry", stageNum)
		}
		stages = []engine.Stage{s}
	case fromNum > 0:
		all := registry.All()
		for _, s := range all {
			if s.Number() >= fromNum {
				stages = append(stages, s)
			}
		}
	default:
		stages = registry.All()
	}

	if len(stages) == 0 {
		printer.Infof("No stages to destroy.")
		return nil
	}

	// Sort in REVERSE order for destruction.
	sort.Slice(stages, func(i, j int) bool {
		return stages[i].Number() > stages[j].Number()
	})

	// Build description of what will be destroyed.
	names := make([]string, len(stages))
	for i, s := range stages {
		names[i] = fmt.Sprintf("%d (%s)", s.Number(), s.Name())
	}

	// Prompt for confirmation unless --yes is set.
	if !viper.GetBool("yes") {
		fmt.Printf("This will destroy the following stages: %s\n", strings.Join(names, ", "))
		fmt.Print("Are you sure? [y/N] ")
		var response string
		fmt.Scanln(&response)
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			printer.Infof("Aborted.")
			return nil
		}
	}

	// Load current state.
	currentState, err := store.Load(cmd.Context())
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	// Ensure the namespace exists for state updates.
	if err := store.EnsureNamespace(cmd.Context()); err != nil {
		return fmt.Errorf("ensuring namespace: %w", err)
	}

	// Destroy each stage in reverse order.
	for _, s := range stages {
		num := s.Number()
		name := s.Name()

		printer.StageHeader(num, name+" (destroy)")

		if err := s.Destroy(cmd.Context(), kc, hc, printer); err != nil {
			// Record failure in state.
			currentState.Stages[num] = &state.StageState{
				Status:  "failed",
				Applied: time.Now(),
				Error:   fmt.Sprintf("destroy failed: %v", err),
			}
			_ = store.Save(cmd.Context(), currentState)
			return fmt.Errorf("destroying stage %d (%s): %w", num, name, err)
		}

		// Remove stage from state.
		delete(currentState.Stages, num)
		if err := store.Save(cmd.Context(), currentState); err != nil {
			return fmt.Errorf("saving state after destroying stage %d: %w", num, err)
		}
	}

	printer.Infof("\nDestroy complete.")
	return nil
}
