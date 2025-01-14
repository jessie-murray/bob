package bobtask

import (
	"strings"

	"github.com/benchkram/bob/pkg/nix"
	"github.com/logrusorgru/aurora"

	"github.com/benchkram/bob/bobtask/hash"
	"github.com/benchkram/bob/bobtask/target"
	"github.com/benchkram/bob/pkg/buildinfostore"
	"github.com/benchkram/bob/pkg/dockermobyutil"
	"github.com/benchkram/bob/pkg/store"
)

type RebuildType string

const (
	RebuildAlways   RebuildType = "always"
	RebuildOnChange RebuildType = "on-change"
)

// Hint: When adding a new *Dirty field assure to update IsValidDecoration().
type Task struct {
	// Inputs are directorys or files
	// the task monitors for a rebuild.

	// InputDirty is the representation read from a bobfile.
	InputDirty string `yaml:"input,omitempty"`
	// InputAdditionalIgnores is a list of ignores
	// usually the child targets.
	InputAdditionalIgnores []string `yaml:"input_additional_ignores,omitempty"`
	// inputs is filtered by ignored & sanitized
	inputs []string

	CmdDirty string `yaml:"cmd,omitempty"`
	// The cmds passed to os.Exec
	cmds []string

	// DependsOn are task which must succeed before this task
	// can run.
	DependsOn []string `yaml:"dependsOn,omitempty"`

	// Target defines the output of a task.
	TargetDirty TargetEntry `yaml:"target,omitempty"`
	target      *target.T

	// defines the rebuild strategy
	RebuildDirty string `yaml:"rebuild,omitempty"`
	rebuild      RebuildType

	// name is the name of the task
	// TODO: Make this public to allow yaml.Marshal to add this to the task hash?!?
	name string

	// project this tasks belongs to
	project string

	// dir is the working directory for this task
	dir string

	// env holds key=value pairs passed to the environment
	// when the task is executed.
	env []string

	// hashIn stores the `In` has for reuse
	hashIn *hash.In

	// local store for artifacts
	local store.Store

	// remote store for artifacts
	remote store.Store

	// buildInfoStore stores buildinfos.
	buildInfoStore buildinfostore.Store

	// color is used to color the task's name on the terminal
	color aurora.Color

	// dockerRegistryClient utility functions to handle requests with local docker registry
	dockerRegistryClient dockermobyutil.RegistryClient

	// skippedInputs is a lists of skipped input files
	skippedInputs []string

	// DependenciesDirty read from the bobfile
	DependenciesDirty []string `yaml:"dependencies,omitempty"`

	// dependencies contain the actual dependencies merged
	// with the global dependencies defined in the Bobfile
	// in the order which they need to be added to PATH
	dependencies []nix.Dependency

	// storePaths contain /nix/store/* paths
	// in the order which they need to be added to PATH
	storePaths []string
}

type TargetEntry interface{}

func Make(opts ...TaskOption) Task {
	t := Task{
		inputs:                 []string{},
		InputAdditionalIgnores: []string{},
		cmds:                   []string{},
		DependsOn:              []string{},
		env:                    []string{},
		rebuild:                RebuildOnChange,
		dockerRegistryClient:   dockermobyutil.NewRegistryClient(),
		dependencies:           []nix.Dependency{},
	}

	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(&t)
	}

	return t
}

// AddToSkippedInputs add filenames with permission issues to the task's
// skippedInput list. returns without appending if file
// already exists, thus maintain uniqueness
func (t *Task) addToSkippedInputs(f string) {
	for _, si := range t.skippedInputs {
		if si == f {
			return
		}
	}
	t.skippedInputs = append(t.skippedInputs, f)
}

// LogSkippedInput skipped input files from the task.
// prints nothing if there is not skipped input.
func (t *Task) LogSkippedInput() []string {
	return t.skippedInputs
}

// IsDecoration check if the task is a decorated task
func (t *Task) IsDecoration() bool {
	return strings.ContainsRune(t.name, TaskPathSeparator)
}

// IsValidDecoration checks if the task is a valid decoration.
// tasks containing a `dependsOn` node only are considered as
// valid decoration.
//
// Make sure to update IsValidDecoration() very time a new
// *Dirty field is added to the task.
func (t *Task) IsValidDecoration() bool {
	if t.InputDirty != "" {
		return false
	}
	if len(t.InputAdditionalIgnores) > 0 {
		return false
	}
	if t.CmdDirty != "" {
		return false
	}
	if t.RebuildDirty != "" {
		return false
	}
	if len(t.DependenciesDirty) > 0 {
		return false
	}
	if t.TargetDirty != nil {
		return false
	}
	return true
}
