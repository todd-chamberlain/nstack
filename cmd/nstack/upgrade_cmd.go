package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/todd-chamberlain/nstack/pkg/engine"
	"github.com/todd-chamberlain/nstack/pkg/state"
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade deployed components",
	Long:  "Re-apply stages with Force=true to upgrade components to their latest configured versions.",
	RunE:  runUpgrade,
}

func init() {
	upgradeCmd.Flags().Int("stage", 0, "upgrade only this stage number")
	upgradeCmd.Flags().String("version", "", "target version for the upgrade (informational)")
}

func runUpgrade(cmd *cobra.Command, args []string) error {
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
	store := state.NewStore(kc.Clientset(), site.Name)
	pipeline := engine.NewPipeline(registry, store, printer)

	stageNum, _ := cmd.Flags().GetInt("stage")
	targetVersion, _ := cmd.Flags().GetString("version")

	resolveOpts := engine.ResolveOpts{}
	if stageNum > 0 {
		resolveOpts.Only = stageNum
	}

	opts := engine.RunOpts{
		ResolveOpts: resolveOpts,
		Force:       true, // Upgrade always forces re-apply.
		DryRun:      false,
		KubeClient:  kc,
		HelmClient:  hc,
		Site:        site,
		Profile:     profile,
	}

	start := time.Now()
	msg := fmt.Sprintf("Upgrading NStack on site %q", site.Name)
	if targetVersion != "" {
		msg += fmt.Sprintf(" to version %s", targetVersion)
	}
	if stageNum > 0 {
		msg += fmt.Sprintf(" (stage %d only)", stageNum)
	}
	printer.Infof("%s...", msg)

	if err := pipeline.Run(cmd.Context(), opts); err != nil {
		return err
	}

	elapsed := time.Since(start)
	printer.DeployComplete(elapsed, nil)
	return nil
}
