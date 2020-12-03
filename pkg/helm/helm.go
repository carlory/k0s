package helm

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/k0sproject/k0s/internal/util"
	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/v1beta1"
	"github.com/k0sproject/k0s/pkg/constant"

	"gopkg.in/yaml.v2"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/downloader"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/repo"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// Commands run different helm command in the same way as CLI tool
type Commands struct {
	repoFile     string
	helmCacheDir string
	kubeConfig   string
}

var getters = getter.Providers{
	getter.Provider{
		Schemes: []string{"http", "https"},
		New:     getter.NewHTTPGetter,
	},
}

// NewCommands builds new Commands instance with default values
func NewCommands(k0sVars constant.CfgVars) *Commands {
	return &Commands{
		repoFile:     k0sVars.HelmRepositoryConfig,
		helmCacheDir: k0sVars.HelmRepositoryCache,
		kubeConfig:   k0sVars.AdminKubeConfigPath,
	}
}

func (hc *Commands) getActionCfg(namespace string) (*action.Configuration, error) {
	insecure := false
	impersonateGroup := []string{}
	cfg := &genericclioptions.ConfigFlags{
		Insecure:         &insecure,
		Timeout:          stringptr("0"),
		KubeConfig:       stringptr(hc.kubeConfig),
		CacheDir:         stringptr(hc.helmCacheDir),
		Namespace:        stringptr(namespace),
		ImpersonateGroup: &impersonateGroup,
	}
	actionConfig := &action.Configuration{}
	if err := actionConfig.Init(cfg, namespace, "secret", func(format string, v ...interface{}) {}); err != nil {
		return nil, err
	}
	return actionConfig, nil
}

func (hc *Commands) AddRepository(repoCfg k0sv1beta1.Repository) error {
	err := util.InitDirectory(filepath.Dir(hc.repoFile), constant.DataDirMode)
	if err != nil && !os.IsExist(err) {
		return fmt.Errorf("can't add repository to %s: %v", hc.repoFile, err)
	}

	b, err := ioutil.ReadFile(hc.repoFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("can't add repository to %s: %v", hc.repoFile, err)
	}

	var f repo.File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("can't add repository to %s: %v", hc.repoFile, err)
	}

	c := repo.Entry{
		Name:                  repoCfg.Name,
		URL:                   repoCfg.URL,
		Username:              repoCfg.Username,
		Password:              repoCfg.Password,
		CertFile:              repoCfg.CertFile,
		KeyFile:               repoCfg.KeyFile,
		CAFile:                repoCfg.CAFile,
		InsecureSkipTLSverify: true,
	}

	r, err := repo.NewChartRepository(&c, getters)
	r.CachePath = hc.helmCacheDir

	if err != nil {
		return fmt.Errorf("can't add repository to %s: %v", hc.repoFile, err)
	}

	if _, err := r.DownloadIndexFile(); err != nil {
		return fmt.Errorf("can't add repository: %q is not a valid chart repository or cannot be reached: %v", "repo", err)
	}
	f.Update(&c)
	if err := f.WriteFile(hc.repoFile, 0644); err != nil {
		return fmt.Errorf("can't add repository to %s: %v", hc.repoFile, err)
	}

	return nil
}

func (hc *Commands) downloadDependencies(chart *chart.Chart, chartPath string) error {
	if chart.Metadata.Dependencies == nil {
		return nil
	}
	if err := action.CheckDependencies(chart, chart.Metadata.Dependencies); err != nil {
		man := &downloader.Manager{
			Out:              os.Stdout,
			ChartPath:        chartPath,
			SkipUpdate:       false,
			Getters:          getters,
			RepositoryConfig: hc.repoFile,
			RepositoryCache:  hc.helmCacheDir,
			Debug:            false,
		}
		if err := man.Update(); err != nil {
			return err
		}
	}
	return nil
}

func (hc *Commands) locateChart(name string, version string) (string, error) {
	name = strings.TrimSpace(name)

	if _, err := os.Stat(name); err == nil {
		abs, err := filepath.Abs(name)
		if err != nil {
			return abs, fmt.Errorf("can't locate chart `%s-%s`: %v", name, version, err)
		}
		return abs, nil
	}
	if filepath.IsAbs(name) || strings.HasPrefix(name, ".") {
		return name, fmt.Errorf("can't locate chart: path not found: %s", name)
	}

	dl := downloader.ChartDownloader{
		Out:     os.Stdout,
		Getters: getters,
		Options: []getter.Option{
			//getter.WithBasicAuth(c.Username, c.Password),
			//getter.WithTLSClientConfig(c.CertFile, c.KeyFile, c.CaFile),
			//getter.WithInsecureSkipVerifyTLS(c.InsecureSkipTLSverify),
		},
		RepositoryConfig: hc.repoFile,
		RepositoryCache:  hc.helmCacheDir,
	}
	//if c.Verify {
	//	dl.Verify = downloader.VerifyAlways
	//}
	//if c.RepoURL != "" {
	//	chartURL, err := repo.FindChartInAuthAndTLSRepoURL(c.RepoURL, c.Username, c.Password, name, version,
	//		c.CertFile, c.KeyFile, c.CaFile, c.InsecureSkipTLSverify, getter.All(settings))
	//	if err != nil {
	//		return "", err
	//	}
	//	name = chartURL
	//}

	if err := util.InitDirectory(hc.helmCacheDir, constant.DataDirMode); err != nil {
		return "", fmt.Errorf("can't locate chart `%s-%s`: %v", name, version, err)
	}

	filename, _, err := dl.DownloadTo(name, version, hc.helmCacheDir)
	if err == nil {
		lname, err := filepath.Abs(filename)
		if err != nil {
			return filename, fmt.Errorf("can't locate chart `%s-%s`: %v", name, version, err)
		}
		return lname, nil
	} else if true {
		return filename, fmt.Errorf("can't locate chart `%s-%s`: %v", name, version, err)
	}

	atVersion := ""
	if version != "" {
		atVersion = fmt.Sprintf(" at version %q", version)
	}
	return filename, fmt.Errorf("failed to download %q%s (hint: running `helm repo update` may help)", name, atVersion)
}

func (hc *Commands) isInstallable(chart *chart.Chart) bool {
	if chart.Metadata.Type != "" && chart.Metadata.Type != "application" {
		return false
	}
	return true
}

func (hc *Commands) InstallChart(chartName string, version string, namespace string, values map[string]interface{}) (*release.Release, error) {
	cfg, err := hc.getActionCfg(namespace)
	if err != nil {
		return nil, fmt.Errorf("can't create action configuration: %v", err)
	}
	install := action.NewInstall(cfg)
	install.CreateNamespace = true
	chartDir, err := hc.locateChart(chartName, version)
	if err != nil {
		return nil, err
	}
	install.Namespace = namespace
	install.GenerateName = true
	name, _, err := install.NameAndChart([]string{chartName})
	install.ReleaseName = name

	if err != nil {
		return nil, err
	}
	chart, err := loader.Load(chartDir)
	if err != nil {
		return nil, fmt.Errorf("can't load chart `%s`: %v", chartDir, err)
	}
	if !hc.isInstallable(chart) {
		return nil, fmt.Errorf("chart with type `%s` is not installable", chart.Metadata.Type)
	}

	if err := hc.downloadDependencies(chart, chartDir); err != nil {
		return nil, err
	}

	chart, err = loader.Load(chartDir)
	if err != nil {
		return nil, fmt.Errorf("can't reload chart `%s`: %v", chartDir, err)
	}

	release, err := install.Run(chart, values)
	if err != nil {
		return nil, fmt.Errorf("can't install chart `%s`: %v", chart.Name(), err)
	}
	return release, nil
}

func (hc *Commands) UpgradeChart(chartName string, version string, releaseName string, namespace string, values map[string]interface{}) (*release.Release, error) {
	cfg, err := hc.getActionCfg(namespace)
	if err != nil {
		return nil, fmt.Errorf("can't create action configuration: %v", err)
	}
	upgrade := action.NewUpgrade(cfg)
	upgrade.Namespace = namespace

	chartDir, err := hc.locateChart(chartName, version)
	if err != nil {
		return nil, err
	}
	chart, err := loader.Load(chartDir)
	if err != nil {
		return nil, fmt.Errorf("can't load chart `%s`: %v", chartDir, err)
	}
	if !hc.isInstallable(chart) {
		return nil, fmt.Errorf("chart with type `%s` is not installable", chart.Metadata.Type)
	}

	if err := hc.downloadDependencies(chart, chartDir); err != nil {
		return nil, err
	}

	chart, err = loader.Load(chartDir)
	if err != nil {
		return nil, fmt.Errorf("can't reload chart `%s`: %v", chartDir, err)
	}

	release, err := upgrade.Run(releaseName, chart, values)

	if err != nil {
		return nil, fmt.Errorf("can't upgrade chart `%s`: %v", chart.Metadata.Name, err)
	}

	return release, nil
}

func stringptr(s string) *string {
	return &s
}

func (hc *Commands) ListReleases(namespace string) ([]*release.Release, error) {
	cfg, err := hc.getActionCfg(namespace)
	if err != nil {
		return nil, fmt.Errorf("can't create action configuration: %v", err)
	}
	action := action.NewList(cfg)
	return action.Run()
}

func (hc *Commands) UninstallRelease(releaseName string, namespace string) error {
	cfg, err := hc.getActionCfg(namespace)
	if err != nil {
		return fmt.Errorf("can't create action configuration: %v", err)
	}
	action := action.NewUninstall(cfg)
	if _, err := action.Run(releaseName); err != nil {
		return fmt.Errorf("can't uninstall release `%s`: %v", releaseName, err)
	}
	return nil
}
