package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xaionaro-go/observability"
	"golang.org/x/sys/unix"
)

const (
	// memoryClaimEventBufferSize batches inotify notifications for a level check.
	//
	// Example: multiple coalesced claim deletions fit in one read.
	memoryClaimEventBufferSize = 4096

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

// armMemoryClaimWatch installs parent watches and returns nil if a leaf is already gone.
//
// Example: a leaf gone just before registration does not need a new event.
func armMemoryClaimWatch(
	ctx context.Context,
	claims []memoryClaim,
) (watch *memoryClaimWatch, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, fmt.Errorf("open memory claim watch: %w", err)
	}
	armed := &memoryClaimWatch{fd: fd, claims: claims}
	// Close the descriptor unless ownership passes to the caller.
	//
	// Example: an already-removed leaf has no watch to close in the caller.
	defer func() {
		if watch == nil {
			resultErr = closeMemoryWatchFD(fd, resultErr)
		}
	}()
	// kernfs reports child removal on the parent, not reliably on the leaf.
	for _, claim := range claims {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		_, err := unix.InotifyAddWatch(fd, filepath.Dir(claim.Path), unix.IN_ONLYDIR|unix.IN_DELETE)
		switch {
		case isGoneMemoryCgroup(err):
			return nil, ctx.Err()
		case err != nil:
			return nil, fmt.Errorf("watch memory claim %s: %w", claim.Path, err)
		}
	}
	gone, err := armed.claimGone()
	switch {
	case err != nil:
		return nil, err
	case gone || ctx.Err() != nil:
		return nil, ctx.Err()
	default:
		return armed, nil
	}
}

// closeMemoryWatchFD preserves a cleanup fault as the classification of a wait.
//
// Example: a failed close after a queue expiry is a guard error.
func closeMemoryWatchFD(
	fd int,
	prior error,
) error {
	if err := unix.Close(fd); err != nil {
		return fmt.Errorf("close memory claim watch after %v: %w", prior, err)
	}
	return prior
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
	fd            int
	fallbackWrite int
	finished      chan struct{}
	done          chan struct{}
	err           error
}

// notify signals a private eventfd on cancellation and joins when the wait returns.
//
// Example: closing the fallback pipe wakes Poll even when the eventfd write fails.
func (s *memoryClaimWatchStop) notify(ctx context.Context) {
	defer close(s.done)
	select {
	case <-s.finished:
		return
	case <-ctx.Done():
	}
	if err := signalStopEvent(s.fd); err != nil {
		s.err = errors.Join(err, unix.Close(s.fallbackWrite))
		s.fallbackWrite = -1
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
	var fallback [2]int
	if err := unix.Pipe2(fallback[:], unix.O_CLOEXEC); err != nil {
		return closeMemoryWatchFD(fd, fmt.Errorf("create memory claim watch stop fallback: %w", err))
	}
	stop := memoryClaimWatchStop{fd: fd, fallbackWrite: fallback[1], finished: make(chan struct{}), done: make(chan struct{})}
	observability.Go(ctx, stop.notify)
	// Join before closing any descriptor the notifier can still use.
	//
	// Example: a concurrent claim deletion cannot race a write to a reused fd number.
	defer func() {
		close(stop.finished)
		<-stop.done
		if stop.err != nil {
			resultErr = fmt.Errorf("stop memory claim watch after %v: %w", resultErr, stop.err)
		}
		if stop.fallbackWrite >= 0 {
			resultErr = closeMemoryWatchFD(stop.fallbackWrite, resultErr)
		}
		resultErr = closeMemoryWatchFD(fallback[0], closeMemoryWatchFD(fd, resultErr))
	}()
	return w.poll(ctx, fd, fallback[0])
}

// poll waits for a claim event or a stop, checking current levels after each notification.
//
// Example: deletion of a sibling only triggers a level check.
func (w memoryClaimWatch) poll(
	ctx context.Context,
	stopFD int,
	fallbackFD int,
) error {
	fds := [...]unix.PollFd{{Fd: int32(w.fd), Events: unix.POLLIN}, {Fd: int32(stopFD), Events: unix.POLLIN}, {Fd: int32(fallbackFD), Events: unix.POLLIN}}
	var event [memoryClaimEventBufferSize]byte
	for {
		_, err := unix.Poll(fds[:], -1)
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case err != nil:
			return fmt.Errorf("poll memory claim watches: %w", err)
		case ctx.Err() != nil:
			return ctx.Err()
		case fds[0].Revents != unix.POLLIN || fds[1].Revents != 0 || fds[2].Revents != 0:
			return fmt.Errorf("unexpected memory claim watch readiness: %#x, stop %#x, fallback %#x", fds[0].Revents, fds[1].Revents, fds[2].Revents)
		}
		switch _, err := unix.Read(w.fd, event[:]); {
		case errors.Is(err, unix.EINTR):
			continue
		case err != nil:
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
	watch, err := armMemoryClaimWatch(ctx, claims)
	if err != nil || watch == nil {
		return err
	}
	// Close the inotify descriptor after the wait joins its notifier.
	//
	// Example: an interrupted wait cannot leak its armed watches.
	defer func() { resultErr = closeMemoryWatchFD(watch.fd, resultErr) }()
	return watch.Wait(ctx)
}
