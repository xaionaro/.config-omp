package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// cgroupTypeFile exists in every non-root cgroup v2 directory.
	//
	// Example: a delegated mount root must not contain this file.
	cgroupTypeFile = "cgroup.type"

	// memoryAdmissionLockRetry is the polling interval for contended flocks.
	//
	// Example: contention remains interruptible by the setup context.
	memoryAdmissionLockRetry = 10 * time.Millisecond
)

// createVisibleCgroup serializes leaf creation with other admissions. A
// restrictive umask can hide the leaf between mkdir and chmod, causing an
// admission walker to fail closed if it sees the intermediate mode.
//
// Example: uncapped launches lock their visible root without requiring the host view.
func createVisibleCgroup(
	ctx context.Context,
	root string,
	path string,
) error {
	lock, err := lockMemoryAdmission(ctx, root)
	if err != nil {
		return err
	}
	if err := makeOwnedCommandCgroup(path); err != nil {
		return errors.Join(err, lock.Close())
	}
	if err := lock.Close(); err != nil {
		return errors.Join(fmt.Errorf("close memory admission lock: %w", err), os.Remove(path))
	}
	return nil
}

// createClaimedCommandCgroup commits a verified cap under the host admission
// lock. The command cgroup retains the claim until it is removed.
//
// Example: a failed admission removes the tentative command cgroup before unlocking.
func createClaimedCommandCgroup(
	ctx context.Context,
	path string,
	limit MemoryBytes,
) (resultErr error) {
	lock, err := lockHostMemoryAdmission(ctx, cgroupRoot)
	if err != nil {
		return err
	}
	if err := makeOwnedCommandCgroup(path); err != nil {
		return errors.Join(err, lock.Close())
	}
	defer
	// Roll back before unlocking; even a failed lock close must prevent launch.
	//
	// Example: a rejected request leaves no capacity claim.
	func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, os.Remove(path), lock.Close())
			return
		}
		if err := lock.Close(); err != nil {
			resultErr = errors.Join(fmt.Errorf("close host memory admission lock: %w", err), os.Remove(path))
		}
	}()
	if err := configureCommandMemory(path, limit); err != nil {
		return err
	}
	view := memoryHostView{Source: os.DirFS("/")}
	topology, err := discoverMemoryTopology(ctx, view, cgroupRoot, path)
	if err != nil {
		return err
	}
	floor, err := readEarlyoomMemoryFloor(ctx, view, cgroupRoot)
	if err != nil {
		return err
	}
	if err := checkCommandMemory(ctx, view, topology, floor, limit.pageFloor()); err != nil {
		return err
	}
	return ctx.Err()
}

// lockHostMemoryAdmission rejects delegated hierarchy views before taking the
// shared root inode lock. Close the returned file to release the lock.
//
// Example: hold the lock across claim discovery and cap publication.
func lockHostMemoryAdmission(
	ctx context.Context,
	root string,
) (*os.File, error) {
	_, err := os.Lstat(filepath.Join(root, cgroupTypeFile))
	if err == nil {
		return nil, fmt.Errorf("memory admission requires the host cgroup root; %s contains %s", root, cgroupTypeFile)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("verify host cgroup root: %w", err)
	}
	return lockMemoryAdmission(ctx, root)
}

// lockMemoryAdmission serializes admission and leaf visibility on one cgroup root.
//
// Example: Go's read-only os.Open sets close-on-exec on the lock descriptor.
func lockMemoryAdmission(
	ctx context.Context,
	root string,
) (*os.File, error) {
	lock, err := os.Open(root)
	if err != nil {
		return nil, fmt.Errorf("open cgroup root for memory admission: %w", err)
	}
	var retry *time.Ticker
	defer
	// Release the retry timer, if needed, without closing a successful lock.
	//
	// Example: an uncontended acquisition never allocates a timer.
	func() {
		if retry != nil {
			retry.Stop()
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, lock.Close())
		}
		err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		switch {
		case err == nil:
			return lock, nil
		case errors.Is(err, unix.EINTR):
			continue
		case !errors.Is(err, unix.EWOULDBLOCK):
			return nil, errors.Join(fmt.Errorf("lock cgroup root for memory admission: %w", err), lock.Close())
		}
		// Flock has no context-aware waiting API. Only contended admissions retry.
		if retry == nil {
			retry = time.NewTicker(memoryAdmissionLockRetry)
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), lock.Close())
		case <-retry.C:
		}
	}
}
