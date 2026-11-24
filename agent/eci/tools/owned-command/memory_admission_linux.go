package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// createReservedCommandCgroup publishes a verified cap while holding the hierarchy
// lock. The cgroup itself holds the claim, even if the guard dies while descendants run.
func createReservedCommandCgroup(ctx context.Context, path string, limit MemoryBytes) (resultErr error) {
	userSlice := filepath.Join(cgroupRoot, "user.slice")
	if filepath.Dir(filepath.Dir(path)) != userManagerAppSlice(userSlice, strconv.Itoa(os.Getuid())) {
		return fmt.Errorf("memory admission requires a systemd user manager app.slice, got %s", path)
	}
	lock, err := lockMemoryAdmission(ctx, cgroupRoot)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Close()) }()
	if err := os.Mkdir(path, 0700); err != nil {
		return fmt.Errorf("create exclusive command cgroup %s: %w", path, err)
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, os.Remove(path))
		}
	}()
	if err := configureCommandMemory(path, limit); err != nil {
		return err
	}
	if err := admitCommandMemory(cgroupRoot, "/proc/meminfo", path, limit.pageFloor()); err != nil {
		return err
	}
	return ctx.Err()
}

// lockMemoryAdmission serializes capped launchers that see the same cgroup root.
func lockMemoryAdmission(ctx context.Context, root string) (*os.File, error) {
	lock, err := os.Open(root)
	if err != nil {
		return nil, fmt.Errorf("open cgroup root for memory admission: %w", err)
	}
	var retry *time.Ticker
	defer func() {
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
			retry = time.NewTicker(10 * time.Millisecond)
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), lock.Close())
		case <-retry.C:
		}
	}
}

// memoryBoundary records pending growth against one enclosing cgroup's controls.
// Unrelated peer workloads contribute only to the host and their shared ancestors.
type memoryBoundary struct {
	path   string
	growth MemoryBytes
}

// admitCommandMemory checks the host and each enclosing non-root cgroup. Host
// reclaimable memory is counted only through MemAvailable; ancestor headroom does
// not borrow candidate cache unless it has actually been reclaimed.
func admitCommandMemory(root, meminfo, command string, requested MemoryBytes) error {
	boundaries := make([]memoryBoundary, 0, 4)
	for path := filepath.Dir(command); path != root; path = filepath.Dir(path) {
		boundaries = append(boundaries, memoryBoundary{path: path})
	}
	var hostGrowth MemoryBytes
	userSlice := filepath.Join(root, "user.slice")
	users, err := os.ReadDir(userSlice)
	if err != nil {
		return err
	}
	for _, entry := range users {
		uid, ok := strings.CutPrefix(entry.Name(), "user-")
		if !ok || !entry.IsDir() {
			continue
		}
		uid, ok = strings.CutSuffix(uid, ".slice")
		if !ok {
			continue
		}
		if _, err := strconv.ParseUint(uid, 10, 32); err != nil {
			continue
		}
		app := userManagerAppSlice(userSlice, uid)
		scopes, err := os.ReadDir(app)
		switch {
		case errors.Is(err, os.ErrNotExist):
			continue
		case err != nil:
			return err
		}
		for _, scope := range scopes {
			if !scope.IsDir() || !strings.HasPrefix(scope.Name(), "owned-command-") || !strings.HasSuffix(scope.Name(), ".scope") {
				continue
			}
			peer := filepath.Join(app, scope.Name(), commandLeaf)
			if peer == command {
				continue
			}
			host, local, err := commandMemoryGrowth(peer)
			if err != nil {
				return err
			}
			hostGrowth = addMemoryGrowth(hostGrowth, host)
			for i := range boundaries {
				if strings.HasPrefix(peer, boundaries[i].path+"/") {
					boundaries[i].growth = addMemoryGrowth(boundaries[i].growth, local)
				}
			}
		}
	}
	available, err := readMemoryField(meminfo, "MemAvailable:", "kB")
	if err != nil {
		return err
	}
	if err := checkMemoryHeadroom("host MemAvailable", available, hostGrowth, requested); err != nil {
		return err
	}
	for _, boundary := range boundaries {
		if err := boundary.admit(requested); err != nil {
			return err
		}
	}
	return nil
}

// userManagerAppSlice locates the canonical default slice used by our transient scopes.
func userManagerAppSlice(parent, uid string) string {
	return filepath.Join(parent, "user-"+uid+".slice", "user@"+uid+".service", "app.slice")
}

// commandMemoryGrowth returns outstanding bytes relative to each baseline. The host
// already discounts reclaimable cache, so only resident nonswappable anon gets
// host credit. Ancestor memory.current also includes the peer's remaining cache;
// since no extra ancestor reclaim is credited, its entire current charge counts.
func commandMemoryGrowth(path string) (host, local MemoryBytes, err error) {
	cap, finite, err := readMemoryControl(filepath.Join(path, "memory.max"))
	if errors.Is(err, os.ErrNotExist) {
		// No controller on an uncapped scope, or a scope collected during discovery.
		return 0, 0, nil
	}
	if err != nil || !finite {
		return 0, 0, err
	}
	current, numeric, err := readMemoryControl(filepath.Join(path, "memory.current"))
	if errors.Is(err, os.ErrNotExist) {
		return cap, cap, nil
	}
	if err != nil {
		return 0, 0, err
	}
	if !numeric {
		return 0, 0, fmt.Errorf("expected numeric memory.current at %s", path)
	}
	swap, finite, err := readMemoryControl(filepath.Join(path, "memory.swap.max"))
	if errors.Is(err, os.ErrNotExist) {
		return cap, cap, nil
	}
	if err != nil {
		return 0, 0, err
	}
	host, local = cap, cap-min(cap, current)
	if finite && swap == 0 {
		resident, err := nonswappableAnonymousMemory(filepath.Join(path, "memory.stat"))
		if errors.Is(err, os.ErrNotExist) {
			return cap, cap, nil
		}
		if err != nil {
			return 0, 0, err
		}
		host -= min(cap, current, resident)
	}
	return host, local, nil
}

// nonswappableAnonymousMemory discounts every page ever marked MADV_FREE.
// Such anonymous pages can contribute to file LRUs; the cumulative counter
// provides a conservative bound without tracking their later lifetimes.
func nonswappableAnonymousMemory(path string) (MemoryBytes, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	stat := string(data)
	anon, err := parseMemoryField(stat, path, "anon", "")
	if err != nil {
		return 0, err
	}
	lazyPages, err := parseMemoryField(stat, path, "pglazyfree", "")
	if err != nil {
		return 0, err
	}
	page := MemoryBytes(os.Getpagesize())
	if lazyPages > anon/page {
		return 0, nil
	}
	return anon - lazyPages*page, nil
}

// admit checks the tighter of the kernel's finite hard limit and reclaim threshold.
func (b memoryBoundary) admit(requested MemoryBytes) error {
	limit := ^MemoryBytes(0)
	for _, name := range [...]string{"memory.max", "memory.high"} {
		value, finite, err := readMemoryControl(filepath.Join(b.path, name))
		if err != nil {
			return err
		}
		if finite {
			limit = min(limit, value)
		}
	}
	if limit == ^MemoryBytes(0) {
		return nil
	}
	current, finite, err := readMemoryControl(filepath.Join(b.path, "memory.current"))
	if err != nil {
		return err
	}
	if !finite {
		return fmt.Errorf("expected numeric memory.current at %s", b.path)
	}
	return checkMemoryHeadroom(b.path, limit-min(limit, current), b.growth, requested)
}

// checkMemoryHeadroom subtracts in order without underflow, even when growth is saturated.
func checkMemoryHeadroom(boundary string, available, growth, requested MemoryBytes) error {
	if requested > available-min(available, growth) {
		return fmt.Errorf("memory admission refused at %s: %d requested bytes, %d available bytes, %d bytes held for existing commands", boundary, requested, available, growth)
	}
	return nil
}

// addMemoryGrowth saturates rather than wrapping a sum of kernel limits.
func addMemoryGrowth(total, extra MemoryBytes) MemoryBytes {
	return total + min(extra, ^MemoryBytes(0)-total)
}

// readMemoryControl reads a cgroup byte value or the kernel's unlimited marker.
func readMemoryControl(path string) (MemoryBytes, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false, err
	}
	value := strings.TrimSpace(string(data))
	if value == "max" {
		return 0, false, nil
	}
	bytes, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("parse %s: %w", path, err)
	}
	return MemoryBytes(bytes), true, nil
}

// readMemoryField extracts a single numeric kernel field in bytes or binary kilobytes.
func readMemoryField(path, field, unit string) (MemoryBytes, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return parseMemoryField(string(data), path, field, unit)
}

// parseMemoryField verifies the field's units before converting its value.
func parseMemoryField(data, path, field, unit string) (MemoryBytes, error) {
	for line := range strings.Lines(data) {
		if key, _, ok := strings.Cut(line, " "); !ok || key != field {
			continue
		}
		values := strings.Fields(line)
		if len(values) == 2 && unit == "" || len(values) == 3 && values[2] == unit {
			value, err := strconv.ParseUint(values[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parse %s field %s: %w", path, field, err)
			}
			if unit == "" {
				return MemoryBytes(value), nil
			}
			if value <= ^uint64(0)/1024 {
				return MemoryBytes(value * 1024), nil
			}
		}
		break
	}
	return 0, fmt.Errorf("invalid or missing %s in %s", field, path)
}
