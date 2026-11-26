// matrix-notify sends one end-to-end encrypted Matrix DM to an allowlisted person (void or vickey) as Hermes's
// Matrix account. It is the sender behind the omp tool matrix_send (agent/extensions/matrix-notify.ts). Credentials
// are read in place from Hermes's .env; the tool logs in as its OWN server-assigned device ("OMP Matrix notify")
// with its own token and crypto store, never Hermes's device or store. It runs no sync loop and installs no inbound
// behavior.
//
// # Runbook
//
// Build once per host, and rebuild after any change in this directory (cgo is required by the SQLite driver); the
// omp tool never builds it:
//
//	cd ~/.omp/agent/cmd/matrix-notify && go build -tags goolm -o matrix-notify .
//
// Usage: matrix-notify -to void|vickey [-deadline 2m] < message
//
// -resolve-only repeats room resolution and reports the DM room without creating, inviting, sharing keys or
// sending; use it to check a recipient's path.
//
// State lives in ~/.omp/agent/matrix-notify (0700, gitignored): session.json (own device and token), pickle.key,
// crypto.db* (Olm store), rooms.json (DMs this tool created), lock. Hermes's .env is ~/.hermes/.env, or
// $HERMES_HOME/.env.
//
// Each run prints one JSON object. Status and exit code:
//
//	sent, resolved, absent         0  Matrix accepted the event (sent), or the resolve-only answer.
//	error                          1  NOT sent; "error" names the category below.
//	invite_pending, key_pending    2  NOT sent; the recipient must join the DM, or has no device that can get the key.
//	unknown                        3  the send request may have been stored: check the DM before retrying.
//
// A failure to close local state after the outcome was known is reported as "cleanup_error" (category) and never
// changes the outcome. Causes are never printed; only the category leaves the process. Categories and actions:
//
//	input        bad alias or empty message: fix the call.
//	credentials  MATRIX_HOMESERVER (https URL with a host), MATRIX_USER_ID or MATRIX_PASSWORD missing in Hermes's .env.
//	state        the state directory or a file in it is unreadable or unwritable: check permissions and disk space.
//	identity     the saved session or crypto store does not match Hermes's account, or is Hermes's own device: delete
//	             session.json, pickle.key, crypto.db* and rooms.json from the state directory, then remove the stale
//	             "OMP Matrix notify" device in a Matrix client. The next run logs in as a new device.
//	auth         the homeserver rejected the password or token: check MATRIX_PASSWORD; a revoked token renews itself.
//	network      the homeserver could not be reached: retry later.
//	room         room state, m.direct or room creation failed: check the account's rooms in a Matrix client.
//	crypto       key query, sharing or encryption failed: retry; if it persists, reset as for identity.
//	send         the homeserver rejected the event: retry later.
//	internal     an unexpected failure: rerun -resolve-only to narrow it down.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
	"maunium.net/go/mautrix/id"
)

// Process exit codes, limits and default locations. The omp extension passes -deadline and kills only after it.
//
// Example: os.Exit(exitPending) after a key_pending result.
const (
	exitOK          = 0
	exitError       = 1
	exitPending     = 2
	exitUnknown     = 3
	defaultDeadline = 2 * time.Minute
	privateUmask    = 0o077
	hermesHomeEnv   = "HERMES_HOME"
	hermesHomeDir   = ".hermes"
	hermesEnvFile   = ".env"
	stateDirInHome  = ".omp/agent/matrix-notify"
)

// Result is the single JSON object printed per run; testdata/wire.json pins its exact form.
//
// Example: {"status":"sent","room_id":"!r:hs","recipient_membership":"join","event_id":"$e","to":"void",...}.
type Result struct {
	Outcome
	To            Recipient   `json:"to"`
	RecipientMXID id.UserID   `json:"recipient_mxid,omitempty"`
	SenderMXID    id.UserID   `json:"sender_mxid,omitempty"`
	DeviceID      id.DeviceID `json:"device_id,omitempty"`
	Error         *Category   `json:"error,omitempty"`
	CleanupError  *Category   `json:"cleanup_error,omitempty"`
}

// runConfig is one invocation's inputs.
//
// Example: runConfig{To: "void", StateDir: dir, EnvPath: env}.
type runConfig struct {
	To          string
	ResolveOnly bool
	StateDir    string
	EnvPath     string
}

// main parses flags, runs once and exits with report's code.
//
// Example: echo hi | matrix-notify -to void.
func main() {
	// Every file this process creates (crypto store, session, rooms) is private to the user.
	unix.Umask(privateUmask)
	home, err := os.UserHomeDir()
	if err != nil {
		os.Exit(report(os.Stdout, Result{}, categorize(CategoryState, err)))
	}
	hermesHome := os.Getenv(hermesHomeEnv)
	if hermesHome == "" {
		hermesHome = filepath.Join(home, hermesHomeDir)
	}
	cfg := runConfig{StateDir: filepath.Join(home, stateDirInHome), EnvPath: filepath.Join(hermesHome, hermesEnvFile)}
	flag.StringVar(&cfg.To, "to", "", "recipient alias: void or vickey")
	flag.BoolVar(&cfg.ResolveOnly, "resolve-only", false, "report the DM room; never create, invite, share keys or send")
	deadline := flag.Duration("deadline", defaultDeadline, "give up after this long")
	flag.Parse()

	// TODO: SIGTERM/SIGINT end the process without cancelling ctx, so a run killed mid-request prints nothing (the
	// omp tool then reports an unknown outcome); convert them with signal.NotifyContext so the run reports
	// StatusUnknown or its pre-send error.
	ctx, cancel := context.WithTimeout(context.Background(), *deadline)
	res, err := run(ctx, cfg, os.Stdin)
	cancel()
	// TODO: only the category leaves the process, so causes are lost; write a private, secret-free local debug log
	// in the state dir (go-belt via ctx).
	os.Exit(report(os.Stdout, res, err))
}

// report writes res as JSON with err reduced to its category, and returns the exit code; an unwritable result is an
// error.
//
// Example: os.Exit(report(os.Stdout, res, err)).
func report(
	w io.Writer,
	res Result,
	err error,
) int {
	code := exitOK
	switch {
	case err != nil:
		c := categoryOf(err)
		res.Outcome, res.Error, code = Outcome{Status: StatusError}, &c, exitError
	case res.Status == StatusInvitePending, res.Status == StatusKeyPending:
		code = exitPending
	case res.Status == StatusUnknown:
		code = exitUnknown
	}
	if err := json.NewEncoder(w).Encode(res); err != nil {
		return exitError
	}
	return code
}

// settleCleanup folds a cleanup failure into a run's result. Once resolve or deliver produced an outcome it is final,
// so the failure is only recorded as CleanupError; before that, it joins the run's error behind the first cause.
//
// Example: defer func() { settleCleanup(&res, &err, lock.Close()) }().
func settleCleanup(
	res *Result,
	err *error,
	cleanupErr error,
) {
	if cleanupErr == nil {
		return
	}
	if *err == nil && res.Status != StatusUnset {
		c := categoryOf(categorize(CategoryState, cleanupErr))
		res.CleanupError = &c
		return
	}
	*err = errors.Join(*err, categorize(CategoryState, cleanupErr))
}

// run validates input before touching credentials or state, then resolves or delivers under the state lock.
//
// Example: res, err := run(ctx, runConfig{To: "void", ...}, strings.NewReader("hi")).
func run(
	ctx context.Context,
	cfg runConfig,
	stdin io.Reader,
) (res Result, err error) {
	to, err := parseRecipient(cfg.To)
	if err != nil {
		return res, err
	}
	res.To, res.RecipientMXID = to, to.MXID()
	var message string
	if !cfg.ResolveOnly {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return res, categorize(CategoryInput, fmt.Errorf("read message: %w", err))
		}
		if message = string(b); !validMessage(message) {
			return res, categorize(CategoryInput, errors.New("empty message"))
		}
	}
	creds, err := loadHermesCredentials(cfg.EnvPath)
	if err != nil {
		return res, err
	}
	if err := ensureStateDir(cfg.StateDir); err != nil {
		return res, err
	}
	lock, err := lockStateDir(ctx, cfg.StateDir)
	if err != nil {
		return res, err
	}
	defer
	// Release the state lock last; settleCleanup keeps an already known outcome.
	//
	// Example: runs when run returns, after the crypto store closed.
	func() { settleCleanup(&res, &err, lock.Close()) }()

	api, err := connect(ctx, cfg.StateDir, creds, nil)
	if err != nil {
		return res, err
	}
	defer
	// Close the crypto store before the lock; settleCleanup keeps an already known outcome.
	//
	// Example: runs when run returns, before the lock is released.
	func() { settleCleanup(&res, &err, api.Crypto.Close()) }()
	res.SenderMXID, res.DeviceID = api.Client.UserID, api.Client.DeviceID

	store := RoomFile{Dir: cfg.StateDir, Account: creds.Account}
	if cfg.ResolveOnly {
		res.Outcome, err = resolve(ctx, api, store, creds.UserID, to.MXID())
	} else {
		res.Outcome, err = deliver(ctx, api, store, creds.UserID, to.MXID(), message)
	}
	return res, err
}
