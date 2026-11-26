// matrix-notify sends one end-to-end encrypted Matrix DM to an allowlisted person (void or vickey) as Hermes's
// Matrix account. Credentials are read in place from Hermes's .env; the tool logs in as its OWN server-assigned
// device with its own token and crypto store (~/.omp/agent/matrix-notify, outside git), never Hermes's device or
// store. It runs no sync loop and installs no inbound behavior.
//
// Build once per host, and rebuild after any change under agent/tools/matrix-notify (cgo is required by the SQLite
// driver); the omp tool never builds it:
//
//	cd ~/.omp/agent/tools/matrix-notify && go build -tags goolm -o ~/.omp/agent/matrix-notify/bin/matrix-notify .
//
// Usage: matrix-notify -to void|vickey [-deadline 2m] < message
// -resolve-only (verification only) reports the DM room without creating, inviting, sharing keys or sending.
// Prints one JSON object. Exit 0: sent/resolved/absent; 2: pending (not sent); 1: error.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"maunium.net/go/mautrix/id"
)

// Process exit codes and limits. The omp extension passes -deadline and kills the process only after it.
//
// Example: os.Exit(exitPending) after a key_pending result.
const (
	exitOK          = 0
	exitError       = 1
	exitPending     = 2
	defaultDeadline = 2 * time.Minute
	privateUmask    = 0o077
)

// Result is the single JSON object printed per run.
//
// Example: {"status":"sent","to":"void","room_id":"!r:hs","event_id":"$e"}.
type Result struct {
	Status           Status      `json:"status"`
	To               Recipient   `json:"to"`
	Recipient        id.UserID   `json:"recipient_mxid,omitempty"`
	Sender           id.UserID   `json:"sender_mxid,omitempty"`
	DeviceID         id.DeviceID `json:"device_id,omitempty"`
	RoomID           id.RoomID   `json:"room_id,omitempty"`
	TargetMembership string      `json:"target_membership,omitempty"`
	EventID          id.EventID  `json:"event_id,omitempty"`
	Error            *Category   `json:"error,omitempty"`
}

// runConfig is one invocation's parsed command line.
//
// Example: runConfig{To: "void", StateDir: dir, EnvPath: env}.
type runConfig struct {
	To          string
	ResolveOnly bool
	StateDir    string
	EnvPath     string
	HTTPClient  *http.Client
}

// main parses flags, runs once and exits with report's code.
//
// Example: echo hi | matrix-notify -to void.
func main() {
	// Every file this process creates (crypto store, session, rooms) is private to the user.
	syscall.Umask(privateUmask)
	home, err := os.UserHomeDir()
	if err != nil {
		os.Exit(report(os.Stdout, Result{}, categorize(CategoryState, err)))
	}
	hermesHome := os.Getenv("HERMES_HOME")
	if hermesHome == "" {
		hermesHome = filepath.Join(home, ".hermes")
	}
	var cfg runConfig
	flag.StringVar(&cfg.To, "to", "", "recipient alias: void or vickey")
	flag.BoolVar(&cfg.ResolveOnly, "resolve-only", false, "verification only: report the DM room; never create, invite, share keys or send")
	flag.StringVar(&cfg.StateDir, "state-dir", filepath.Join(home, ".omp/agent/matrix-notify"), "own device/token/crypto store")
	flag.StringVar(&cfg.EnvPath, "hermes-env", filepath.Join(hermesHome, ".env"), "Hermes .env holding the Matrix credentials")
	deadline := flag.Duration("deadline", defaultDeadline, "give up after this long")
	flag.Parse()

	// TODO(C7): SIGTERM/SIGINT end the process without cancelling ctx, so a run killed after the server accepted the
	// event reports nothing and a retry can send twice; convert them with signal.NotifyContext and report "cancelled".
	ctx, cancel := context.WithTimeout(context.Background(), *deadline)
	res, err := run(ctx, cfg, os.Stdin)
	cancel()
	// TODO(A12): only the category leaves the process, so causes are lost; write a private, secret-free local debug
	// log in the state dir (go-belt via ctx) or document the diagnostic route.
	os.Exit(report(os.Stdout, res, err))
}

// report writes res as JSON with err reduced to its category, and returns the exit code; a sent result without an
// event ID, or an unwritable result, is an error.
//
// Example: os.Exit(report(os.Stdout, res, err)).
func report(
	w io.Writer,
	res Result,
	err error,
) int {
	if err == nil && res.Status == StatusSent && res.EventID == "" {
		err = categorize(CategorySend, errors.New("sent without event ID"))
	}
	code := exitOK
	switch {
	case err != nil:
		c := categoryOf(err)
		res.Status, res.Error, res.EventID, code = StatusError, &c, "", exitError
	case res.Status == StatusInvitePending, res.Status == StatusKeyPending:
		code = exitPending
	}
	if err := json.NewEncoder(w).Encode(res); err != nil {
		return exitError
	}
	return code
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
	res.To, res.Recipient = to, to.MXID()
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
	defer func() { err = errors.Join(err, categorizeIfErr(CategoryState, lock.Close())) }()

	api, err := connect(ctx, cfg.StateDir, creds, cfg.HTTPClient)
	if err != nil {
		return res, err
	}
	defer func() { err = errors.Join(err, categorizeIfErr(CategoryState, api.Crypto.Close())) }()
	res.Sender, res.DeviceID = api.Client.UserID, api.Client.DeviceID

	store := RoomFile{Dir: cfg.StateDir, Account: creds.Account}
	var out Outcome
	if cfg.ResolveOnly {
		out, err = resolve(ctx, api, store, creds.UserID, to.MXID())
	} else {
		out, err = deliver(ctx, api, store, creds.UserID, to.MXID(), message)
	}
	if err != nil {
		return res, err
	}
	res.Status, res.RoomID, res.TargetMembership, res.EventID = out.Status, out.Room, string(out.TargetMembership), out.EventID
	return res, nil
}
