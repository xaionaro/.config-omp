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
	queue bool,
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
			if cleanupErr := errors.Join(os.Remove(path), lock.Close()); cleanupErr != nil {
				resultErr = errors.Join(resultErr, cleanupErr)
			}
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
	if err := checkCommandMemory(ctx, view, topology, floor, limit.pageFloor(), queue); err != nil {
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

// waitForMemoryClaim waits for the peer leaf's removal, not merely process exit.
// Charges and the claim may outlive the peer's population.
//
// Example: if a peer removes its leaf before watch installation, admission can retry immediately.
func waitForMemoryClaim(
	ctx context.Context,
	path string,
) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return fmt.Errorf("open memory claim watch: %w", err)
	}
	file := os.NewFile(uintptr(fd), "memory claim watch")
	// kernfs does not reliably report DELETE_SELF on a cgroup v2 leaf.
	if _, err := unix.InotifyAddWatch(fd, filepath.Dir(path), unix.IN_ONLYDIR|unix.IN_DELETE); err != nil {
		if isGoneMemoryCgroup(err) {
			return errors.Join(ctx.Err(), file.Close())
		}
		return errors.Join(fmt.Errorf("watch memory claim %s: %w", path, err), file.Close())
	}
	closed := make(chan error, 1)
	stop := context.AfterFunc(ctx,
		// Close interrupts the pollable descriptor's pending read.
		//
		// Example: setup signals interrupt a queued memory request.
		func() { closed <- file.Close() },
	)
	defer
	// Only one party owns the close; join it before returning.
	//
	// Example: cancellation at the same moment as removal still closes the watch once.
	func() {
		if stop() {
			resultErr = errors.Join(resultErr, file.Close())
			return
		}
		resultErr = errors.Join(resultErr, <-closed)
	}()
	var event [4096]byte
	for {
		// Checking the leaf after arming catches removal before installation.
		// Rechecking after notification ignores deletion of sibling leaves.
		_, err := os.Lstat(path)
		switch {
		case isGoneMemoryCgroup(err):
			return ctx.Err()
		case err != nil:
			return fmt.Errorf("verify memory claim removal %s: %w", path, err)
		}
		if _, err := file.Read(event[:]); err != nil {
			if errors.Is(err, os.ErrClosed) && ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read memory claim watch %s: %w", path, err)
		}
	}
}
