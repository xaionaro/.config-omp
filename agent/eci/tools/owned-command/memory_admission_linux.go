package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xaionaro-go/observability"
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
			resultErr = memoryClaimRollbackError(path, resultErr, errors.Join(os.Remove(path), lock.Close()))
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

// memoryClaimRollbackError keeps failed cleanup from being mistaken for a retryable refusal.
//
// Example: an unremoved tentative leaf makes the result a guard error.
func memoryClaimRollbackError(
	path string,
	admissionErr error,
	cleanupErr error,
) error {
	if cleanupErr == nil {
		return admissionErr
	}
	var refusal *memoryAdmissionRefusal
	if errors.As(admissionErr, &refusal) {
		return fmt.Errorf("roll back refused memory claim %s after %v: %w", path, admissionErr, cleanupErr)
	}
	return errors.Join(admissionErr, cleanupErr)
}

// memoryClaimWatch watches parent directories for command leaf removal.
//
// Example: sibling events cause a fresh level check, not a successful wake by themselves.
type memoryClaimWatch struct {
	fd     int
	claims []memoryClaim
}

// armMemoryClaimWatch installs one set of watches and checks for earlier removals.
//
// Example: a leaf gone just before registration does not need a new event.
func armMemoryClaimWatch(
	ctx context.Context,
	claims []memoryClaim,
) (watch memoryClaimWatch, gone bool, resultErr error) {
	if err := ctx.Err(); err != nil {
		return watch, false, err
	}
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return watch, false, fmt.Errorf("open memory claim watch: %w", err)
	}
	watch = memoryClaimWatch{fd: fd, claims: claims}
	defer func() {
		if gone || resultErr != nil {
			if err := watch.Close(); err != nil {
				resultErr = fmt.Errorf("close after arming memory claim watch (gone %t, error %v): %w", gone, resultErr, err)
			}
		}
	}()
	// kernfs reports child removal on the parent, not reliably on the leaf.
	for _, claim := range claims {
		if err := ctx.Err(); err != nil {
			return watch, false, err
		}
		_, err := unix.InotifyAddWatch(fd, filepath.Dir(claim.Path), unix.IN_ONLYDIR|unix.IN_DELETE)
		switch {
		case isGoneMemoryCgroup(err):
			return watch, true, ctx.Err()
		case err != nil:
			return watch, false, fmt.Errorf("watch memory claim %s: %w", claim.Path, err)
		}
	}
	gone, err = watch.claimGone()
	if err != nil {
		return watch, false, err
	}
	return watch, gone, ctx.Err()
}

// claimGone checks watched leaf levels after installation or any notification.
//
// Example: overflow or an ignored watch also triggers this check.
func (w memoryClaimWatch) claimGone() (bool, error) {
	for _, claim := range w.claims {
		_, err := os.Lstat(claim.Path)
		switch {
		case isGoneMemoryCgroup(err):
			return true, nil
		case err != nil:
			return false, fmt.Errorf("verify memory claim removal %s: %w", claim.Path, err)
		}
	}
	return false, nil
}

// Close releases the inotify descriptor after Wait has stopped polling it.
//
// Example: a retry installs a fresh watch rather than reusing the old claim set.
func (w memoryClaimWatch) Close() error {
	if err := unix.Close(w.fd); err != nil {
		return fmt.Errorf("close memory claim watch: %w", err)
	}
	return nil
}

// memoryClaimWatchStop translates context cancellation into a pollable event.
//
// Example: a signal wakes Poll even if every watched claim still exists.
type memoryClaimWatchStop struct {
	fd       int
	finished chan struct{}
	done     chan struct{}
}

// notify signals a private eventfd on cancellation and joins when the wait returns.
//
// Example: a stop concurrent with claim deletion cannot write to a reused descriptor.
func (s memoryClaimWatchStop) notify(ctx context.Context) {
	defer close(s.done)
	select {
	case <-s.finished:
		return
	case <-ctx.Done():
	}
	var value [8]byte
	binary.NativeEndian.PutUint64(value[:], 1)
	for {
		// The only writer writes once to a live private eventfd.
		_, err := unix.Write(s.fd, value[:])
		if !errors.Is(err, unix.EINTR) {
			return
		}
	}
}

// Wait blocks for one armed leaf to disappear, or for the queue to stop.
//
// Example: a removal queued between arming and waiting is consumed by Poll.
func (w memoryClaimWatch) Wait(ctx context.Context) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	fd, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		return fmt.Errorf("create memory claim watch stop event: %w", err)
	}
	stop := memoryClaimWatchStop{fd: fd, finished: make(chan struct{}), done: make(chan struct{})}
	observability.Go(ctx, stop.notify)
	defer func() {
		close(stop.finished)
		<-stop.done
		if err := unix.Close(fd); err != nil {
			resultErr = fmt.Errorf("close memory claim watch stop event after %v: %w", resultErr, err)
		}
	}()
	fds := [...]unix.PollFd{{Fd: int32(w.fd), Events: unix.POLLIN}, {Fd: int32(fd), Events: unix.POLLIN}}
	var event [4096]byte
	for {
		_, err := unix.Poll(fds[:], -1)
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case err != nil:
			return fmt.Errorf("poll memory claim watches: %w", err)
		case ctx.Err() != nil:
			return ctx.Err()
		case fds[0].Revents != unix.POLLIN || fds[1].Revents != 0:
			return fmt.Errorf("unexpected memory claim watch readiness: %#x, stop %#x", fds[0].Revents, fds[1].Revents)
		}
		if _, err := unix.Read(w.fd, event[:]); err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return fmt.Errorf("read memory claim watches: %w", err)
		}
		gone, err := w.claimGone()
		if err != nil {
			return err
		}
		if gone {
			return ctx.Err()
		}
	}
}

// waitForMemoryClaims waits until any sufficient claim disappears.
//
// Example: a claim removed before watch installation lets admission retry immediately.
func waitForMemoryClaims(
	ctx context.Context,
	claims []memoryClaim,
) (resultErr error) {
	watch, gone, err := armMemoryClaimWatch(ctx, claims)
	if err != nil || gone {
		return err
	}
	defer func() {
		if err := watch.Close(); err != nil {
			resultErr = fmt.Errorf("close memory claim wait after %v: %w", resultErr, err)
		}
	}()
	return watch.Wait(ctx)
}
