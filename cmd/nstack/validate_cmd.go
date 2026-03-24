package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Run pre-flight checks",
	Long:  "Validate that the cluster is ready for deployment by running pre-flight checks on each stage.",
	RunE:  runValidate,
}

func runValidate(cmd *cobra.Command, args []string) error {
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

	printer.Infof("Running pre-flight validation for site %q...", site.Name)

	stages := registry.All()
	allPassed := true

	for _, s := range stages {
		printer.Infof("  Stage %d (%s)...", s.Number(), s.Name())
		if err := s.Validate(cmd.Context(), kc, profile); err != nil {
			printer.Errorf("  Stage %d (%s): FAILED — %v", s.Number(), s.Name(), err)
			allPassed = false
		} else {
			printer.Infof("  Stage %d (%s): OK", s.Number(), s.Name())
		}
	}

	if !allPassed {
		return fmt.Errorf("validation failed; fix the issues above before deploying")
	}

	printer.Infof("\nAll pre-flight checks passed.")
	return nil
}
