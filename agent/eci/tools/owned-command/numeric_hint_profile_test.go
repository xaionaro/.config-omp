//go:build linux

package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// numericHintNativeSource supplies controlled thread groups without a competing guard implementation.
// The nonleader owns its forked child and joins it before the group exits.
//
// Example: lifetime 8 keeps seven workers alive after the group leader exits.
const numericHintNativeSource = `
#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <pthread.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/prctl.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

static int pidfd_race(int argc, char **argv);

static pthread_mutex_t lock = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t changed = PTHREAD_COND_INITIALIZER;
static pthread_t workers[127];
static int stop, leader_exit, worker_count, lifetime;

static void die(const char *message) {
    perror(message);
    _exit(90);
}
static void check(int result, const char *message) {
    if (result != 0) { errno = result; die(message); }
}
static void line(const char *message) {
    if (puts(message) == EOF || fflush(stdout) != 0) die("stdout");
}
static void release_workers(void) {
    check(pthread_mutex_lock(&lock), "lock");
    stop = 1;
    check(pthread_cond_broadcast(&changed), "broadcast");
    check(pthread_mutex_unlock(&lock), "unlock");
}
static void *sleeper(void *unused) {
    (void)unused;
    check(pthread_mutex_lock(&lock), "lock");
    while (!stop) check(pthread_cond_wait(&changed, &lock), "wait");
    check(pthread_mutex_unlock(&lock), "unlock");
    return NULL;
}
static void *controller(void *unused) {
    (void)unused;
    int pipefd[2];
    if (pipe(pipefd) != 0) die("pipe");
    pid_t child = fork();
    if (child < 0) die("fork");
    if (child == 0) {
        if (close(pipefd[1]) != 0) _exit(91);
        char byte;
        ssize_t count;
        do { count = read(pipefd[0], &byte, 1); } while (count < 0 && errno == EINTR);
        if (count != 0 || close(pipefd[0]) != 0) _exit(92);
        _exit(0);
    }
    if (close(pipefd[0]) != 0) die("close");
    char ready[128];
    if (snprintf(ready, sizeof(ready), "READY %d %ld %d %d", getpid(), syscall(SYS_gettid), child, worker_count + 1) < 0) die("format");
    line(ready);
    char command;
    ssize_t count;
    while ((count = read(STDIN_FILENO, &command, 1)) != 0) {
        if (count < 0) { if (errno == EINTR) continue; die("stdin"); }
        if (command == 'Q') break;
        if (command != 'L') die("command");
        check(pthread_mutex_lock(&lock), "lock");
        leader_exit = 1;
        check(pthread_cond_broadcast(&changed), "broadcast");
        check(pthread_mutex_unlock(&lock), "unlock");
        line("LEADER_EXIT_REQUESTED");
    }
    if (close(pipefd[1]) != 0) die("close");
    int status;
    pid_t waited;
    do { waited = waitpid(child, &status, 0); } while (waited < 0 && errno == EINTR);
    if (waited != child || !WIFEXITED(status) || WEXITSTATUS(status) != 0) die("child wait");
    release_workers();
    for (int i = 1; i < worker_count; ++i) check(pthread_join(workers[i], NULL), "join");
    line("DONE");
    return NULL;
}
int main(int argc, char **argv) {
    if (argc > 1 && strcmp(argv[1], "pidfd-race") == 0) return pidfd_race(argc, argv);
    if (argc != 3) return 93;
    lifetime = strcmp(argv[1], "lifetime") == 0;
    if (!lifetime && strcmp(argv[1], "hold") != 0) return 94;
    int threads = atoi(argv[2]);
    if (threads < 1 || threads > 128 || (lifetime && threads < 2)) return 95;
    worker_count = threads - 1;
    if (prctl(PR_SET_PDEATHSIG, SIGKILL) != 0) die("pdeathsig");
    for (int i = lifetime ? 1 : 0; i < worker_count; ++i) check(pthread_create(&workers[i], NULL, sleeper, NULL), "create");
    if (lifetime) {
        check(pthread_create(&workers[0], NULL, controller, NULL), "create");
        check(pthread_mutex_lock(&lock), "lock");
        while (!leader_exit && !stop) check(pthread_cond_wait(&changed, &lock), "wait");
        check(pthread_mutex_unlock(&lock), "unlock");
        pthread_exit(NULL);
    }
    char ready[128];
    if (snprintf(ready, sizeof(ready), "READY %d 0 0 %d", getpid(), threads) < 0) die("format");
    line(ready);
    char command;
    ssize_t count;
    do { count = read(STDIN_FILENO, &command, 1); } while (count < 0 && errno == EINTR);
    if (count < 0 || (count > 0 && command != 'Q')) die("stdin");
    release_workers();
    for (int i = 0; i < worker_count; ++i) check(pthread_join(workers[i], NULL), "join");
    line("DONE");
    return 0;
}

// This mode records a finite experiment, not a guard or an absence decision.
static int64_t race_now(void) {
    struct timespec value;
    if (clock_gettime(CLOCK_MONOTONIC, &value) != 0) return -1;
    return (int64_t)value.tv_sec * 1000000000LL + value.tv_nsec;
}
static int race_close(int *fd) {
    if (*fd < 0) return 0;
    int owned = *fd;
    *fd = -1;
    return close(owned);
}
static int race_byte(int fd, int64_t deadline) {
    for (;;) {
        int64_t left = deadline - race_now();
        if (left <= 0) return -1;
        struct pollfd item = {.fd = fd, .events = POLLIN};
        int result = poll(&item, 1, (int)((left + 999999) / 1000000));
        if (result < 0 && errno == EINTR) continue;
        if (result <= 0) return -1;
        char byte;
        ssize_t count = read(fd, &byte, 1);
        if (count < 0 && errno == EINTR) continue;
        return count == 1 ? 0 : -1;
    }
}
static int race_parent(void) {
    pid_t before = getppid();
    if (before <= 1 || prctl(PR_SET_PDEATHSIG, SIGKILL) != 0 || getppid() != before) return -1;
    return 0;
}
static int race_cancelled(void) {
    struct pollfd item = {.fd = STDIN_FILENO, .events = POLLIN};
    int result = poll(&item, 1, 0);
    return result < 0 ? errno != EINTR : result > 0;
}
static int race_sample(int anchor, int attempt, const char *phase, int64_t deadline) {
    int failed = 0;
    int samples = 0;
    for (int kind = 0; kind < 2 && race_now() < deadline; ++kind) {
        samples++;
        const char *name = kind ? "status" : "stat";
        int fd = openat(anchor, name, O_RDONLY | O_CLOEXEC);
        int saved = fd < 0 ? errno : 0;
        char data[8193];
        ssize_t count = -1;
        if (fd >= 0) {
            count = read(fd, data, sizeof(data) - 1);
            saved = count < 0 ? errno : 0;
            if (count >= 0) {
                char tail;
                ssize_t after = read(fd, &tail, 1);
                if (after != 0) saved = after < 0 ? errno : EOVERFLOW;
            }
        }
        int close_error = race_close(&fd) == 0 ? 0 : errno;
        if (saved || close_error || count <= 0 || count == (ssize_t)sizeof(data) - 1) failed = 1;
        if (count < 0) count = 0;
        data[count] = 0;
        if (kind) {
            // Emit only identity vectors; preserve missing fields as missing.
            char selected[1024] = "";
            int fields = 0;
            char *line_at = data;
            while (*line_at) {
                char *end = strchr(line_at, '\n');
                if (end) *end = 0;
                if (strncmp(line_at, "Tgid:", 5) == 0 || strncmp(line_at, "NStgid:", 7) == 0 || strncmp(line_at, "NSpid:", 6) == 0) {
                    fields++;
                    size_t used = strlen(selected);
                    size_t length = strlen(line_at);
                    if (length + used + 2 > sizeof(selected)) {
                        failed = 1;
                    } else {
                        memcpy(selected + used, line_at, length);
                        selected[used + length] = '|';
                        selected[used + length + 1] = 0;
                    }
                }
                if (!end) break;
                line_at = end + 1;
            }
            snprintf(data, sizeof(data), "%s", selected);
            if (fields != 3) failed = 1;
        } else {
            char *at = strrchr(data, ')');
            unsigned long long birth = 0;
            if (at) {
                at++;
                for (int field = 3; field <= 22; ++field) {
                    while (*at == ' ') at++;
                    if (!*at) break;
                    if (field == 22) {
                        char *end;
                        birth = strtoull(at, &end, 10);
                        if (end == at || (*end != ' ' && *end != '\n')) birth = 0;
                        break;
                    }
                    while (*at && *at != ' ') at++;
                }
            }
            if (!birth) failed = 1;
        }
        for (char *at = data; *at; ++at) if (*at == '\n' || *at == '\r') *at = '|';
        printf("SAMPLE attempt=%d phase=%s kind=%s error=%d close_error=%d bytes=%ld data=%s\n", attempt, phase, name, saved, close_error, (long)count, data);
    }
    return failed || samples != 2;
}
struct race_opening {
    pthread_mutex_t mutex;
    pthread_cond_t changed;
    int ready, go, attempt, anchor, failed, calls, einval;
    pid_t pid;
    int64_t deadline;
};
static void *race_opener(void *argument) {
    struct race_opening *value = argument;
    int result = pthread_mutex_lock(&value->mutex);
    if (result != 0) { value->failed = result; return NULL; }
    value->ready = 1;
    result = pthread_cond_broadcast(&value->changed);
    while (result == 0 && !value->go) result = pthread_cond_wait(&value->changed, &value->mutex);
    int unlock_error = pthread_mutex_unlock(&value->mutex);
    if (result != 0 || unlock_error != 0) { value->failed = result ? result : unlock_error; return NULL; }
    for (int call = 0; call < 32 && race_now() < value->deadline; ++call) {
        if (race_cancelled()) { value->failed = ECANCELED; break; }
        int64_t at = race_now();
        int fd = syscall(SYS_pidfd_open, value->pid, 0);
        int saved = fd < 0 ? errno : 0;
        int returned = fd;
        int close_error = race_close(&fd) == 0 ? 0 : errno;
        printf("CALL attempt=%d call=%d mono=%lld pid=%d flags=0 result=%d errno=%d close_error=%d\n", value->attempt, call, (long long)at, value->pid, returned, saved, close_error);
        value->calls++;
        if (close_error || (returned < 0 && saved != ESRCH && saved != EINVAL)) value->failed = close_error ? close_error : saved;
        if (returned < 0 && saved == EINVAL) {
            value->einval++;
            // TODO: Record deadline-skipped/partial sample availability and completion/error status explicitly, without extending the deadline or requiring post-read success.
            if (value->einval == 1) (void)race_sample(value->anchor, value->attempt, "post-EINVAL", value->deadline);
        }
        if (value->failed) break;
    }
    return NULL;
}
struct race_control {
    pthread_mutex_t mutex;
    pthread_cond_t changed;
    int ready, stop;
    pid_t tid;
};
static void *race_worker(void *argument) {
    struct race_control *value = argument;
    check(pthread_mutex_lock(&value->mutex), "control lock");
    value->tid = syscall(SYS_gettid);
    value->ready = 1;
    check(pthread_cond_broadcast(&value->changed), "control ready");
    while (!value->stop) check(pthread_cond_wait(&value->changed, &value->mutex), "control wait");
    check(pthread_mutex_unlock(&value->mutex), "control unlock");
    return NULL;
}
static int race_wait(pid_t *child, int pin, int64_t deadline, int *status) {
    while (*child > 0 && race_now() < deadline) {
        pid_t waited = waitpid(*child, status, WNOHANG);
        int saved = waited < 0 ? errno : 0;
        if (waited == *child) { *child = -1; return 0; }
        if (waited < 0 && saved != EINTR) {
            if (saved == ECHILD) *child = -1;
            return saved;
        }
        int64_t left = deadline - race_now();
        if (left <= 0) break;
        struct pollfd item = {.fd = pin, .events = POLLIN};
        int milliseconds = (int)((left + 999999) / 1000000);
        if (pin < 0 && milliseconds > 1) milliseconds = 1;
        int result = poll(&item, 1, milliseconds);
        if (result < 0 && errno != EINTR) return errno;
    }
    return ETIMEDOUT;
}
static int pidfd_race(int argc, char **argv) {
    if (argc != 3 || race_parent() != 0) return 96;
    char *end;
    int64_t deadline = strtoll(argv[2], &end, 10);
    int64_t now = race_now();
    if (*end || now < 0 || deadline <= now || deadline - now > 20000000000LL) return 97;
    if (setvbuf(stdout, NULL, _IOLBF, 0) != 0) return 98;
    struct race_control control = {.mutex = PTHREAD_MUTEX_INITIALIZER, .changed = PTHREAD_COND_INITIALIZER};
    pthread_t worker;
    check(pthread_create(&worker, NULL, race_worker, &control), "control worker");
    check(pthread_mutex_lock(&control.mutex), "main control lock");
    while (!control.ready) check(pthread_cond_wait(&control.changed, &control.mutex), "main control wait");
    printf("READY version=1 pid=%d leader=%ld worker=%d tgid=%d\n", getpid(), syscall(SYS_gettid), control.tid, getpid());
    int self_anchor = open("/proc/self", O_RDONLY | O_DIRECTORY | O_CLOEXEC);
    int self_error = self_anchor < 0 ? errno : 0;
    int failed = self_anchor < 0;
    if (self_anchor >= 0 && race_sample(self_anchor, -1, "ready", deadline)) failed = 1;
    printf("SELF_ANCHOR error=%d\n", self_error);
    if (race_close(&self_anchor) != 0) failed = 1;
    for (int kind = 0; kind < 2; ++kind) {
        pid_t pid = kind ? control.tid : getpid();
        int fd = syscall(SYS_pidfd_open, pid, 0);
        int saved = fd < 0 ? errno : 0;
        int returned = fd;
        int close_error = race_close(&fd) == 0 ? 0 : errno;
        printf("CONTROL kind=%s pid=%d tgid=%d flags=0 result=%d errno=%d close_error=%d\n", kind ? "nonleader" : "leader", pid, getpid(), returned, saved, close_error);
        if (close_error || (!kind && returned < 0) || (kind && (returned >= 0 || saved != EINVAL))) failed = 1;
    }
    control.stop = 1;
    check(pthread_cond_broadcast(&control.changed), "control release");
    check(pthread_mutex_unlock(&control.mutex), "main control unlock");
    check(pthread_join(worker, NULL), "control join");
    check(pthread_cond_destroy(&control.changed), "control condition destroy");
    check(pthread_mutex_destroy(&control.mutex), "control mutex destroy");
    int attempts = 0, calls = 0, einval = 0;
    for (int attempt = 0; attempt < 64 && !failed && race_now() < deadline; ++attempt) {
        if (race_cancelled()) { failed = 1; break; }
        int ready[2] = {-1, -1}, release[2] = {-1, -1}, anchor = -1, pin = -1;
        pid_t child = -1;
        int status = 0, opened = 0;
        pthread_t opener;
        struct race_opening value = {.mutex = PTHREAD_MUTEX_INITIALIZER, .changed = PTHREAD_COND_INITIALIZER, .attempt = attempt, .deadline = deadline};
        if (pipe2(ready, O_CLOEXEC) != 0 || pipe2(release, O_CLOEXEC) != 0) { failed = 1; goto attempt_cleanup; }
        child = fork();
        if (child < 0) { failed = 1; goto attempt_cleanup; }
        if (child == 0) {
            if (race_parent() != 0 || race_close(&ready[0]) != 0 || race_close(&release[1]) != 0 || write(ready[1], "R", 1) != 1 || race_close(&ready[1]) != 0) _exit(91);
            int result = race_byte(release[0], deadline);
            if (race_close(&release[0]) != 0) _exit(92);
            _exit(result == 0 ? 0 : 93);
        }
        if (race_close(&ready[1]) != 0 || race_close(&release[0]) != 0 || race_byte(ready[0], deadline) != 0 || race_close(&ready[0]) != 0) { failed = 1; goto attempt_cleanup; }
        char path[64];
        snprintf(path, sizeof(path), "/proc/%d", child);
        anchor = open(path, O_RDONLY | O_DIRECTORY | O_CLOEXEC);
        if (anchor < 0) { failed = 1; goto attempt_cleanup; }
        pin = syscall(SYS_pidfd_open, child, 0);
        printf("CHILD_PIN attempt=%d pid=%d result=%d errno=%d\n", attempt, child, pin, pin < 0 ? errno : 0);
        if (pin < 0) { failed = 1; goto attempt_cleanup; }
        if (race_sample(anchor, attempt, "before-release", deadline)) { failed = 1; goto attempt_cleanup; }
        value.pid = child;
        value.anchor = anchor;
        int create_error = pthread_create(&opener, NULL, race_opener, &value);
        if (create_error) { failed = 1; goto attempt_cleanup; }
        opened = 1;
        check(pthread_mutex_lock(&value.mutex), "opener lock");
        while (!value.ready) check(pthread_cond_wait(&value.changed, &value.mutex), "opener ready");
        printf("OPENER_READY attempt=%d pid=%d\n", attempt, child);
        value.go = 1;
        check(pthread_cond_broadcast(&value.changed), "opener go");
        check(pthread_mutex_unlock(&value.mutex), "opener unlock");
        printf("RELEASE attempt=%d mono=%lld pid=%d\n", attempt, (long long)race_now(), child);
        if (write(release[1], "X", 1) != 1 || race_close(&release[1]) != 0) failed = 1;
        pid_t expected = child;
        printf("WAIT_ENTER attempt=%d mono=%lld pid=%d\n", attempt, (long long)race_now(), expected);
        int wait_error = race_wait(&child, pin, deadline, &status);
        printf("WAIT_RETURN attempt=%d mono=%lld pid=%d consumed=%d status=%d error=%d\n", attempt, (long long)race_now(), expected, child < 0, status, wait_error);
        if (wait_error || !WIFEXITED(status) || WEXITSTATUS(status) != 0) failed = 1;
attempt_cleanup:
        if (child > 0) {
            // Only this main thread's still-unreaped direct child is signalable here.
            if (kill(child, SIGKILL) != 0 && errno != ESRCH) failed = 1;
            int64_t cleanup = race_now() + 10000000000LL;
            if (cleanup > deadline + 10000000000LL) cleanup = deadline + 10000000000LL;
            if (race_wait(&child, pin, cleanup, &status) != 0) failed = 1;
        }
        if (opened) {
            check(pthread_join(opener, NULL), "opener join");
            calls += value.calls;
            einval += value.einval;
            if (value.failed) failed = 1;
        }
        if (race_close(&ready[0]) != 0) failed = 1;
        if (race_close(&ready[1]) != 0) failed = 1;
        if (race_close(&release[0]) != 0) failed = 1;
        if (race_close(&release[1]) != 0) failed = 1;
        if (race_close(&pin) != 0) failed = 1;
        if (race_close(&anchor) != 0) failed = 1;
        check(pthread_cond_destroy(&value.changed), "opener condition destroy");
        check(pthread_mutex_destroy(&value.mutex), "opener mutex destroy");
        attempts++;
        printf("ATTEMPT_DONE attempt=%d calls=%d race_einval=%d failed=%d child_owned=%d\n", attempt, value.calls, value.einval, failed, child > 0);
    }
    printf("DONE attempts=%d calls=%d race_einval=%d failed=%d\n", attempts, calls, einval, failed);
    return failed ? 99 : 0;
}
`

// numericHintNative retains the test's single wait owner and bounded protocol pipes.
//
// Example: Finish releases workers and consumes exactly one Cmd.Wait result.
type numericHintNative struct {
	Command     *exec.Cmd
	Input       io.WriteCloser
	Output      *bufio.Reader
	OutputFile  *os.File
	Diagnostics bytes.Buffer
	PID         int
	Worker      int
	Child       int
	Threads     int
	Waited      bool
}

// compileNumericHintNative builds only the controlled native fixture in disposable storage.
//
// Example: fixed-read and leader-lifetime controls share the same fixture executable.
func compileNumericHintNative(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "numeric-hint-native")
	cmd := exec.Command("cc", "-O2", "-Wall", "-Wextra", "-Werror", "-pthread", "-x", "c", "-", "-o", binary)
	cmd.Stdin = strings.NewReader(numericHintNativeSource)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native fixture compile: %v: %s", err, output)
	}
	return binary
}

// pidfdRaceClock reads the shared native monotonic time used by this experiment.
//
// Example: compiler setup cannot restart the enclosing family's budget.
func pidfdRaceClock(t *testing.T) int64 {
	t.Helper()
	var now unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &now); err != nil {
		t.Fatal(err)
	}
	return now.Nano()
}

// TestNumericHintPIDFDRace records bounded native controls and exit/reap attempts.
// It does not classify an unexpected pidfd error as absence or establish its cause.
//
// Example: GUARD_PIDFD_RACE=1 enables one externally budgeted investigation.
func TestNumericHintPIDFDRace(t *testing.T) {
	if os.Getenv("GUARD_PIDFD_RACE") != "1" {
		t.Skip("set GUARD_PIDFD_RACE=1 within the selected finite family")
	}
	start, err := strconv.ParseInt(os.Getenv("GUARD_PIDFD_RACE_START_NS"), 10, 64)
	if err != nil || start <= 0 {
		t.Fatalf("missing original monotonic family start: %v", err)
	}
	now := pidfdRaceClock(t)
	if start > now || now-start >= int64(50*time.Second) {
		t.Fatal("future or expired original monotonic family start")
	}
	proof := os.Getenv("GUARD_PIDFD_RACE_ARTIFACT_DIR")
	if !filepath.IsAbs(proof) {
		t.Fatal("existing absolute artifact directory required")
	}
	if os.Getenv("GUARD_PIDFD_RACE_CHILD") == "1" {
		runPIDFDRaceNative(t, start, proof)
		return
	}
	guard := os.Getenv("GUARD_PIDFD_RACE_GUARD")
	if !filepath.IsAbs(guard) {
		t.Fatal("qualified prebuilt guard required")
	}
	remaining := time.Duration(start + int64(20*time.Second) - pidfdRaceClock(t))
	if remaining <= time.Second {
		t.Fatal("combined compilation budget exhausted before C compilation")
	}
	binary := filepath.Join(t.TempDir(), "numeric-hint-pidfd-race")
	receiptPath := filepath.Join(proof, "c-compile.json")
	compile := exec.Command(guard, "-deadline", (remaining - 500*time.Millisecond).String(), "-grace", "100ms", "-receipt", receiptPath, "--", "cc", "-O2", "-Wall", "-Wextra", "-Werror", "-pthread", "-x", "c", "-", "-o", binary)
	compile.Stdin = strings.NewReader(numericHintNativeSource)
	output, compileErr := compile.CombinedOutput()
	if err := os.WriteFile(filepath.Join(proof, "c-compile.log"), output, 0600); err != nil {
		t.Fatal(err)
	}
	data, receiptErr := os.ReadFile(receiptPath)
	var receipt Receipt
	if receiptErr == nil {
		receiptErr = json.Unmarshal(data, &receipt)
	}
	if compileErr != nil || receiptErr != nil || !receipt.HostChildrenSettled || len(receipt.Survivors) != 0 || len(receipt.Errors) != 0 || receipt.CommandExit != 0 || receipt.Cause != "command exited" || pidfdRaceClock(t) >= start+int64(20*time.Second) {
		t.Fatalf("C compiler not settled within original compilation stage: compile=%v receipt=%v state=%+v output=%s", compileErr, receiptErr, receipt, output)
	}
	fixture, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proof, "fixture.sha256"), []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(fixture), binary)), 0600); err != nil {
		t.Fatal(err)
	}
	traceLog, err := os.OpenFile(filepath.Join(proof, "traced-test.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	// strace owns the child Go test; that test starts and waits for the C helper directly.
	trace := exec.Command("strace", "-f", "-qq", "-ttt", "-T", "-s", "256", "-o", filepath.Join(proof, "native.trace"), "-e", "trace=pidfd_open,clone,clone3,wait4,waitid,exit,exit_group", os.Args[0], "-test.run=^TestNumericHintPIDFDRace$", "-test.count=1", "-test.timeout=35s", "-test.v")
	trace.Env = append(os.Environ(), "GUARD_PIDFD_RACE_CHILD=1", "GUARD_PIDFD_RACE_BINARY="+binary)
	trace.Stdout, trace.Stderr = traceLog, traceLog
	traceErr := trace.Run()
	closeErr := traceLog.Close()
	if traceErr != nil || closeErr != nil {
		t.Fatalf("traced child test failed: %v", errors.Join(traceErr, closeErr))
	}
	t.Logf("experiment evidence=%s; success/ESRCH-only is inconclusive; race EINVAL cannot identify the internal selected PID lifetime", proof)
}

// runPIDFDRaceNative retains direct Cmd ownership while draining bounded records.
//
// Example: parsing failure kills only the still-unreaped direct helper, then consumes its Wait.
func runPIDFDRaceNative(
	t *testing.T,
	start int64,
	proof string,
) {
	t.Helper()
	now := pidfdRaceClock(t)
	deadline := min(now+int64(20*time.Second), start+int64(40*time.Second))
	if deadline <= now || start+int64(50*time.Second)-deadline < int64(10*time.Second) {
		t.Fatal("no native phase with original cleanup reserve")
	}
	binary := os.Getenv("GUARD_PIDFD_RACE_BINARY")
	if !filepath.IsAbs(binary) {
		t.Fatal("fresh C fixture required")
	}
	transcript, err := os.OpenFile(filepath.Join(proof, "native-transcript.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := transcript.Close(); err != nil {
			t.Error(err)
		}
	}()
	command := exec.Command(binary, "pidfd-race", strconv.FormatInt(deadline, 10))
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(errors.Join(err, input.Close()))
	}
	if err := command.Start(); err != nil {
		t.Fatal(errors.Join(err, input.Close(), output.Close()))
	}
	waited := false
	t.Cleanup(func() {
		if waited {
			return
		}
		// Wait has not been called: this is still our exact unreaped direct root.
		_, requestErr := io.WriteString(input, "Q")
		inputErr := input.Close()
		outputErr := output.Close()
		killErr := command.Process.Kill()
		if errors.Is(killErr, os.ErrProcessDone) || errors.Is(killErr, unix.ESRCH) {
			killErr = nil
		}
		waited = true
		waitErr := command.Wait()
		t.Errorf("incomplete helper protocol; cleanup=%v diagnostics=%s", errors.Join(requestErr, inputErr, outputErr, killErr, waitErr), diagnostics.String())
	})
	file, ok := output.(*os.File)
	if !ok {
		t.Fatal("native stdout is not an owned file")
	}
	if err := file.SetReadDeadline(time.Now().Add(time.Duration(start + int64(50*time.Second) - pidfdRaceClock(t)))); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 16384)
	ready, controls, calls, attempts, done, records, totalBytes := false, 0, 0, 0, false, 0, 0
	activePID, attemptCalls, raceErrors := 0, 0, 0
	consumed := false
	for scanner.Scan() {
		line := scanner.Text()
		records++
		totalBytes += len(line) + 1
		if records > 2700 || totalBytes > 1024*1024 {
			t.Fatal("native transcript exceeded finite record/byte bound")
		}
		if _, err := fmt.Fprintln(transcript, line); err != nil {
			t.Fatal(err)
		}
		switch {
		case strings.HasPrefix(line, "READY "):
			var pid, leader, worker, tgid int
			count, err := fmt.Sscanf(line, "READY version=1 pid=%d leader=%d worker=%d tgid=%d", &pid, &leader, &worker, &tgid)
			if ready || err != nil || count != 4 || pid != command.Process.Pid || leader != pid || tgid != pid || worker <= 0 || worker == pid {
				t.Fatalf("invalid direct helper identity: %q err=%v", line, err)
			}
			ready = true
		case strings.HasPrefix(line, "CONTROL "):
			var kind string
			var pid, tgid, flags, result, errno, closeError int
			count, err := fmt.Sscanf(line, "CONTROL kind=%s pid=%d tgid=%d flags=%d result=%d errno=%d close_error=%d", &kind, &pid, &tgid, &flags, &result, &errno, &closeError)
			valid := kind == "leader" && pid == tgid && result >= 0 && errno == 0 || kind == "nonleader" && pid != tgid && result < 0 && errno == int(unix.EINVAL)
			if !ready || count != 7 || err != nil || flags != 0 || tgid != command.Process.Pid || closeError != 0 || !valid {
				t.Fatalf("unexpected native control: %q err=%v", line, err)
			}
			controls++
		case strings.HasPrefix(line, "CALL "):
			var attempt, call, pid, flags, result, errno, closeError int
			var at int64
			count, err := fmt.Sscanf(line, "CALL attempt=%d call=%d mono=%d pid=%d flags=%d result=%d errno=%d close_error=%d", &attempt, &call, &at, &pid, &flags, &result, &errno, &closeError)
			valid := result >= 0 && errno == 0 || result < 0 && (errno == int(unix.ESRCH) || errno == int(unix.EINVAL))
			if count != 8 || err != nil || attempt != attempts || call != attemptCalls || call >= 32 || pid != activePID || pid <= 0 || flags != 0 || at <= 0 || closeError != 0 || !valid {
				t.Fatalf("invalid native call record: %q err=%v", line, err)
			}
			attemptCalls++
			if errno == int(unix.EINVAL) {
				raceErrors++
			}
			calls++
			if controls != 2 || calls > 2048 {
				t.Fatalf("race call outside finite controls: %q", line)
			}
		case strings.HasPrefix(line, "ATTEMPT_DONE "):
			if !consumed {
				t.Fatalf("attempt has no successful consuming wait: %q", line)
			}
			attempts++
			activePID, attemptCalls, consumed = 0, 0, false
			if attempts > 64 || !strings.HasSuffix(line, "failed=0 child_owned=0") {
				t.Fatalf("unjoined/failed attempt: %q", line)
			}
		case strings.HasPrefix(line, "DONE "):
			var reportedAttempts, reportedCalls, raceEINVAL, failed int
			count, err := fmt.Sscanf(line, "DONE attempts=%d calls=%d race_einval=%d failed=%d", &reportedAttempts, &reportedCalls, &raceEINVAL, &failed)
			if done || err != nil || count != 4 || failed != 0 || reportedAttempts != attempts || reportedCalls != calls || attempts == 0 || raceEINVAL != raceErrors {
				t.Fatalf("invalid final native counts: %q err=%v", line, err)
			}
			done = true
			t.Logf("native attempts=%d calls=%d race_EINVAL=%d; control EINVAL excluded; internal kernel interleaving remains unproved", attempts, calls, raceEINVAL)
		case strings.HasPrefix(line, "CHILD_PIN "):
			var attempt, pid, result, errno int
			count, err := fmt.Sscanf(line, "CHILD_PIN attempt=%d pid=%d result=%d errno=%d", &attempt, &pid, &result, &errno)
			if count != 4 || err != nil || attempt != attempts || pid <= 0 || result < 0 || errno != 0 || activePID != 0 {
				t.Fatalf("invalid controlled child pin: %q err=%v", line, err)
			}
			activePID = pid
		case strings.HasPrefix(line, "WAIT_RETURN "):
			var attempt, pid, didConsume, status, waitError int
			var at int64
			count, err := fmt.Sscanf(line, "WAIT_RETURN attempt=%d mono=%d pid=%d consumed=%d status=%d error=%d", &attempt, &at, &pid, &didConsume, &status, &waitError)
			if count != 6 || err != nil || attempt != attempts || pid != activePID || didConsume != 1 || status != 0 || waitError != 0 || consumed {
				t.Fatalf("invalid consuming native wait: %q err=%v", line, err)
			}
			consumed = true
		case strings.HasPrefix(line, "SAMPLE "), strings.HasPrefix(line, "SELF_ANCHOR "), strings.HasPrefix(line, "OPENER_READY "), strings.HasPrefix(line, "RELEASE "), strings.HasPrefix(line, "WAIT_ENTER "):
		default:
			t.Fatalf("unexpected native record: %q", line)
		}
	}
	if err := scanner.Err(); err != nil || !ready || controls != 2 || !done {
		t.Fatalf("incomplete bounded native transcript: ready=%t controls=%d done=%t err=%v", ready, controls, done, err)
	}
	inputErr := input.Close()
	waited = true
	waitErr := command.Wait()
	if inputErr != nil || waitErr != nil || diagnostics.Len() != 0 {
		t.Fatalf("native helper join: %v diagnostics=%s", errors.Join(inputErr, waitErr), diagnostics.String())
	}
	t.Logf("direct helper pid=%d joined state=%s elapsed_family_seconds=%.6f", command.Process.Pid, command.ProcessState, float64(pidfdRaceClock(t)-start)/float64(time.Second))
}

// startNumericHintNative starts a private process group and parses its completed ready event.
//
// Example: a lifetime fixture reports the nonleader TID and its child PID.
func startNumericHintNative(
	t *testing.T,
	binary string,
	mode string,
	threads int,
) *numericHintNative {
	t.Helper()
	native := &numericHintNative{}
	native.Command = exec.Command(binary, mode, strconv.Itoa(threads))
	native.Command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	native.Command.Stderr = &native.Diagnostics
	var err error
	native.Input, err = native.Command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := native.Command.StdoutPipe()
	if err != nil {
		t.Fatal(errors.Join(err, native.Input.Close()))
	}
	var ok bool
	native.OutputFile, ok = output.(*os.File)
	if !ok {
		t.Fatal(errors.Join(errors.New("stdout pipe is not an os.File"), native.Input.Close(), output.Close()))
	}
	native.Output = bufio.NewReader(native.OutputFile)
	if err := native.Command.Start(); err != nil {
		t.Fatal(errors.Join(err, native.Input.Close(), output.Close()))
	}
	// The same Finish method owns Wait during ordinary completion and assertion cleanup.
	//
	// Example: a failed metadata assertion still releases the fixture's native child.
	t.Cleanup(func() {
		if !native.Waited {
			if err := native.Finish(); err != nil {
				t.Errorf("native assertion cleanup: %v", err)
			}
		}
	})
	line := native.Line(t)
	count, err := fmt.Sscanf(line, "READY %d %d %d %d\n", &native.PID, &native.Worker, &native.Child, &native.Threads)
	if err != nil || count != 4 || native.PID != native.Command.Process.Pid || native.Threads != threads {
		t.Fatalf("invalid native ready event %q: count=%d err=%v", line, count, err)
	}
	tasks, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", native.PID))
	if err != nil || len(tasks) != threads {
		t.Fatalf("native thread count: requested=%d actual=%d err=%v", threads, len(tasks), err)
	}
	return native
}

// Line consumes one event with an upper bound on the opaque native fixture wait.
//
// Example: READY is received before metadata reads begin.
func (n *numericHintNative) Line(t *testing.T) string {
	t.Helper()
	if err := n.OutputFile.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	line, err := n.Output.ReadString('\n')
	if err != nil {
		t.Fatalf("native event read: %v: %q", err, line)
	}
	return line
}

// Send writes a single completed native control event.
//
// Example: L lets the leader exit while workers keep the process alive.
func (n *numericHintNative) Send(
	t *testing.T,
	command string,
) {
	t.Helper()
	count, err := io.WriteString(n.Input, command)
	if err != nil || count != len(command) {
		t.Fatalf("native command write: count=%d err=%v", count, err)
	}
}

// Finish releases native descendants and owns the only consuming root wait.
//
// Example: no native fixture remains when a guard constructor checks childlessness.
func (n *numericHintNative) Finish() error {
	count, sendErr := io.WriteString(n.Input, "Q")
	inputCloseErr := n.Input.Close()
	deadlineErr := n.OutputFile.SetReadDeadline(time.Now().Add(10 * time.Second))
	var line string
	var readErr error
	if deadlineErr == nil {
		line, readErr = n.Output.ReadString('\n')
	}
	var killErr error
	if sendErr != nil || count != 1 || deadlineErr != nil || readErr != nil || line != "DONE\n" {
		// This group was created by this Cmd with Setpgid and is never shared with the host workload.
		killErr = unix.Kill(-n.Command.Process.Pid, syscall.SIGKILL)
		if errors.Is(killErr, unix.ESRCH) {
			killErr = nil
		}
	}
	outputCloseErr := n.OutputFile.Close()
	n.Waited = true
	waitErr := n.Command.Wait()
	var protocolErr error
	if count != 1 || line != "DONE\n" {
		protocolErr = fmt.Errorf("native completion: count=%d event=%q", count, line)
	}
	var diagnosticErr error
	if n.Diagnostics.Len() != 0 {
		diagnosticErr = fmt.Errorf("native fixture diagnostics: %s", n.Diagnostics.String())
	}
	return errors.Join(sendErr, inputCloseErr, deadlineErr, readErr, outputCloseErr, killErr, waitErr, protocolErr, diagnosticErr)
}

// TestNumericHintNativeCleanupFailure proves a lost protocol reader cannot bypass the sole native wait.
//
// Example: closing the test's stdout pipe forces checked private-group cleanup.
func TestNumericHintNativeCleanupFailure(t *testing.T) {
	if os.Getenv("GUARD_HINT_NATIVE") != "1" {
		t.Skip("set GUARD_HINT_NATIVE=1 for a forced native cleanup failure")
	}
	native := startNumericHintNative(t, compileNumericHintNative(t), "hold", 1)
	if err := native.OutputFile.Close(); err != nil {
		t.Fatal(err)
	}
	err := native.Finish()
	if !errors.Is(err, os.ErrClosed) || !native.Waited || native.Command.ProcessState == nil {
		t.Fatalf("closed protocol pipe bypassed checked wait: waited=%t state=%v err=%v", native.Waited, native.Command.ProcessState, err)
	}
	if _, readErr := readProcess(native.PID); !errors.Is(readErr, unix.ENOENT) && !errors.Is(readErr, unix.ESRCH) {
		t.Fatalf("failed protocol left controlled fixture visible: %v", readErr)
	}
	t.Logf("forced_cleanup pid=%d waited=%t process_state=%s retained_error=%v", native.PID, native.Waited, native.Command.ProcessState, err)
}

// TestNumericHintNativeGroupLifetime compares used fields and actual group readiness across leader exit.
//
// Example: GUARD_HINT_NATIVE=1 go test -run '^TestNumericHintNativeGroupLifetime$'.
func TestNumericHintNativeGroupLifetime(t *testing.T) {
	if os.Getenv("GUARD_HINT_NATIVE") != "1" {
		t.Skip("set GUARD_HINT_NATIVE=1 for controlled native thread lifetime checks")
	}
	native := startNumericHintNative(t, compileNumericHintNative(t), "lifetime", 8)
	if native.Worker == native.PID || native.Worker <= 0 || native.Child <= 0 {
		t.Fatalf("child was not created by a nonleader: %+v", native)
	}
	group, err := readProcess(native.PID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := readNumericProcess(fmt.Sprintf("/proc/%d/task/%d/stat", native.PID, native.PID), native.PID)
	if err != nil || task.ProcessIdentity != group.ProcessIdentity || task.Parent != group.Parent || group.Parent != os.Getpid() || group.Birth == 0 {
		t.Fatalf("leader field mismatch: group=%+v task=%+v err=%v", group, task, err)
	}
	child, err := readProcess(native.Child)
	if err != nil || child.Parent != native.PID || child.Parent == native.Worker {
		t.Fatalf("nonleader child parent is not TGID: child=%+v worker=%d err=%v", child, native.Worker, err)
	}
	fd, err := unix.PidfdOpen(native.PID, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The held group pidfd has exactly one close owner independent of native Wait.
	//
	// Example: leader zombie state never changes this descriptor's group identity.
	defer func() {
		if err := unix.Close(fd); err != nil {
			t.Errorf("native group pidfd close: %v", err)
		}
	}()
	native.Send(t, "L")
	if line := native.Line(t); line != "LEADER_EXIT_REQUESTED\n" {
		t.Fatalf("leader event %q", line)
	}
	until := time.Now().Add(5 * time.Second)
	for {
		group, err = readProcess(native.PID)
		if err != nil {
			t.Fatal(err)
		}
		if group.State == "Z" {
			break
		}
		if !time.Now().Before(until) {
			t.Fatalf("leader did not exit: %+v", group)
		}
		time.Sleep(time.Millisecond)
	}
	task, taskErr := readNumericProcess(fmt.Sprintf("/proc/%d/task/%d/stat", native.PID, native.PID), native.PID)
	hint, hintErr := (linuxKernel{}).Hint(native.PID)
	if hintErr != nil || hint.ProcessIdentity != group.ProcessIdentity || hint.Parent != group.Parent || hint.State != "Z" {
		t.Fatalf("leader-exit hint differs from original group metadata: group=%+v task=%+v taskErr=%v hint=%+v hintErr=%v", group, task, taskErr, hint, hintErr)
	}
	if taskErr == nil && (task.ProcessIdentity != group.ProcessIdentity || task.Parent != group.Parent || task.State != "Z") {
		t.Fatalf("stable exited leader task fields differ: group=%+v task=%+v", group, task)
	}
	state, err := (linuxKernel{}).Poll(fd)
	if err != nil || state != processRunning {
		t.Fatalf("leader exit incorrectly ended group lifetime: state=%d err=%v", state, err)
	}
	t.Logf("native leader_exit group=%+v task=%+v task_error=%v hint=%+v pidfd_running=%t worker=%d child=%d", group, task, taskErr, hint, state == processRunning, native.Worker, native.Child)
	if err := native.Finish(); err != nil {
		t.Fatal(err)
	}
	state, err = (linuxKernel{}).Poll(fd)
	if err != nil || state != processExited {
		t.Fatalf("last worker exit did not end group lifetime: state=%d err=%v", state, err)
	}
	if _, err := readProcess(native.Child); !errors.Is(err, unix.ENOENT) && !errors.Is(err, unix.ESRCH) {
		t.Fatalf("controlled nonleader child not joined: %v", err)
	}
}

// TestNumericHintFixedReads measures identical completed read counts without discovery scheduling.
//
// Example: GUARD_HINT_NATIVE=1 compares group/task views for one and 64 native threads.
func TestNumericHintFixedReads(t *testing.T) {
	if os.Getenv("GUARD_HINT_NATIVE") != "1" {
		t.Skip("set GUARD_HINT_NATIVE=1 for fixed-count native stat comparisons")
	}
	binary := compileNumericHintNative(t)
	for _, threads := range []int{1, 64} {
		native := startNumericHintNative(t, binary, "hold", threads)
		for _, view := range []string{"group", "task", "task", "group"} {
			path := fmt.Sprintf("/proc/%d/stat", native.PID)
			if view == "task" {
				path = fmt.Sprintf("/proc/%d/task/%d/stat", native.PID, native.PID)
			}
			var before, after syscall.Rusage
			if err := syscall.Getrusage(syscall.RUSAGE_SELF, &before); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			completed := 0
			for completed < 65536 {
				stat, err := readNumericProcess(path, native.PID)
				if err != nil || stat.PID != native.PID || stat.Birth == 0 || stat.Parent != os.Getpid() || stat.State == "Z" {
					t.Fatalf("fixed read failed at %d: %+v err=%v", completed, stat, err)
				}
				completed++
			}
			wall := time.Since(started).Seconds()
			if err := syscall.Getrusage(syscall.RUSAGE_SELF, &after); err != nil {
				t.Fatal(err)
			}
			user := float64(after.Utime.Nano()-before.Utime.Nano()) / float64(time.Second)
			system := float64(after.Stime.Nano()-before.Stime.Nano()) / float64(time.Second)
			t.Logf("fixed_reads threads=%d view=%s completed=%d user_seconds=%.6f system_seconds=%.6f cpu_seconds=%.6f wall_seconds=%.9f", threads, view, completed, user, system, user+system, wall)
		}
		// A byte-sized real procfs read proves short positive reads must continue to EOF.
		path := fmt.Sprintf("/proc/%d/task/%d/stat", native.PID, native.PID)
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var data []byte
		var byteBuffer [1]byte
		reads := 0
		for {
			count, err := file.Read(byteBuffer[:])
			reads++
			data = append(data, byteBuffer[:count]...)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(errors.Join(err, file.Close()))
			}
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if stat, err := parseProcess(data, native.PID); err != nil || stat.Birth == 0 || reads != len(data)+1 {
			t.Fatalf("partial/EOF observation: bytes=%d reads=%d stat=%+v err=%v", len(data), reads, stat, err)
		}
		t.Logf("native_short_read threads=%d positive_reads=%d eof_reads=1 bytes=%d", threads, reads-1, len(data))
		if err := native.Finish(); err != nil {
			t.Fatal(err)
		}
	}
}

// logNumericHintHost records visible process/thread population outside the measured driver interval.
//
// Example: population and load qualify an alternating native CPU comparison.
func logNumericHintHost(t *testing.T) {
	t.Helper()
	entries, err := (linuxKernel{}).Entries()
	if err != nil {
		t.Fatal(err)
	}
	threads, misses := 0, 0
	for _, pid := range entries {
		tasks, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
		if err != nil {
			switch {
			case errors.Is(err, unix.ENOENT), errors.Is(err, unix.ESRCH), errors.Is(err, unix.EACCES), errors.Is(err, unix.EPERM):
				misses++
				continue
			default:
				t.Fatalf("host task population PID %d: %v", pid, err)
			}
		}
		threads += len(tasks)
	}
	load, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("host pid=%d go=%s cpus=%d processes=%d visible_threads=%d task_directory_misses=%d load=%s", os.Getpid(), runtime.Version(), runtime.NumCPU(), len(entries), threads, misses, strings.TrimSpace(string(load)))
}

// numericHintRouteCounts reports actual traced Hint routing, separately from sampled CPU.
//
// Example: an original absolute-path open proves a fallback attempt without reproducing its policy.
type numericHintRouteCounts struct {
	Calls, Successes, Errors, TaskSuccesses, TaskErrors, Fallbacks    int
	TaskOpens, GroupOpens, TaskEOF, GroupEOF, TaskCloses, GroupCloses int
}

// numericHintTraceResult parses a completed strace result and rejects incomplete observations.
//
// Example: a positive short read differs from the final zero-byte EOF read.
func numericHintTraceResult(line string) (int, error) {
	if strings.Contains(line, "<unfinished ...>") || strings.Contains(line, "resumed>") {
		return 0, fmt.Errorf("incomplete route observation: %s", line)
	}
	at := strings.LastIndex(line, " = ")
	if at < 0 {
		return 0, fmt.Errorf("missing route result: %s", line)
	}
	fields := strings.Fields(line[at+3:])
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty route result: %s", line)
	}
	// Successful -yy opens annotate the numeric descriptor with its resolved path.
	result := strings.Split(fields[0], "<")[0]
	return strconv.Atoi(result)
}

// numericHintTraceRoutes counts complete real routes delimited by single owner-thread writes.
//
// Example: success requires actual returned success plus positive data, EOF and checked close.
func numericHintTraceRoutes(trace []byte) (numericHintRouteCounts, error) {
	var total, current numericHintRouteCounts
	pid, owner := 0, ""
	taskPositive, groupPositive := 0, 0
	for _, line := range strings.Split(string(trace), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		begin := strings.Index(line, `"HINT_BEGIN `)
		end := strings.Index(line, `"HINT_END `)
		if strings.Contains(line, "write(1") && (begin >= 0 || end >= 0) {
			written, err := numericHintTraceResult(line)
			if err != nil || written <= 0 {
				return total, fmt.Errorf("marker not completed: %s: %v", line, err)
			}
			if begin >= 0 {
				if pid != 0 {
					return total, errors.New("overlapping Hint markers")
				}
				if count, err := fmt.Sscanf(line[begin+1:], "HINT_BEGIN %d", &pid); err != nil || count != 1 || pid <= 0 {
					return total, fmt.Errorf("invalid begin marker: %s", line)
				}
				owner = fields[0]
				current = numericHintRouteCounts{}
				taskPositive, groupPositive = 0, 0
				continue
			}
			var endedPID, success int
			if count, err := fmt.Sscanf(line[end+1:], "HINT_END %d %d", &endedPID, &success); err != nil || count != 2 || endedPID != pid || fields[0] != owner || (success != 0 && success != 1) {
				return total, fmt.Errorf("invalid end marker: %s", line)
			}
			if current.TaskOpens == 0 {
				return total, errors.New("Hint has no observed task attempt")
			}
			if success == 1 {
				switch {
				case current.GroupOpens != 0:
					if groupPositive == 0 || current.GroupEOF != 1 || current.GroupCloses != 1 {
						return total, fmt.Errorf("fallback success lacks complete EOF/close: %+v", current)
					}
				case taskPositive == 0 || current.TaskEOF != 1 || current.TaskCloses != 1:
					return total, fmt.Errorf("task success lacks complete EOF/close: %+v", current)
				}
				total.Successes++
			} else {
				total.Errors++
			}
			switch {
			case current.GroupOpens != 0:
				total.Fallbacks++
			case success == 1:
				total.TaskSuccesses++
			default:
				total.TaskErrors++
			}
			total.Calls++
			total.TaskOpens += current.TaskOpens
			total.GroupOpens += current.GroupOpens
			total.TaskEOF += current.TaskEOF
			total.GroupEOF += current.GroupEOF
			total.TaskCloses += current.TaskCloses
			total.GroupCloses += current.GroupCloses
			pid, owner = 0, ""
			continue
		}
		if pid == 0 || fields[0] != owner {
			continue
		}
		taskPath := fmt.Sprintf("/proc/%d/task/%d/stat", pid, pid)
		groupPath := fmt.Sprintf("/proc/%d/stat", pid)
		isTask := strings.Contains(line, `"`+taskPath+`"`) || strings.Contains(line, "<"+taskPath+">")
		isGroup := strings.Contains(line, `"`+groupPath+`"`) || strings.Contains(line, "<"+groupPath+">")
		if !isTask && !isGroup {
			continue
		}
		result, err := numericHintTraceResult(line)
		if err != nil {
			return total, err
		}
		switch {
		case strings.Contains(line, "openat("):
			if isTask {
				current.TaskOpens++
			} else {
				current.GroupOpens++
			}
		case strings.Contains(line, "read("):
			if isTask && result > 0 {
				taskPositive++
			}
			if isGroup && result > 0 {
				groupPositive++
			}
			if isTask && result == 0 {
				current.TaskEOF++
			}
			if isGroup && result == 0 {
				current.GroupEOF++
			}
		case strings.Contains(line, "close("):
			if result != 0 {
				return total, fmt.Errorf("unconfirmed route close: %s", line)
			}
			if isTask {
				current.TaskCloses++
			} else {
				current.GroupCloses++
			}
		}
	}
	if pid != 0 {
		return total, errors.New("Hint has no completed end marker")
	}
	return total, nil
}

// TestNumericHintTraceRoutesSynthetic rejects missing EOF/close and incomplete owner observations.
//
// Example: positive bytes alone cannot be counted as successful task metadata.
func TestNumericHintTraceRoutesSynthetic(t *testing.T) {
	trace := "10 write(1<pipe:[1]>, \"HINT_BEGIN 20\\n\", 14) = 14\n" +
		"10 openat(AT_FDCWD, \"/proc/20/task/20/stat\", O_RDONLY|O_CLOEXEC) = 3</proc/20/task/20/stat>\n" +
		"10 read(3</proc/20/task/20/stat>, \"record\", 512) = 6\n" +
		"10 read(3</proc/20/task/20/stat>, \"\", 506) = 0\n" +
		"10 close(3</proc/20/task/20/stat>) = 0\n" +
		"10 write(1<pipe:[1]>, \"HINT_END 20 1\\n\", 14) = 14\n"
	counts, err := numericHintTraceRoutes([]byte(trace))
	if err != nil || counts.Calls != 1 || counts.TaskSuccesses != 1 || counts.Successes != 1 || counts.Fallbacks != 0 {
		t.Fatalf("complete route rejected: %+v err=%v", counts, err)
	}
	for _, missing := range []string{
		"10 read(3</proc/20/task/20/stat>, \"\", 506) = 0\n",
		"10 close(3</proc/20/task/20/stat>) = 0\n",
		"10 write(1<pipe:[1]>, \"HINT_END 20 1\\n\", 14) = 14\n",
	} {
		if _, err := numericHintTraceRoutes([]byte(strings.ReplaceAll(trace, missing, ""))); err == nil {
			t.Fatalf("incomplete route accepted after removing %q", missing)
		}
	}
	for _, invalid := range []string{
		strings.ReplaceAll(trace, "HINT_END 20 1", "HINT_END 21 1"),
		strings.ReplaceAll(trace, " = 6", " <unfinished ...>"),
		strings.ReplaceAll(trace, "close(3</proc/20/task/20/stat>) = 0", "close(3</proc/20/task/20/stat>) = -1 EIO (error)"),
	} {
		if _, err := numericHintTraceRoutes([]byte(invalid)); err == nil {
			t.Fatal("invalid route accepted")
		}
	}
}

// numericHintTraceMarker emits one trace-visible event on the locked Hint owner thread.
//
// Example: BEGIN/END isolate this call from unrelated Go runtime descriptor activity.
func numericHintTraceMarker(t *testing.T, marker string) {
	t.Helper()
	count, err := os.Stdout.WriteString(marker)
	if err != nil || count != len(marker) {
		t.Fatalf("route marker: count=%d err=%v", count, err)
	}
}

// numericHintObserved serializes an actual weak result across a disposable tracer subprocess.
//
// Example: the native parent joins the tracee before deliberately ending its observed child.
type numericHintObserved struct {
	Stat  processStat
	Error string
}

// TestNumericHintRouteCounts traces actual Hint routes without duplicating fallback classification.
//
// Example: GUARD_HINT_ROUTE_TRACE=1 records four host snapshots; tracer timing is not CPU evidence.
func TestNumericHintRouteCounts(t *testing.T) {
	if mode := os.Getenv("GUARD_HINT_ROUTE_CHILD"); mode != "" {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		entriesCalls, calls, successes := 0, 0, 0
		for pass := 0; pass < 4; pass++ {
			entries, err := (linuxKernel{}).Entries()
			if mode == "one" {
				pid, parseErr := strconv.Atoi(os.Getenv("GUARD_HINT_ROUTE_PID"))
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				entries, err = []int{pid}, nil
			}
			if err != nil {
				t.Fatal(err)
			}
			entriesCalls++
			for _, pid := range entries {
				numericHintTraceMarker(t, fmt.Sprintf("HINT_BEGIN %d\n", pid))
				stat, hintErr := (linuxKernel{}).Hint(pid)
				observed := numericHintObserved{Stat: stat}
				success := 1
				if hintErr != nil {
					observed.Error, success = hintErr.Error(), 0
				}
				numericHintTraceMarker(t, fmt.Sprintf("HINT_END %d %d\n", pid, success))
				calls++
				successes += success
				if mode == "one" {
					data, err := json.Marshal(observed)
					if err != nil {
						t.Fatal(err)
					}
					numericHintTraceMarker(t, "OBSERVED "+string(data)+"\n")
				}
			}
			if mode == "one" {
				break
			}
		}
		t.Logf("actual_hint_routes entries_calls=%d calls=%d successes=%d errors=%d", entriesCalls, calls, successes, calls-successes)
		return
	}
	if os.Getenv("GUARD_HINT_ROUTE_TRACE") != "1" {
		t.Skip("set GUARD_HINT_ROUTE_TRACE=1 for actual route counts separate from CPU sampling")
	}
	tracePath := filepath.Join(t.TempDir(), "all-host.trace")
	if directory := os.Getenv("GUARD_HINT_ARTIFACT_DIR"); directory != "" {
		tracePath = filepath.Join(directory, "guard-numeric-hint-all-host-routes.trace")
	}
	cmd := exec.Command("strace", "-f", "-qq", "-yy", "-s", "4096", "-o", tracePath, "-e", "trace=openat,read,close,write", os.Args[0], "-test.run=^TestNumericHintRouteCounts$", "-test.count=1", "-test.timeout=15s", "-test.v")
	cmd.Env = append(os.Environ(), "GUARD_HINT_ROUTE_CHILD=all")
	output, runErr := cmd.CombinedOutput()
	trace, readErr := os.ReadFile(tracePath)
	if runErr != nil || readErr != nil {
		t.Fatalf("own route trace: run=%v read=%v output=%s", runErr, readErr, output)
	}
	counts, err := numericHintTraceRoutes(trace)
	if err != nil || counts.Calls == 0 || counts.Calls != counts.Successes+counts.Errors || counts.Calls != counts.TaskSuccesses+counts.TaskErrors+counts.Fallbacks {
		t.Fatalf("incomplete actual route counts: %+v err=%v", counts, err)
	}
	var entriesCalls, calls, successes, failures int
	for _, line := range strings.Split(string(output), "\n") {
		if at := strings.Index(line, "actual_hint_routes "); at >= 0 {
			if count, err := fmt.Sscanf(line[at:], "actual_hint_routes entries_calls=%d calls=%d successes=%d errors=%d", &entriesCalls, &calls, &successes, &failures); err != nil || count != 4 {
				t.Fatal("invalid child route counters")
			}
		}
	}
	if entriesCalls != 4 || calls != counts.Calls || successes != counts.Successes || failures != counts.Errors {
		t.Fatalf("trace/actual Hint count mismatch: %+v child=%s", counts, output)
	}
	t.Logf("actual_traced_routes counts=%+v task_success_fraction=%.6f fallback_attempt_fraction=%.6f raw_trace=%s; four snapshots under ptrace, not old untraced profile fractions or CPU measurement", counts, float64(counts.TaskSuccesses)/float64(counts.Calls), float64(counts.Fallbacks)/float64(counts.Calls), tracePath)
}

// numericHintTransitionKernel ends a real weakly observed child before scripted anchored admission.
//
// Example: replacement anchors can have the same birth tick yet a foreign fresh parent.
type numericHintTransitionKernel struct {
	*syntheticKernel
	Test     *testing.T
	Native   *numericHintNative
	Mode     string
	Fallback bool
	Observed processStat
}

// Hint returns actual selected metadata only after the native wait owner has joined its lifetime.
//
// Example: the anchor phase models absence/reuse after task success or an injected actual fallback.
func (k *numericHintTransitionKernel) Hint(pid int) (processStat, error) {
	t := k.Test
	k.Calls = append(k.Calls, fmt.Sprintf("hint:%d", pid))
	var stat processStat
	var err error
	if !k.Fallback {
		stat, err = (linuxKernel{}).Hint(pid)
	}
	if k.Fallback {
		pidText := strconv.Itoa(pid)
		taskPath, groupPath := "/proc/"+pidText+"/task/"+pidText+"/stat", "/proc/"+pidText+"/stat"
		tracePath := filepath.Join(t.TempDir(), "weak-fallback.trace")
		if directory := os.Getenv("GUARD_HINT_ARTIFACT_DIR"); directory != "" {
			tracePath = filepath.Join(directory, "guard-numeric-hint-weak-"+k.Mode+".trace")
		}
		cmd := exec.Command("strace", "-f", "-qq", "-yy", "-s", "4096", "-P", taskPath, "-P", groupPath, "-o", tracePath, "-e", "trace=openat,read,close", "-e", "inject=openat:error=ENOENT:when=1", os.Args[0], "-test.run=^TestNumericHintRouteCounts$", "-test.count=1", "-test.timeout=10s")
		cmd.Env = append(os.Environ(), "GUARD_HINT_ROUTE_CHILD=one", "GUARD_HINT_ROUTE_PID="+pidText)
		output, runErr := cmd.CombinedOutput()
		trace, readErr := os.ReadFile(tracePath)
		if runErr != nil || readErr != nil {
			t.Fatalf("weak fallback child: run=%v read=%v output=%s", runErr, readErr, output)
		}
		var observed numericHintObserved
		observations := 0
		for _, line := range strings.Split(string(output), "\n") {
			if strings.HasPrefix(line, "OBSERVED ") {
				if err := json.Unmarshal([]byte(line[len("OBSERVED "):]), &observed); err != nil {
					t.Fatal(err)
				}
				observations++
			}
		}
		taskOpens, groupOpens, positive, eof, closes := 0, 0, 0, 0, 0
		for _, line := range strings.Split(string(trace), "\n") {
			if strings.Contains(line, "openat(") && strings.Contains(line, `"`+taskPath+`"`) {
				taskOpens++
				if !strings.Contains(line, "= -1 ENOENT") || !strings.Contains(line, "(INJECTED)") {
					t.Fatalf("task miss was not injected: %s", line)
				}
			}
			if strings.Contains(line, "openat(") && strings.Contains(line, `"`+groupPath+`"`) {
				groupOpens++
			}
			if strings.Contains(line, "<"+groupPath+">") {
				result, err := numericHintTraceResult(line)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(line, "read(") && result > 0 {
					positive++
				}
				if strings.Contains(line, "read(") && result == 0 {
					eof++
				}
				if strings.Contains(line, "close(") && result == 0 {
					closes++
				}
			}
		}
		if observations != 1 || observed.Error != "" || taskOpens != 1 || groupOpens != 1 || positive < 1 || eof != 1 || closes != 1 {
			t.Fatalf("actual fallback incomplete: observed=%+v count=%d task=%d group=%d data=%d EOF=%d close=%d trace=%s", observed, observations, taskOpens, groupOpens, positive, eof, closes, trace)
		}
		stat, err = observed.Stat, nil
		t.Logf("actual fallback task_miss=1 group_open=1 positive_reads=%d EOF=1 close=1 sole_tracee_wait_consumed=true trace=%s", positive, tracePath)
	}
	if err != nil || stat.PID != pid || stat.Birth == 0 || stat.Parent != os.Getpid() {
		t.Fatalf("actual weak observation: %+v err=%v", stat, err)
	}
	k.Observed = stat
	k.Calls = append(k.Calls, fmt.Sprintf("hint-complete:%d", pid))
	if err := k.Native.Finish(); err != nil {
		t.Fatal(err)
	}
	k.Calls = append(k.Calls, fmt.Sprintf("native-joined:%d", pid))
	if k.Mode == "absent" {
		k.DirectoryResults = []syntheticFDResult{{Error: unix.ENOENT}}
		k.PIDFDResults = []syntheticFDResult{{Error: unix.ESRCH}}
	} else {
		anchored := stat
		if k.Mode != "owned" {
			anchored.Parent = os.Getpid() + 1
		}
		if k.Mode == "changed-birth" {
			anchored.Birth++
		}
		k.DirectoryResults = []syntheticFDResult{{FD: 30}}
		k.PIDFDResults = []syntheticFDResult{{FD: 40}}
		k.StatReads = []metadataRead{{Stat: anchored}, {Stat: anchored}}
	}
	return stat, nil
}

// TestNumericHintWeakTransition couples actual weak attempts to modeled fresh custody.
//
// Example: actual fallback, joined native death and same-tick foreign anchors confer no authority.
func TestNumericHintWeakTransition(t *testing.T) {
	if os.Getenv("GUARD_HINT_NATIVE") != "1" {
		t.Skip("set GUARD_HINT_NATIVE=1 for actual weak observation plus modeled death/reuse")
	}
	binary := compileNumericHintNative(t)
	for _, fallback := range []bool{false, true} {
		for _, mode := range []string{"absent", "changed-birth", "same-birth", "owned"} {
			t.Run(fmt.Sprintf("fallback-%t-%s", fallback, mode), func(t *testing.T) {
				native := startNumericHintNative(t, binary, "hold", 1)
				kernel := &numericHintTransitionKernel{syntheticKernel: &syntheticKernel{}, Test: t, Native: native, Mode: mode, Fallback: fallback}
				g := newGuard(kernel, &syntheticClock{Time: time.Unix(100, 0)}, nil, time.Second, time.Second, ProcessIdentity{})
				if _, err := kernel.Hint(native.PID); err != nil {
					t.Fatal(err)
				}
				kernel.RunningFDs = map[int]bool{70: true}
				directory, err := kernel.OpenDirectory(native.PID)
				if err != nil && !errors.Is(err, unix.ENOENT) {
					t.Fatal(err)
				}
				if err != nil {
					directory = -1
				}
				g.acquire(&pendingAcquisition{PID: native.PID, DirectoryFD: directory}, map[int]*custody{os.Getpid(): {ProcessIdentity: ProcessIdentity{PID: os.Getpid(), Birth: 1}, FD: 70, Owned: true}})
				if !native.Waited || native.Command.ProcessState == nil {
					t.Fatal("weak/native/anchor sequence not complete")
				}
				prefix := fmt.Sprintf("hint:%d hint-complete:%d native-joined:%d directory:%d", native.PID, native.PID, native.PID, native.PID)
				wanted := prefix + fmt.Sprintf(" stat:30:%d pidfd:%d stat:30:%d", native.PID, native.PID, native.PID)
				if mode == "absent" {
					wanted = prefix + fmt.Sprintf(" pidfd:%d", native.PID)
				}
				if mode == "owned" {
					wanted += " poll:70"
				}
				if strings.Join(kernel.Calls, " ") != wanted {
					t.Fatalf("incorrect admission sequence: got=%v wanted=%s", kernel.Calls, wanted)
				}
				g.beginRetirement("test weak transition")
				g.retire()
				wantedCloses := "[40 30]"
				if mode == "owned" {
					value := g.current[native.PID]
					if value == nil || !value.Owned || value.ProcessIdentity != kernel.Observed.ProcessIdentity || len(kernel.Signals) != 1 || kernel.Signals[0] != (syntheticSignal{FD: 40, Signal: syscall.SIGTERM}) {
						t.Fatalf("positive anchored control did not retain authority: current=%v signals=%v", g.current, kernel.Signals)
					}
					g.release(value)
					wantedCloses = "[30 40]"
				}
				if mode != "owned" && (len(g.current) != 0 || len(g.owned) != 0 || len(kernel.Signals) != 0) {
					t.Fatalf("weak replacement acquired foreign signal authority: current=%v signals=%v", g.current, kernel.Signals)
				}
				if mode == "absent" {
					wantedCloses = "[]"
				}
				if fmt.Sprint(kernel.Closed) != wantedCloses || len(g.pending) != 0 || len(g.tentative) != 0 || len(g.receipt.Errors) != 0 || len(kernel.StatReads) != 0 || len(kernel.PIDFDResults) != 0 || len(kernel.DirectoryResults) != 0 {
					t.Fatalf("scratch ownership incomplete: closes=%v pending=%v tentative=%v errors=%v", kernel.Closed, g.pending, g.tentative, g.receipt.Errors)
				}
				t.Logf("actual_hint_then_joined_death fallback=%t modeled_anchor=%s observed=%+v sequence=%v closed=%v signals=%v; modeled PID reuse, not native reuse or native task-view loss", fallback, mode, kernel.Observed, kernel.Calls, kernel.Closed, kernel.Signals)
			})
		}
	}
}
