package bobpackages

import (
	"context"
	"errors"
	"strings"

	"github.com/Benchkram/bob/pkg/aqua"
	"github.com/Benchkram/bob/pkg/boblog"
	"github.com/Benchkram/bob/pkg/packagemanager"
	"github.com/Benchkram/bob/pkg/usererror"
	"github.com/Benchkram/errz"
	"github.com/logrusorgru/aurora"
)

var (
	ErrInvalidPackageDefinition = errors.New("Invalid package definition")
)

type Packages struct {
	ListDirty []string `yaml:"packages"`

	// Packages managed in a map to eliminate duplicates
	Packages map[string]packagemanager.Package `yaml:"-"`
	manager  packagemanager.PackageManager
}

// Sanitize dirty inputs to package definitions
// Returns usererror on bad package definition
func (p *Packages) Sanitize() error {

	for _, pkg := range p.ListDirty {
		splits := strings.Split(pkg, "@")
		if len(splits) != 2 {
			return usererror.Wrap(ErrInvalidPackageDefinition)
		}
		name, version := splits[0], splits[1]

		p.Packages[pkg] = packagemanager.Package{
			Name:    name,
			Version: version,
		}
	}

	// get list of packages to add them to packagemanager
	pkgs := make([]packagemanager.Package, 0, len(p.Packages))
	for _, pkg := range p.Packages {
		pkgs = append(pkgs, pkg)
	}

	p.manager = aqua.New()
	p.manager.Add(pkgs...)

	return nil
}

// Install passes down install call to internal packagemanager
func (p *Packages) Install(ctx context.Context) (err error) {
	defer errz.Recover(&err)
	// If no packages defined just return
	if len(p.Packages) == 0 {
		return nil
	}

	boblog.Log.Info(aurora.Green("Installing packages...").String())

	err = p.manager.Install(ctx)
	errz.Fatal(err)

	boblog.Log.Info(aurora.Green("All packages successfully installed").String())

	return nil
}

// Prune passes down prune call to internal packagemanager
func (p *Packages) Prune(ctx context.Context) error {
	return p.manager.Prune(ctx)
}

// SetEnvirionment passes down call to internal packagemanager
func (p *Packages) SetEnvirionment() error {
	return p.manager.SetEnvirionment()
}

// Search returns a set of possible packages to add
func (p *Packages) Search(ctx context.Context) ([]string, error) {
	return p.manager.Search(ctx)
}
