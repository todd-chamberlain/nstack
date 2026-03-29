package helm

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/repo"
)

// Client wraps the Helm v3 SDK for programmatic chart operations.
type Client struct {
	settings   *cli.EnvSettings
	kubeconfig string
}

// installOpts holds optional configuration for UpgradeOrInstall.
type installOpts struct {
	version         string
	timeout         time.Duration
	createNamespace bool
	wait            bool
}

// Option configures an UpgradeOrInstall call.
type Option func(*installOpts)

// WithVersion pins the chart to a specific version.
func WithVersion(v string) Option {
	return func(o *installOpts) {
		o.version = v
	}
}

// WithTimeout sets the operation timeout.
func WithTimeout(d time.Duration) Option {
	return func(o *installOpts) {
		o.timeout = d
	}
}

// WithCreateNamespace enables automatic namespace creation.
func WithCreateNamespace() Option {
	return func(o *installOpts) {
		o.createNamespace = true
	}
}

// WithWait waits until all resources are ready.
func WithWait() Option {
	return func(o *installOpts) {
		o.wait = true
	}
}

// NewClient creates a new Helm client configured for the given kubeconfig.
func NewClient(kubeconfig string) *Client {
	settings := cli.New()
	if kubeconfig != "" {
		settings.KubeConfig = kubeconfig
	}
	return &Client{
		settings:   settings,
		kubeconfig: kubeconfig,
	}
}

// actionConfig builds an action.Configuration bound to the client's kubeconfig and the given namespace.
// A fresh cli.EnvSettings is created per call to avoid mutating the shared c.settings field,
// which would cause a race condition when actionConfig is called concurrently.
func (c *Client) actionConfig(namespace string) (*action.Configuration, error) {
	settings := cli.New()
	if c.kubeconfig != "" {
		settings.KubeConfig = c.kubeconfig
	}
	if namespace != "" {
		settings.SetNamespace(namespace)
	}
	cfg := new(action.Configuration)
	err := cfg.Init(settings.RESTClientGetter(), namespace, os.Getenv("HELM_DRIVER"), log.Printf)
	if err != nil {
		return nil, fmt.Errorf("initializing Helm configuration: %w", err)
	}
	return cfg, nil
}

// AddRepo adds (or updates) a named Helm chart repository and downloads its index.
func (c *Client) AddRepo(name, url string) error {
	repoFile := c.settings.RepositoryConfig

	// Ensure the repository config directory exists.
	if dir := filepath.Dir(repoFile); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating repo config dir: %w", err)
		}
	}

	f, err := repo.LoadFile(repoFile)
	if err != nil || f == nil {
		f = repo.NewFile()
	}

	entry := &repo.Entry{
		Name: name,
		URL:  url,
	}

	if f.Has(name) {
		f.Update(entry)
	} else {
		f.Add(entry)
	}

	if err := f.WriteFile(repoFile, 0644); err != nil {
		return fmt.Errorf("writing repository file: %w", err)
	}

	// Download the index file so charts can be resolved.
	chartRepo, err := repo.NewChartRepository(entry, getter.All(c.settings))
	if err != nil {
		return fmt.Errorf("creating chart repository: %w", err)
	}
	chartRepo.CachePath = c.settings.RepositoryCache
	if _, err := chartRepo.DownloadIndexFile(); err != nil {
		return fmt.Errorf("downloading repo index for %s: %w", name, err)
	}

	return nil
}

// UpgradeOrInstall installs a chart if the release does not exist, or upgrades it
// if it does. This is the idempotent install-or-upgrade pattern used by nstack.
func (c *Client) UpgradeOrInstall(ctx context.Context, releaseName, chartRef, namespace string, values map[string]interface{}, opts ...Option) error {
	o := &installOpts{
		timeout: 5 * time.Minute,
	}
	for _, fn := range opts {
		fn(o)
	}

	cfg, err := c.actionConfig(namespace)
	if err != nil {
		return err
	}

	// Check if the release already exists and its state.
	histClient := action.NewHistory(cfg)
	histClient.Max = 1
	releases, histErr := histClient.Run(releaseName)
	releaseExists := histErr == nil && len(releases) > 0

	// Handle stuck releases (pending-install, pending-upgrade, pending-rollback).
	if releaseExists {
		latest := releases[len(releases)-1]
		if latest.Info != nil && latest.Info.Status.IsPending() {
			// Release is stuck. Attempt rollback before retrying.
			rollback := action.NewRollback(cfg)
			rollback.Force = true
			rollback.CleanupOnFail = true
			if rbErr := rollback.Run(releaseName); rbErr != nil {
				// Rollback failed — try uninstall to clean up completely.
				uninstall := action.NewUninstall(cfg)
				uninstall.KeepHistory = false
				_, _ = uninstall.Run(releaseName)
				releaseExists = false
			}
		}
	}

	if !releaseExists {
		return c.installRelease(ctx, cfg, releaseName, chartRef, namespace, values, o)
	}
	return c.upgradeRelease(ctx, cfg, releaseName, chartRef, namespace, values, o)
}

// installRelease performs a fresh Helm install.
func (c *Client) installRelease(ctx context.Context, cfg *action.Configuration, releaseName, chartRef, namespace string, values map[string]interface{}, o *installOpts) error {
	install := action.NewInstall(cfg)
	install.ReleaseName = releaseName
	install.Namespace = namespace
	install.CreateNamespace = o.createNamespace
	install.Wait = o.wait
	install.Timeout = o.timeout

	if o.version != "" {
		install.Version = o.version
	}

	chartPath, err := install.ChartPathOptions.LocateChart(chartRef, c.settings)
	if err != nil {
		return fmt.Errorf("locating chart %s: %w", chartRef, err)
	}

	ch, err := loader.Load(chartPath)
	if err != nil {
		return fmt.Errorf("loading chart %s: %w", chartPath, err)
	}

	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()

	_, err = install.RunWithContext(ctx, ch, values)
	if err != nil {
		return fmt.Errorf("installing release %s: %w", releaseName, err)
	}

	return nil
}

// upgradeRelease performs a Helm upgrade on an existing release.
func (c *Client) upgradeRelease(ctx context.Context, cfg *action.Configuration, releaseName, chartRef, namespace string, values map[string]interface{}, o *installOpts) error {
	upgrade := action.NewUpgrade(cfg)
	upgrade.Namespace = namespace
	upgrade.Wait = o.wait
	upgrade.Timeout = o.timeout

	if o.version != "" {
		upgrade.Version = o.version
	}

	chartPath, err := upgrade.ChartPathOptions.LocateChart(chartRef, c.settings)
	if err != nil {
		return fmt.Errorf("locating chart %s: %w", chartRef, err)
	}

	ch, err := loader.Load(chartPath)
	if err != nil {
		return fmt.Errorf("loading chart %s: %w", chartPath, err)
	}

	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()

	_, err = upgrade.RunWithContext(ctx, releaseName, ch, values)
	if err != nil {
		return fmt.Errorf("upgrading release %s: %w", releaseName, err)
	}

	return nil
}

// Uninstall removes a Helm release.
func (c *Client) Uninstall(releaseName, namespace string) error {
	cfg, err := c.actionConfig(namespace)
	if err != nil {
		return err
	}

	uninstall := action.NewUninstall(cfg)
	_, err = uninstall.Run(releaseName)
	if err != nil {
		return fmt.Errorf("uninstalling release %s: %w", releaseName, err)
	}
	return nil
}

// GetRelease retrieves information about a deployed release.
func (c *Client) GetRelease(releaseName, namespace string) (*release.Release, error) {
	cfg, err := c.actionConfig(namespace)
	if err != nil {
		return nil, err
	}

	get := action.NewGet(cfg)
	rel, err := get.Run(releaseName)
	if err != nil {
		return nil, fmt.Errorf("getting release %s: %w", releaseName, err)
	}
	return rel, nil
}

// IsInstalled checks whether a release exists and returns its chart version.
func (c *Client) IsInstalled(releaseName, namespace string) (installed bool, version string, err error) {
	cfg, err := c.actionConfig(namespace)
	if err != nil {
		return false, "", err
	}

	histClient := action.NewHistory(cfg)
	histClient.Max = 1
	releases, err := histClient.Run(releaseName)
	if err != nil {
		// "release: not found" is the expected error when not installed.
		return false, "", nil
	}
	if len(releases) == 0 {
		return false, "", nil
	}

	rel := releases[0]

	// A failed or pending-install release should not be considered "installed".
	// Uninstall it so the next deploy can do a clean install.
	if rel.Info != nil && (rel.Info.Status == release.StatusFailed ||
		rel.Info.Status == release.StatusPendingInstall ||
		rel.Info.Status == release.StatusPendingUpgrade) {
		uninstall := action.NewUninstall(cfg)
		_, _ = uninstall.Run(releaseName)
		return false, "", nil
	}

	if rel.Chart != nil && rel.Chart.Metadata != nil {
		version = rel.Chart.Metadata.Version
	}
	return true, version, nil
}
