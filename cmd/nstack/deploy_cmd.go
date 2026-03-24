package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/todd-chamberlain/nstack/pkg/engine"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/state"
)

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy NStack components",
	Long:  "Deploy GPU infrastructure, Slurm, and MLOps components to the target cluster.",
	RunE:  runDeploy,
}

func init() {
	deployCmd.Flags().Int("from", 0, "start from this stage number")
	deployCmd.Flags().Int("only", 0, "run only this stage number")
	deployCmd.Flags().String("stages", "", "comma-separated list of stage numbers (e.g., \"4,6\")")
	deployCmd.Flags().Bool("force", false, "re-apply stages even if already deployed")
	deployCmd.Flags().StringSlice("set", nil, "override values (key=value, can be repeated)")
}

func runDeploy(cmd *cobra.Command, args []string) error {
	if err := requireSite(); err != nil {
		return err
	}

	site, profile, err := loadSiteAndProfile()
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
	pipeline := engine.NewPipeline(registry, store, printer)

	from, _ := cmd.Flags().GetInt("from")
	only, _ := cmd.Flags().GetInt("only")
	stagesFlag, _ := cmd.Flags().GetString("stages")
	force, _ := cmd.Flags().GetBool("force")
	setValues, _ := cmd.Flags().GetStringSlice("set")

	resolveOpts, err := parseResolveOpts(from, only, stagesFlag)
	if err != nil {
		return err
	}

	// Handle --set flags by merging into site overrides.
	// Keys with a component prefix (e.g. "gpu-operator.driver.enabled=false") are
	// routed to that component's override map. Keys without a recognized component
	// prefix are applied to ALL components as global overrides.
	if len(setValues) > 0 {
		parsed, err := helm.ParseSetValues(setValues)
		if err != nil {
			return fmt.Errorf("parsing --set values: %w", err)
		}
		if site.Overrides == nil {
			site.Overrides = make(map[string]map[string]interface{})
		}
		for key, val := range parsed {
			subMap, isMap := val.(map[string]interface{})
			if !isMap {
				return fmt.Errorf("--set key %q must be prefixed with a component name (e.g., gpu-operator.%s)", key, key)
			}
			existing := site.Overrides[key]
			if existing == nil {
				existing = make(map[string]interface{})
			}
			for k, v := range subMap {
				existing[k] = v
			}
			site.Overrides[key] = existing
		}
	}

	opts := engine.RunOpts{
		ResolveOpts: resolveOpts,
		Force:       force,
		DryRun:      false,
		KubeClient:  kc,
		HelmClient:  hc,
		Site:        site,
		Profile:     profile,
	}

	start := time.Now()
	printer.Infof("Deploying NStack to site %q (profile: %s)...", site.Name, profile.Name)

	if err := pipeline.Run(cmd.Context(), opts); err != nil {
		return err
	}

	elapsed := time.Since(start)
	printer.DeployComplete(elapsed, nil)
	return nil
}
