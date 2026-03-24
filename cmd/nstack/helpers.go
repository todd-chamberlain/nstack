package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/viper"

	"github.com/todd-chamberlain/nstack/pkg/config"
	"github.com/todd-chamberlain/nstack/pkg/engine"
	"github.com/todd-chamberlain/nstack/pkg/helm"
	"github.com/todd-chamberlain/nstack/pkg/kube"
	"github.com/todd-chamberlain/nstack/pkg/output"
)

// loadSiteAndProfile loads the site configuration and its associated profile.
func loadSiteAndProfile() (*config.Site, *config.Profile, error) {
	cfgPath := viper.GetString("config")
	if cfgPath == "" {
		cfgPath = config.DefaultConfigPath()
	}
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		return nil, nil, fmt.Errorf("loading config: %w", err)
	}

	site, err := cfg.GetSite(viper.GetString("site"))
	if err != nil {
		return nil, nil, err
	}

	profile, err := config.LoadProfile(site.Profile)
	if err != nil {
		return nil, nil, err
	}

	return site, profile, nil
}

// requireSite checks that the --site flag was provided and returns an error if not.
func requireSite() error {
	if viper.GetString("site") == "" {
		return fmt.Errorf("--site is required; specify the target site name")
	}
	return nil
}

// createClients creates Kubernetes and Helm clients from the site configuration.
func createClients(site *config.Site) (*kube.Client, *helm.Client, error) {
	kc, err := kube.NewClient(site.Kubeconfig)
	if err != nil {
		return nil, nil, fmt.Errorf("creating kube client: %w", err)
	}
	hc := helm.NewClient(site.Kubeconfig, "default")
	return kc, hc, nil
}

// createPrinter creates an output printer from global flags.
func createPrinter() *output.Printer {
	return output.New(
		viper.GetString("output"),
		viper.GetBool("quiet"),
		viper.GetBool("verbose"),
	)
}

// parseResolveOpts converts the --from, --only, and --stages flags into ResolveOpts.
func parseResolveOpts(from, only int, stages string) (engine.ResolveOpts, error) {
	opts := engine.ResolveOpts{
		From: from,
		Only: only,
	}
	if stages != "" {
		parts := strings.Split(stages, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			n, err := strconv.Atoi(p)
			if err != nil {
				return opts, fmt.Errorf("invalid stage number %q in --stages: %w", p, err)
			}
			opts.Stages = append(opts.Stages, n)
		}
	}
	return opts, nil
}
