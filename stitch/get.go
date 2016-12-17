package stitch

import (
	"errors"
	"fmt"
	"golang.org/x/tools/go/vcs"
	"os"
	"path/filepath"

	"github.com/NetSys/quilt/util"

	log "github.com/Sirupsen/logrus"
	homedir "github.com/mitchellh/go-homedir"

	"github.com/robertkrimen/otto"
	"github.com/spf13/afero"
)

// QuiltPathKey is the environment variable key we use to lookup the Quilt path.
const QuiltPathKey = "QUILT_PATH"

// GetQuiltPath returns the user-defined QUILT_PATH, or the default absolute QUILT_PATH,
// which is ~/.quilt if the user did not specify a QUILT_PATH.
func GetQuiltPath() string {
	if quiltPath := os.Getenv(QuiltPathKey); quiltPath != "" {
		return quiltPath
	}

	dir, err := homedir.Dir()
	if err != nil {
		log.WithError(err).Fatalf("Failed to get user's homedir for "+
			"%s generation", QuiltPathKey)
	}

	return filepath.Join(dir, ".quilt")
}

// ImportGetter provides functions for working with imports.
type ImportGetter struct {
	Path         string
	AutoDownload bool

	repoFactory func(repo string) (repo, error)

	// Used to detect import cycles.
	importPath []string
}

func (getter ImportGetter) withAutoDownload(autoDownload bool) ImportGetter {
	return ImportGetter{
		Path:         getter.Path,
		AutoDownload: autoDownload,
		repoFactory:  getter.repoFactory,
	}
}

type repo interface {
	// Pull the latest changes in the repo to `dir`.
	update(dir string) error

	// Checkout the repo to `dir`.
	create(dir string) error

	// Get the root of the repo.
	root() string
}

// `goRepo` is a wrapper around `vcs.RepoRoot` that satisfies the `repo` interface.
type goRepo struct {
	repo *vcs.RepoRoot
}

func (gr goRepo) update(dir string) error {
	return gr.repo.VCS.Download(dir)
}

func (gr goRepo) create(dir string) error {
	return gr.repo.VCS.Create(dir, gr.repo.Repo)
}

func (gr goRepo) root() string {
	return gr.repo.Root
}

func goRepoFactory(repoName string) (repo, error) {
	vcsRepo, err := vcs.RepoRootForImportPath(repoName, true)
	return goRepo{vcsRepo}, err
}

// DefaultImportGetter uses the default QUILT_PATH, and doesn't automatically
// download imports.
var DefaultImportGetter = ImportGetter{
	Path:        GetQuiltPath(),
	repoFactory: goRepoFactory,
}

// Get takes in an import path `repoName`, and attempts to download the
// repository associated with that repoName.
func (getter ImportGetter) Get(repoName string) error {
	path, err := getter.downloadSpec(repoName)
	if err != nil {
		return err
	}
	return getter.resolveSpecImports(path)
}

func (getter ImportGetter) downloadSpec(repoName string) (string, error) {
	repo, err := getter.repoFactory(repoName)
	if err != nil {
		return "", err
	}

	path := filepath.Join(getter.Path, repo.root())
	if _, statErr := util.AppFs.Stat(path); os.IsNotExist(statErr) {
		log.Info(fmt.Sprintf("Cloning %s into %s", repo.root(), path))
		err = repo.create(path)
	} else {
		log.Info(fmt.Sprintf("Updating %s in %s", repo.root(), path))
		err = repo.update(path)
	}
	return path, err
}

func (getter ImportGetter) resolveSpecImports(folder string) error {
	return afero.Walk(util.AppFs, folder, getter.checkSpec)
}

func (getter ImportGetter) checkSpec(file string, _ os.FileInfo, _ error) error {
	if filepath.Ext(file) != ".js" {
		return nil
	}
	_, err := FromFile(file, getter.withAutoDownload(true))
	return err
}

func (getter ImportGetter) specContents(name string) (string, string, error) {
	modulePath := filepath.Join(getter.Path, name+".js")
	if _, err := util.AppFs.Stat(modulePath); os.IsNotExist(err) &&
		getter.AutoDownload {
		getter.Get(name)
	}

	spec, err := util.ReadFile(modulePath)
	if err != nil {
		return "", "", fmt.Errorf("unable to open import %s (path=%s)",
			name, modulePath)
	}
	return modulePath, spec, nil
}

func (getter *ImportGetter) requireImpl(call otto.FunctionCall) (otto.Value, error) {
	if len(call.ArgumentList) != 1 {
		return otto.Value{}, errors.New(
			"require requires the import as an argument")
	}
	name, err := call.Argument(0).ToString()
	if err != nil {
		return otto.Value{}, err
	}

	// An import cycle exists if a spec imports one of its parents.
	// We detect this by keeping track of the path to get to the current import.
	// This slice is maintained by adding imports to the path when they're
	// initially imported, and removing them when all their children have finished
	// importing.
	if contains(getter.importPath, name) {
		return otto.Value{},
			fmt.Errorf("import cycle: %v", append(getter.importPath, name))
	}

	getter.importPath = append(getter.importPath, name)
	defer func() {
		getter.importPath = getter.importPath[:len(getter.importPath)-1]
	}()

	modulePath, impStr, err := getter.specContents(name)
	if err != nil {
		return otto.Value{}, err
	}

	return runSpec(call.Otto, modulePath, impStr)
}
