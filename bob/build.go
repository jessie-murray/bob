package bob

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Benchkram/bob/bob/playbook"
	"github.com/Benchkram/bob/bobtask"
	"github.com/Benchkram/errz"
)

var (
	ErrNoRebuildRequired = errors.New("no rebuild required")
)

func (b *B) Build(ctx context.Context, taskname string) (err error) {
	defer errz.Recover(&err)

	aggregate, err := b.Aggregate()
	errz.Fatal(err)
	println(aggregate.Tasks.String())
	println(aggregate.Runs.String())

	playbook, err := aggregate.Playbook(taskname)
	errz.Fatal(err)
	println(playbook.String())

	return b.BuildTask(ctx, taskname, playbook)
}

// BuildTask and its childs in a playbook.
func (b *B) BuildTask(ctx context.Context, taskname string, playbook *playbook.Playbook) (err error) {
	done := make(chan error)

	go func() {
		// TODO: Run a worker pool so that multiple tasks can run in parallel.
		// https://stackoverflow.com/questions/25306073/always-have-x-number-of-goroutines-running-at-any-time

		c := playbook.TaskChannel()
		for task := range c {
			err := b.buildSingleTask(ctx, taskname, task, playbook)
			if errors.Is(err, context.Canceled) {
				done <- err
				break
			}
			killOnError(err)
		}

		close(done)
	}()

	playbook.Play()
	err = <-done
	fmt.Printf("\n\nDone running playbook in %s\n", playbook.ExecutionTime())
	return err
}

// Run a single task (of potentially a parent) in a playbook.
func (b *B) buildSingleTask(ctx context.Context, taskname string, task bobtask.Task, playbook *playbook.Playbook) (err error) {
	// TODO: Run a worker pool so that multiple tasks can run in parallel.
	// https://stackoverflow.com/questions/25306073/always-have-x-number-of-goroutines-running-at-any-time

	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-done:
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				err := playbook.TaskCanceled(task.Name())
				killOnError(err)
				fmt.Printf("Task %q was canceled\n", task.Name())
			}
		}
	}()

	println()
	println()
	fmt.Printf("Beginning to run task: %q\n", task.Name())

	rebuildRequired, err := playbook.TaskNeedsRebuild(task.Name())
	killOnError(err)

	if !rebuildRequired {
		err = playbook.TaskNoRebuildRequired(task.Name())
		killOnError(err)
		fmt.Printf("Task %q doesn't need to be rebuilt\n", task.Name())
		return nil
	}

	err = task.Clean()
	killOnError(err)

	err = task.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return err
	}
	killOnError(err)

	err = task.VerifyAfter()
	killOnError(err)

	// 3: Check whether output is created correctly.
	//    Might be a build error or a configuration
	//    error when the output does not exist.
	// -> task.Target
	succeeded, failedTargets := task.DidSucceede()
	if succeeded {
		err = playbook.TaskCompleted(task.Name())
		if err != nil {
			errz.Log(err)
		}
		taskStatus, err := playbook.TaskStatus(task.Name())
		if err != nil {
			errz.Log(err)
		}
		fmt.Printf("Task %q completed in %s\n", task.Name(), taskStatus.ExecutionTime())
	} else {
		for _, target := range failedTargets {
			fmt.Printf("Target %q does not exist", target)
		}
		err = playbook.TaskFailed(task.Name())
		killOnError(err)

		fmt.Printf("Task %q failed\n", taskname)
	}

	return nil
}

func killOnError(err error) {
	if err != nil {
		errz.Log(err)
		os.Exit(1)
	}
}
