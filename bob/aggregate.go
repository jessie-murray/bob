package bob

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/benchkram/bob/bob/bobfile"
	"github.com/benchkram/bob/bob/bobfile/project"
	"github.com/benchkram/bob/bob/global"
	"github.com/benchkram/bob/bobtask"
	"github.com/benchkram/bob/pkg/auth"
	"github.com/benchkram/bob/pkg/boblog"
	"github.com/benchkram/bob/pkg/envutil"
	"github.com/benchkram/bob/pkg/file"
	"github.com/benchkram/bob/pkg/usererror"
	"github.com/benchkram/errz"
	"github.com/hashicorp/go-version"
	"github.com/logrusorgru/aurora"
)

var (
	ErrDuplicateProjectName = fmt.Errorf("duplicate project name")
)

func (b *B) PrintVersionCompatibility(bobfile *bobfile.Bobfile) {
	binVersion, _ := version.NewVersion(Version)

	for _, boblet := range bobfile.Bobfiles() {
		if boblet.Version != "" {
			bobletVersion, _ := version.NewVersion(boblet.Version)

			if binVersion.Core().Segments64()[0] != bobletVersion.Core().Segments64()[0] {
				fmt.Println(aurora.Red(fmt.Sprintf("Warning: major version mismatch: Your bobfile's major version (%s, '%s') is different from the CLI version (%s). This might lead to unexpected errors.", boblet.Version, boblet.Dir(), binVersion)).String())
				continue
			}

			if binVersion.LessThan(bobletVersion) {
				fmt.Println(aurora.Red(fmt.Sprintf("Warning: possible version incompatibility: Your bobfile's version (%s, '%s') is higher than the CLI version (%s). Some features might not work as expected.", boblet.Version, boblet.Dir(), binVersion)).String())
				continue
			}
		}
	}
}

// AggregateSparse reads Bobfile with the intent to gather task names.
// The returned bobfile is not ready to be executed with a playbook.
func (b *B) AggregateSparse(omitRunTasks ...bool) (aggregate *bobfile.Bobfile, err error) {
	defer errz.Recover(&err)

	addRunTasks := true
	if len(omitRunTasks) > 0 {
		if omitRunTasks[0] {
			addRunTasks = false
		}
	}

	// Passing "." instead of the absPath so the
	// tasks can be initialized with the relativve path.
	// The absolut path is only stored in `aggregate.Project`.
	aggregate, err = bobfile.BobfileReadPlain(".")
	errz.Fatal(err)

	if !file.Exists(global.BobFileName) {
		return nil, usererror.Wrap(ErrCouldNotFindTopLevelBobfile)
	}

	if aggregate == nil {
		return nil, usererror.Wrap(ErrCouldNotFindTopLevelBobfile)
	}

	bobs, err := readImports(aggregate, true)
	errz.Fatal(err)

	if aggregate.Project == "" {
		// TODO: maybe don't leak absolute path of environment

		wd, _ := os.Getwd()
		aggregate.Project = wd
	}

	// set project names for all bobfiles and build tasks
	aggregate, bobs = syncProjectName(aggregate, bobs)

	aggregate.SetBobfiles(bobs)

	// Merge tasks into one Bobfile
	aggregate, err = b.addBuildTasksToAggregate(aggregate, bobs, nil)
	errz.Fatal(err)

	if addRunTasks {
		aggregate = b.addRunTasksToAggregate(aggregate, bobs)
	}

	return aggregate, nil
}

// Aggregate determine and read Bobfiles recursively into memory
// and returns a single Bobfile containing all tasks & runs.
func (b *B) Aggregate() (aggregate *bobfile.Bobfile, err error) {
	defer errz.Recover(&err)

	// Passing "." instead of the absPath so the
	// tasks can be initialized with the relative path.
	// The absolute path is only stored in `aggregate.Project`.
	aggregate, err = bobfile.BobfileRead(".")
	errz.Fatal(err)

	if !file.Exists(global.BobFileName) {
		return nil, usererror.Wrap(ErrCouldNotFindTopLevelBobfile)
	}

	if aggregate == nil {
		return nil, usererror.Wrap(ErrCouldNotFindTopLevelBobfile)
	}

	// FIXME: Implement more generaly to work on all levels.
	decorations, err := collectDecorations(aggregate)
	errz.Fatal(err)

	bobs, err := readImports(aggregate, false)
	errz.Fatal(err)

	for _, boblet := range append(bobs, aggregate) {
		for key, task := range boblet.BTasks {
			task.SetEnv(envutil.Merge(boblet.Vars(), b.env))
			boblet.BTasks[key] = task
		}

		for key, task := range boblet.RTasks {
			task.SetEnv(envutil.Merge(boblet.Vars(), b.env))
			boblet.RTasks[key] = task
		}
	}

	if aggregate.Project == "" {
		// TODO: maybe don't leak absolute path of environment
		wd, _ := os.Getwd()
		aggregate.Project = wd
	}

	// set project names for all bobfiles and build tasks
	aggregate, bobs = syncProjectName(aggregate, bobs)

	aggregate.SetBobfiles(bobs)

	// Merge tasks into one Bobfile
	aggregate, err = b.addBuildTasksToAggregate(aggregate, bobs, decorations)
	errz.Fatal(err)

	// Merge runs into one Bobfile
	aggregate = b.addRunTasksToAggregate(aggregate, bobs)

	// Assure tasks are correctly initialised.
	for i, task := range aggregate.BTasks {
		task.WithLocalstore(b.local)
		task.WithBuildinfoStore(b.buildInfoStore)
		task.WithDockerRegistryClient(b.dockerRegistryClient)

		// a task must always-rebuild when caching is disabled
		if !b.enableCaching {
			task.SetRebuildStrategy(bobtask.RebuildAlways)
		}
		aggregate.BTasks[i] = task
	}

	// Aggregate all dependencies set at bobfile level
	addedDependencies := make(map[string]bool)
	var allDeps []string
	for _, bobfile := range bobs {
		for _, dep := range bobfile.Dependencies {
			if _, added := addedDependencies[dep]; !added {
				if strings.HasSuffix(dep, ".nix") {
					allDeps = append(allDeps, bobfile.Dir()+"/"+dep)
				} else {
					allDeps = append(allDeps, dep)
				}
				addedDependencies[dep] = true
			}
		}
	}
	aggregate.Dependencies = make([]string, 0)
	aggregate.Dependencies = append(aggregate.Dependencies, allDeps...)

	// Initialize remote store in case of a valid remote url / project name
	if aggregate.Project != "" {
		projectName, err := project.Parse(aggregate.Project)
		if err != nil {
			return nil, err
		}

		switch projectName.Type() {
		case project.Local:
			// Do nothing
		case project.Remote:
			// Initialize remote store
			url, err := projectName.Remote()
			if err != nil {
				return nil, err
			}

			authCtx, err := b.CurrentAuthContext()
			if err != nil {
				if errors.Is(err, auth.ErrNotFound) {
					fmt.Printf("Will not sync to %s because of missing auth context\n", projectName)
				} else {
					return nil, err
				}
			} else {
				boblog.Log.V(1).Info(fmt.Sprintf("Using remote store: %s", url.String()))
				aggregate.SetRemotestore(bobfile.NewRemotestore(url, b.allowInsecure, authCtx.Token))
			}
		}
	} else {
		aggregate.Project = aggregate.Dir()
	}

	err = aggregate.Verify()
	errz.Fatal(err)

	err = aggregate.BTasks.IgnoreChildTargets()
	errz.Fatal(err)

	err = aggregate.BTasks.FilterInputs()
	errz.Fatal(err)

	return aggregate, nil
}

func addTaskPrefix(prefix, taskname string) string {
	taskname = filepath.Join(prefix, taskname)
	taskname = strings.TrimPrefix(taskname, string(bobtask.TaskPathSeparator))
	return taskname
}

// collectDecorations returns a mapping of taskname to child tasks
// for valid decorations.
// An err is returned if attempting to collect an invalid decoration
func collectDecorations(ag *bobfile.Bobfile) (_ map[string][]string, err error) {
	defer errz.Recover(&err)

	decorations := make(map[string][]string)
	for k, task := range ag.BTasks {
		if !task.IsDecoration() {
			continue
		}
		if !task.IsValidDecoration() {
			errz.Fatal(usererror.Wrap(fmt.Errorf("task `%s` modifies an imported task. It can only contain a `dependsOn` property", k)))
		}
		decorations[k] = task.DependsOn
	}
	return decorations, nil
}
