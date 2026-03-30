package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/todd-chamberlain/nstack/pkg/state"
)

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Show deployment plan without applying",
	Long:  "Compute and display the deployment plan for all stages without making changes.",
	RunE:  runPlan,
}

func init() {
	planCmd.Flags().Int("from", 0, "start from this stage number")
	planCmd.Flags().Int("only", 0, "run only this stage number")
	planCmd.Flags().String("stages", "", "comma-separated list of stage numbers (e.g., \"4,6\")")
}

func runPlan(cmd *cobra.Command, args []string) error {
	if err := requireSite(); err != nil {
		return err
	}

	site, profile, err := loadSiteAndProfile()
	if err != nil {
		return err
	}

	kc, _, err := createClients(site)
	if err != nil {
		return err
	}

	printer := createPrinter()
	registry := buildRegistry()
	store := state.NewStore(kc.Clientset(), site.Name)

	currentState, err := store.Load(cmd.Context())
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	from, _ := cmd.Flags().GetInt("from")
	only, _ := cmd.Flags().GetInt("only")
	stagesFlag, _ := cmd.Flags().GetString("stages")

	resolveOpts, err := parseResolveOpts(from, only, stagesFlag)
	if err != nil {
		return err
	}

	stages, err := registry.Resolve(resolveOpts, currentState)
	if err != nil {
		return fmt.Errorf("resolving stages: %w", err)
	}

	printer.Infof("Deployment plan for site %q (profile: %s):", site.Name, profile.Name)

	format := viper.GetString("output")

	for _, s := range stages {
		num := s.Number()
		name := s.Name()

		var currentStageState *state.StageState
		if ss, ok := currentState.Stages[num]; ok {
			currentStageState = ss
		}

		plan, err := s.Plan(cmd.Context(), kc, profile, currentStageState)
		if err != nil {
			return fmt.Errorf("planning stage %d (%s): %w", num, name, err)
		}

		if format == "json" {
			data, err := json.MarshalIndent(plan, "", "  ")
			if err != nil {
				return fmt.Errorf("marshaling plan for stage %d: %w", num, err)
			}
			fmt.Println(string(data))
			continue
		}

		// Text output.
		printer.StageHeader(num, name)
		printer.Infof("  Action: %s", plan.Action)

		for _, comp := range plan.Components {
			current := comp.Current
			if current == "" {
				current = "(new)"
			}
			printer.Infof("    %-25s %s -> %s  [%s]", comp.Name, current, comp.Version, comp.Action)
		}

		for _, patch := range plan.Patches {
			printer.Infof("    Patch: %-25s %s", patch.Name, patch.Description)
		}
	}

	return nil
}
