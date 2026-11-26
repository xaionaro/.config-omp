// matrix-notify sends one end-to-end encrypted Matrix DM to an allowlisted person (void or vickey) as Hermes's
// Matrix account. Credentials are read in place from Hermes's .env; the tool logs in as its OWN server-assigned
// device with its own token and crypto store (~/.omp/agent/matrix-notify, outside git), never Hermes's device or
// store. It runs no sync loop and installs no inbound behavior.
//
// Build once per host (cgo is required by the SQLite driver):
//
//	cd ~/.omp/agent/tools/matrix-notify && go build -tags goolm -o ~/.omp/agent/matrix-notify/bin/matrix-notify .
//
// Usage: matrix-notify -to void|vickey < message
// -resolve-only (verification only) reports the DM room without creating, inviting or sending.
// Prints one JSON object. Exit 0: sent/resolved/absent; 2: pending (not sent); 1: error.
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
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"maunium.net/go/mautrix/id"
)

// Fixed error categories; only the category leaves the process, never raw command/SDK/HTTP text.
var (
	errInput       = errors.New("input")
	errCredentials = errors.New("credentials")
	errState       = errors.New("state")
	errIdentity    = errors.New("identity")
	errAuth        = errors.New("auth")
	errNetwork     = errors.New("network")
	errRoom        = errors.New("room")
	errCrypto      = errors.New("crypto")
	errSend        = errors.New("send")
)

var categories = []error{errInput, errCredentials, errState, errIdentity, errAuth, errNetwork, errRoom, errCrypto, errSend}

type result struct {
	Status           status `json:"status"`
	To               string `json:"to"`
	Recipient        string `json:"recipient_mxid,omitempty"`
	Sender           string `json:"sender_mxid,omitempty"`
	DeviceID         string `json:"device_id,omitempty"`
	RoomID           string `json:"room_id,omitempty"`
	TargetMembership string `json:"target_membership,omitempty"`
	EventID          string `json:"event_id,omitempty"`
	Error            string `json:"error,omitempty"`
}

func main() {
	// Every file this process creates (crypto store, session, rooms) is private to the user.
	syscall.Umask(0o077)
	home, err := os.UserHomeDir()
	if err != nil {
		os.Exit(report(os.Stdout, result{}, fmt.Errorf("%w: %w", errState, err)))
	}
	hermesHome := os.Getenv("HERMES_HOME")
	if hermesHome == "" {
		hermesHome = filepath.Join(home, ".hermes")
	}
	to := flag.String("to", "", "recipient alias: void or vickey")
	resolveOnly := flag.Bool("resolve-only", false, "verification only: report the DM room; never create, invite or send")
	stateDir := flag.String("state-dir", filepath.Join(home, ".omp/agent/matrix-notify"), "own device/token/crypto store")
	envPath := flag.String("hermes-env", filepath.Join(hermesHome, ".env"), "Hermes .env holding the Matrix credentials")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res := result{To: *to}
	err = run(ctx, &res, *stateDir, *envPath, os.Stdin, *resolveOnly)
	cancel()
	os.Exit(report(os.Stdout, res, err))
}

// report writes res as JSON with err reduced to its category, and returns the exit code.
func report(w io.Writer, res result, err error) int {
	code := 0
	if err != nil {
		res.Status, res.Error, code = statusError, "internal", 1
		for _, c := range categories {
			if errors.Is(err, c) {
				res.Error = c.Error()
				break
			}
		}
	} else if res.Status == statusInvitePending || res.Status == statusKeyPending {
		code = 2
	}
	_ = json.NewEncoder(w).Encode(res)
	return code
}

func run(ctx context.Context, res *result, stateDir, envPath string, stdin io.Reader, resolveOnly bool) error {
	target, err := recipientMXID(res.To)
	if err != nil {
		return err
	}
	res.Recipient = target.String()
	var message string
	if !resolveOnly {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return fmt.Errorf("%w: read message: %w", errInput, err)
		}
		if message = string(b); !validMessage(message) {
			return fmt.Errorf("%w: empty message", errInput)
		}
	}
	creds, err := loadHermesCredentials(envPath)
	if err != nil {
		return err
	}
	if err := ensureStateDir(stateDir); err != nil {
		return fmt.Errorf("%w: %w", errState, err)
	}
	unlock, err := lockStateDir(stateDir)
	if err != nil {
		return fmt.Errorf("%w: %w", errState, err)
	}
	defer unlock()

	api, err := connect(ctx, stateDir, creds)
	if err != nil {
		return err
	}
	defer api.crypto.Close()
	res.Sender, res.DeviceID = api.cli.UserID.String(), api.cli.DeviceID.String()

	store := roomFile{dir: stateDir, account: creds.Homeserver + "|" + creds.UserID}
	out, err := deliver(ctx, api, store, id.UserID(creds.UserID), target, message, resolveOnly)
	if err != nil {
		return err
	}
	res.Status, res.RoomID, res.TargetMembership, res.EventID = out.Status, out.Room.String(), string(out.TargetMembership), out.EventID.String()
	return nil
}
