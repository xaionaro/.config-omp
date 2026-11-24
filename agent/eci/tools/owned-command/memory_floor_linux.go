package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// earlyoomUnit identifies the root-managed cgroup trusted for daemon membership.
	//
	// Example: an unrelated process named earlyoom outside this unit is ignored.
	earlyoomUnit = "/system.slice/earlyoom.service"

	// earlyoomProcRoot is the procfs used to inspect live process membership.
	//
	// Example: /proc/123/cgroup records where PID 123 lives now.
	earlyoomProcRoot = "/proc"

	// earlyoomProcMembership is the procfs name of the membership file.
	//
	// Example: the v2 membership line starts with 0::.
	earlyoomProcMembership = "cgroup"

	// earlyoomProcArguments holds the NUL-delimited live argument vector.
	//
	// Example: /proc/123/cmdline supplies the daemon's effective flags.
	earlyoomProcArguments = "cmdline"

	// earlyoomDefaultMemoryPercent is the term threshold with no memory flags.
	//
	// Example: earlyoom -s100 still uses the default 10% memory threshold.
	earlyoomDefaultMemoryPercent = 10

	// earlyoomMaxMemoryPercent is the upper limit accepted for -m.
	//
	// Example: -m100 is invalid even though -s100 is valid.
	earlyoomMaxMemoryPercent = 99

	// memoryPercentBase converts percentages to fractions.
	//
	// Example: 18 percent is 18 divided by memoryPercentBase.
	memoryPercentBase = 100
)

// The option names follow earlyoom v1.9.0 main.c's getopt_long short_opt and
// long_opt tables: https://github.com/rfjakob/earlyoom/blob/v1.9.0/main.c
// Upstream added --kernel-oom in https://github.com/rfjakob/earlyoom/commit/750a7c0d9.
const (
	// earlyoomMemoryPercentFlag selects the memory percentage threshold.
	//
	// Example: -m18 sets the floor to 18 percent.
	earlyoomMemoryPercentFlag = 'm'
	// earlyoomMemoryKiBFlag selects the absolute memory threshold.
	//
	// Example: -M1048576 sets a 1 GiB threshold.
	earlyoomMemoryKiBFlag = 'M'
	// earlyoomSwapPercentFlag selects the swap percentage threshold.
	//
	// Example: -s100 does not reduce the memory floor.
	earlyoomSwapPercentFlag = 's'
	// earlyoomSwapKiBFlag selects the absolute swap threshold.
	//
	// Example: -S512 is parsed without changing the memory floor.
	earlyoomSwapKiBFlag = 'S'
	// earlyoomShortNoValue lists flags without values.
	//
	// Example: -d can appear before another short option.
	earlyoomShortNoValue = "kingdvph"
	// earlyoomShortWithValue lists flags that consume a value.
	//
	// Example: -N and -m both consume an attached or next argument.
	earlyoomShortWithValue = string(earlyoomMemoryPercentFlag) + string(earlyoomMemoryKiBFlag) +
		string(earlyoomSwapPercentFlag) + string(earlyoomSwapKiBFlag) + "NPr"
	// earlyoomLongPrefer selects a preferred process pattern.
	//
	// Example: --prefer=worker consumes a pattern.
	earlyoomLongPrefer = "prefer"
	// earlyoomLongAvoid selects a process pattern to avoid.
	//
	// Example: --avoid=worker consumes a pattern.
	earlyoomLongAvoid = "avoid"
	// earlyoomLongIgnore selects a process pattern to ignore.
	//
	// Example: --ignore=worker consumes a pattern.
	earlyoomLongIgnore = "ignore"
	// earlyoomLongIgnoreRoot excludes root-owned processes.
	//
	// Example: --ignore-root-user takes no value.
	earlyoomLongIgnoreRoot = "ignore-root-user"
	// earlyoomLongDryRun requests observation without signaling.
	//
	// Example: --dryrun takes no value.
	earlyoomLongDryRun = "dryrun"
	// earlyoomLongSortRSS selects RSS-based victim ordering.
	//
	// Example: --sort-by-rss takes no value.
	earlyoomLongSortRSS = "sort-by-rss"
	// earlyoomLongSyslog enables syslog output.
	//
	// Example: --syslog takes no value.
	earlyoomLongSyslog = "syslog"
	// earlyoomLongKernelOOM selects the kernel OOM killer without changing the memory threshold.
	//
	// Example: --kernel-oom takes no value.
	earlyoomLongKernelOOM = "kernel-oom"
	// earlyoomLongHelp requests usage information.
	//
	// Example: --help takes no value.
	earlyoomLongHelp = "help"
	// earlyoomLongDebug enables debug output.
	//
	// Example: --debug takes no value.
	earlyoomLongDebug = "debug"
)

// readEarlyoomMemoryFloor takes the maximum threshold among current members of
// the trusted service cgroup; the independent swap condition is not credited.
//
// Example: service members at 10% and 18% give an 18% floor.
func readEarlyoomMemoryFloor(
	ctx context.Context,
	view memoryHostView,
	root string,
) (MemoryBytes, error) {
	unit := filepath.Join(root, earlyoomUnit, cgroupProcsFile)
	pids, err := view.readFile(unit)
	if isGoneMemoryCgroup(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read earlyoom unit membership: %w", err)
	}

	var total, floor MemoryBytes
	for pid := range strings.FieldsSeq(string(pids)) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, err := strconv.ParseUint(pid, 10, 32)
		if err != nil || n == 0 {
			return 0, fmt.Errorf("invalid PID in earlyoom unit membership")
		}

		args, err := readEarlyoomArguments(view, filepath.Join(earlyoomProcRoot, pid))
		if errors.Is(err, os.ErrNotExist) {
			err = checkHiddenEarlyoomPID(view, unit, pid)
		}
		if err != nil {
			return 0, fmt.Errorf("inspect earlyoom PID %s: %w", pid, err)
		}
		if args == nil {
			continue
		}
		if total == 0 {
			total, err = readPositiveMemTotal(view)
		}
		if err != nil {
			return 0, err
		}
		threshold, err := earlyoomHostFloor(total, args[1:])
		if err != nil {
			return 0, fmt.Errorf("parse earlyoom PID %s options: %w", pid, err)
		}
		floor = max(floor, threshold)
	}
	return floor, nil
}

// readPositiveMemTotal reads the nonzero byte total used for percentage thresholds.
//
// Example: an active earlyoom process cannot use a zero MemTotal denominator.
func readPositiveMemTotal(view memoryHostView) (MemoryBytes, error) {
	total, err := view.meminfoBytes(meminfoTotalKey)
	if err != nil {
		return 0, err
	}
	if total == 0 {
		return 0, fmt.Errorf("%s must be positive", meminfoTotalKey)
	}
	return total, nil
}

// readEarlyoomArguments revalidates service membership after reading argv.
// TODO: On empty argv during exit, re-read cgroup.procs; skip unlisted PIDs
// and fail closed if the PID is still listed.
//
// Example: ignore a PID that exited or moved to another unit before validation.
func readEarlyoomArguments(
	view memoryHostView,
	dir string,
) ([]string, error) {
	cmdline, cmdlineErr := view.readFile(filepath.Join(dir, earlyoomProcArguments))
	if errors.Is(cmdlineErr, os.ErrNotExist) {
		return nil, cmdlineErr
	}
	membership, err := view.readFile(filepath.Join(dir, earlyoomProcMembership))
	if err != nil {
		return nil, err
	}
	for line := range strings.Lines(string(membership)) {
		if strings.TrimSuffix(line, "\n") != cgroupV2Prefix+earlyoomUnit {
			continue
		}
		if cmdlineErr != nil {
			return nil, cmdlineErr
		}
		argv, ok := strings.CutSuffix(string(cmdline), "\x00")
		if !ok || argv == "" || argv[0] == 0 {
			return nil, fmt.Errorf("invalid or missing earlyoom argv")
		}
		return strings.Split(argv, "\x00"), nil
	}
	return nil, nil
}

// checkHiddenEarlyoomPID differentiates an exited member from one hidden
// by the procfs mount.
//
// Example: a PID still in cgroup.procs might be hidden by hidepid.
func checkHiddenEarlyoomPID(
	view memoryHostView,
	unit string,
	pid string,
) error {
	pids, err := view.readFile(unit)
	if isGoneMemoryCgroup(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("recheck earlyoom unit membership: %w", err)
	}
	for member := range strings.FieldsSeq(string(pids)) {
		if member == pid {
			return fmt.Errorf("PID %s is still in the earlyoom unit but /proc hides it (for example, hidepid)", pid)
		}
	}
	return nil
}

// earlyoomHostFloor follows earlyoom's getopt_long option table and threshold
// merging in main.c (v1.9.0), conservatively rounded up to a whole byte.
// It accepts the later upstream --kernel-oom option without changing the floor.
//
// Example: -m18,13 -M1048576 uses the lower of 18% and one GiB for the floor.
func earlyoomHostFloor(
	total MemoryBytes,
	args []string,
) (MemoryBytes, error) {
	var options earlyoomMemoryOptions
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		if arg == "--" && len(args) == 0 {
			break
		}
		var err error
		args, err = options.consume(total, arg, args)
		if err != nil {
			return 0, err
		}
	}

	floor := options.kib * bytesPerKiB
	if options.percent == 0 && options.kib == 0 {
		options.percent = earlyoomDefaultMemoryPercent
	}
	if options.percent != 0 {
		fromPercent := float64(total) * options.percent / memoryPercentBase
		if options.kib == 0 || fromPercent < floor {
			floor = fromPercent
		}
	}
	if floor >= float64(math.MaxUint64) {
		return 0, fmt.Errorf("earlyoom floor overflows byte count")
	}
	return MemoryBytes(math.Ceil(floor)), nil
}

// consume parses an earlyoom option and returns the arguments left after it.
//
// Example: a long option that needs a value can consume the next argument.
func (o *earlyoomMemoryOptions) consume(
	total MemoryBytes,
	arg string,
	args []string,
) ([]string, error) {
	if long, ok := strings.CutPrefix(arg, "--"); ok {
		return consumeEarlyoomLongOption(long, args)
	}
	if len(arg) < 2 || arg[0] != '-' {
		return nil, fmt.Errorf("unexpected positional earlyoom argument")
	}
	return o.consumeShort(total, arg[1:], args)
}

// earlyoomMemoryOptions records the last -m and -M threshold options.
// Threshold fields are set only after parsing and checking each option's range.
//
// Example: the two options combine using the lower floor when both occur.
type earlyoomMemoryOptions struct {
	percent float64
	kib     float64
}

// consumeShort processes a cluster of short options and any following value.
//
// Example: -dm18 enables debugging and sets an 18% threshold.
func (o *earlyoomMemoryOptions) consumeShort(
	total MemoryBytes,
	flags string,
	args []string,
) ([]string, error) {
	for len(flags) != 0 {
		flag := flags[0]
		flags = flags[1:]
		if strings.IndexByte(earlyoomShortNoValue, flag) >= 0 {
			continue
		}
		if strings.IndexByte(earlyoomShortWithValue, flag) < 0 {
			return nil, fmt.Errorf("unknown earlyoom short option")
		}
		value, rest, err := earlyoomShortValue(flag, flags, args)
		if err != nil {
			return nil, err
		}
		if err := o.setThreshold(total, flag, value); err != nil {
			return nil, fmt.Errorf("invalid earlyoom -%c threshold: %w", flag, err)
		}
		return rest, nil
	}
	return args, nil
}

// earlyoomShortValue returns a short option's attached or next argument.
//
// Example: -m18 and -m 18 both supply 18.
func earlyoomShortValue(
	flag byte,
	attached string,
	args []string,
) (string, []string, error) {
	if attached != "" {
		return attached, args, nil
	}
	if len(args) == 0 {
		return "", nil, fmt.Errorf("missing argument for earlyoom -%c", flag)
	}
	return args[0], args[1:], nil
}

// setThreshold validates a memory or swap threshold and records memory values.
//
// Example: swap thresholds are parsed but do not reduce the host floor.
func (o *earlyoomMemoryOptions) setThreshold(
	total MemoryBytes,
	flag byte,
	value string,
) error {
	var err error
	switch flag {
	case earlyoomMemoryPercentFlag:
		o.percent, err = parseEarlyoomThreshold(value, earlyoomMaxMemoryPercent)
	case earlyoomMemoryKiBFlag:
		upper := float64(total/bytesPerKiB) * memoryPercentBase / earlyoomMaxMemoryPercent
		o.kib, err = parseEarlyoomThreshold(value, math.Floor(upper))
	case earlyoomSwapPercentFlag:
		_, err = parseEarlyoomThreshold(value, memoryPercentBase)
	case earlyoomSwapKiBFlag:
		_, err = parseEarlyoomThreshold(value, math.Inf(1))
	}
	return err
}

// consumeEarlyoomLongOption applies GNU getopt_long's exact-name or unique
// prefix match, and consumes an optional attached or required separate value.
//
// Example: --avoid=-m and --avoid -m do not set the memory threshold.
func consumeEarlyoomLongOption(
	input string,
	args []string,
) ([]string, error) {
	name, _, attached := strings.Cut(input, "=")
	match, err := matchEarlyoomLongOption(name)
	if err != nil {
		return nil, err
	}
	switch match {
	case earlyoomLongPrefer, earlyoomLongAvoid, earlyoomLongIgnore:
		if attached {
			return args, nil
		}
		if len(args) == 0 {
			return nil, fmt.Errorf("missing value for earlyoom --%s", match)
		}
		return args[1:], nil
	default:
		if attached {
			return nil, fmt.Errorf("unexpected value for earlyoom --%s", match)
		}
		return args, nil
	}
}

// matchEarlyoomLongOption resolves an exact long option or an unambiguous prefix.
//
// Example: ignore matches exactly, although it also prefixes ignore-root-user.
func matchEarlyoomLongOption(name string) (string, error) {
	var match string
	ambiguous := false
	for _, option := range [...]string{
		earlyoomLongPrefer, earlyoomLongAvoid, earlyoomLongIgnore, earlyoomLongIgnoreRoot,
		earlyoomLongDryRun, earlyoomLongSortRSS, earlyoomLongSyslog, earlyoomLongKernelOOM,
		earlyoomLongHelp, earlyoomLongDebug,
	} {
		if option == name {
			return option, nil
		}
		if !strings.HasPrefix(option, name) {
			continue
		}
		ambiguous = match != ""
		match = option
	}
	switch {
	case ambiguous:
		return "", fmt.Errorf("ambiguous earlyoom long option")
	case match == "":
		return "", fmt.Errorf("unknown earlyoom long option")
	default:
		return match, nil
	}
}

// parseEarlyoomThreshold returns the first threshold at which earlyoom can
// act, including its promotion when the SIGKILL threshold is higher.
//
// Example: 12,18 becomes 18, and 18,13 also becomes 18.
func parseEarlyoomThreshold(
	input string,
	upper float64,
) (float64, error) {
	term, kill, hasKill := strings.Cut(input, ",")
	termValue, err := parseEarlyoomThresholdPart(term, upper)
	if err != nil {
		return 0, err
	}
	if hasKill {
		killValue, err := parseEarlyoomThresholdPart(kill, upper)
		if err != nil {
			return 0, err
		}
		termValue = max(termValue, killValue)
	}
	if termValue == 0 {
		return 0, fmt.Errorf("both thresholds are zero")
	}
	return termValue, nil
}

// parseEarlyoomThresholdPart parses one numeric component as in upstream msg.c.
//
// Example: a finite decimal fraction is allowed, but NaN is not.
func parseEarlyoomThresholdPart(
	input string,
	upper float64,
) (float64, error) {
	// strtod accepts an empty component as zero; both components cannot be zero.
	if input == "" {
		return 0, nil
	}
	value, err := strconv.ParseFloat(strings.TrimLeft(input, " \t\v\r\n\f"), 64)
	if err != nil || strings.ContainsRune(input, '_') || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > upper {
		return 0, fmt.Errorf("threshold is not a finite value in range")
	}
	return value, nil
}
