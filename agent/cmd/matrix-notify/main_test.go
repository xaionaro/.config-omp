package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"maunium.net/go/mautrix/event"
)

// wireFixture is testdata/wire.json, the Go→TS result contract that tests/matrix-notify.test.ts also reads.
type wireFixture struct {
	Categories []string          `json:"categories"`
	Aliases    []string          `json:"aliases"`
	Cases      map[string]string `json:"cases"`
}

func loadWireFixture(t *testing.T) wireFixture {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "wire.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f wireFixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// connected is a Result as run fills it after connecting, for void.
func connected(o Outcome) Result {
	return Result{Outcome: o, To: RecipientVoid, RecipientMXID: voidM, SenderMXID: sender, DeviceID: "OWNDEV"}
}

func cat(c Category) *Category { return &c }

// T3: report's output for every result shape equals the fixture byte for byte; categories and aliases match.
func TestReportMatchesWireFixture(t *testing.T) {
	joined := Outcome{RoomID: "!r:hs", RecipientMembership: event.MembershipJoin}
	with := func(o Outcome, s Status) Outcome { o.Status = s; return o }
	sentJoin := with(joined, StatusSent)
	sentJoin.EventID = "$e"
	sentInvite := sentJoin
	sentInvite.RecipientMembership = event.MembershipInvite
	cleanup := connected(sentJoin)
	cleanup.CleanupError = cat(CategoryState)
	invitePending := Outcome{Status: StatusInvitePending, RoomID: "!r:hs", RecipientMembership: event.MembershipInvite}
	vickey := func(o Outcome) Result {
		return Result{Outcome: o, To: RecipientVickey, RecipientMXID: vicM, SenderMXID: sender, DeviceID: "OWNDEV"}
	}
	cases := map[string]struct {
		res Result
		err error
	}{
		"sent_join":          {connected(sentJoin), nil},
		"sent_invite":        {connected(sentInvite), nil},
		"sent_cleanup_error": {cleanup, nil},
		"invite_pending":     {connected(invitePending), nil},
		"key_pending":        {connected(with(joined, StatusKeyPending)), nil},
		"unknown":            {connected(with(joined, StatusUnknown)), nil},
		"error":              {Result{To: RecipientVoid, RecipientMXID: voidM}, categorize(CategoryAuth, errors.New("rejected"))},
		"resolved":           {vickey(with(joined, StatusResolved)), nil},
		"absent":             {vickey(Outcome{Status: StatusAbsent}), nil},
	}
	fixture := loadWireFixture(t)
	if got, want := slices.Sorted(maps.Keys(cases)), slices.Sorted(maps.Keys(fixture.Cases)); !slices.Equal(got, want) {
		t.Fatalf("case names: go %v, fixture %v", got, want)
	}
	for name, c := range cases {
		var buf bytes.Buffer
		report(&buf, c.res, c.err)
		if got := strings.TrimSuffix(buf.String(), "\n"); got != fixture.Cases[name] {
			t.Errorf("%s:\n got  %s\n want %s", name, got, fixture.Cases[name])
		}
	}
	if !slices.Equal(fixture.Categories, categoryNames[:]) {
		t.Errorf("categories: fixture %v, go %v", fixture.Categories, categoryNames)
	}
	if !slices.Equal(fixture.Aliases, recipientAliases()) {
		t.Errorf("aliases: fixture %v, go %v", fixture.Aliases, recipientAliases())
	}
}

func TestReportExitCodes(t *testing.T) {
	if exitOK != 0 || exitError != 1 || exitPending != 2 || exitUnknown != 3 {
		t.Fatal("exit code values changed")
	}
	cases := map[exitCode][]Result{
		exitOK:      {connected(Outcome{Status: StatusSent, EventID: "$e"}), connected(Outcome{Status: StatusResolved}), connected(Outcome{Status: StatusAbsent})},
		exitPending: {connected(Outcome{Status: StatusKeyPending}), connected(Outcome{Status: StatusInvitePending})},
		exitUnknown: {connected(Outcome{Status: StatusUnknown})},
	}
	for want, results := range cases {
		for _, res := range results {
			if got := report(&bytes.Buffer{}, res, nil); got != want {
				t.Errorf("%v: exit %d, want %d", res.Status, got, want)
			}
		}
	}
	if got := report(&bytes.Buffer{}, connected(Outcome{}), errors.New("x")); got != exitError {
		t.Errorf("error exit %d", got)
	}
}

func TestReportCategorizesWithoutLeakingSecrets(t *testing.T) {
	cause := categorize(CategoryAuth, fmt.Errorf("login https://hs password=hunter2 token syt_abc"))
	var buf bytes.Buffer
	code := report(&buf, Result{To: RecipientVoid}, cause)
	out := buf.String()
	if code != exitError || !strings.Contains(out, `"error":"auth"`) {
		t.Fatalf("code %d out %s", code, out)
	}
	for _, leak := range []string{"hunter2", "syt_abc", "https://hs", "login"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q: %s", leak, out)
		}
	}
	buf.Reset()
	if code := report(&buf, Result{To: RecipientVoid}, errors.New("unexpected")); code != exitError || !strings.Contains(buf.String(), `"error":"internal"`) {
		t.Fatalf("uncategorized: %d %s", code, buf.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestReportFailsWhenResultCannotBeWritten(t *testing.T) {
	if code := report(failingWriter{}, connected(Outcome{Status: StatusSent, EventID: "$e"}), nil); code != exitError {
		t.Fatalf("code %d", code)
	}
}

// T1: a failed cleanup never rewrites an outcome that resolve or deliver already returned.
func TestCleanupFailureKeepsTheOutcome(t *testing.T) {
	closeErr := categorize(CategoryState, errors.New("close crypto.db"))

	res := connected(Outcome{Status: StatusSent, RoomID: "!r:hs", EventID: "$e"})
	var err error
	settleCleanup(&res, &err, closeErr)
	var buf bytes.Buffer
	code := report(&buf, res, err)
	if code != exitOK || err != nil || !strings.Contains(buf.String(), `"status":"sent"`) ||
		!strings.Contains(buf.String(), `"event_id":"$e"`) || !strings.Contains(buf.String(), `"cleanup_error":"state"`) {
		t.Fatalf("sent: exit %d err %v out %s", code, err, buf.String())
	}

	res = connected(Outcome{Status: StatusKeyPending, RoomID: "!r:hs"})
	err = nil
	settleCleanup(&res, &err, closeErr)
	buf.Reset()
	if code := report(&buf, res, err); code != exitPending || !strings.Contains(buf.String(), `"cleanup_error":"state"`) {
		t.Fatalf("pending: exit %d out %s", code, buf.String())
	}

	res = Result{To: RecipientVoid}
	err = categorize(CategoryAuth, errors.New("login"))
	settleCleanup(&res, &err, closeErr)
	buf.Reset()
	if code := report(&buf, res, err); code != exitError || !strings.Contains(buf.String(), `"error":"auth"`) ||
		strings.Contains(buf.String(), "cleanup_error") || !errors.Is(err, closeErr) {
		t.Fatalf("failed run: exit %d err %v out %s", code, err, buf.String())
	}

	res = connected(Outcome{Status: StatusSent, EventID: "$e"})
	err = nil
	settleCleanup(&res, &err, nil)
	if res.CleanupError != nil || err != nil {
		t.Fatal("clean close marked as failure")
	}
}

func TestInvalidInputStopsBeforeCredentialsOrState(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	for _, tc := range []struct{ to, msg string }{{"bob", "hi"}, {"void", "  \n"}} {
		cfg := runConfig{To: tc.to, StateDir: state, EnvPath: filepath.Join(dir, "missing.env")}
		if _, err := run(context.Background(), cfg, strings.NewReader(tc.msg)); categoryOf(err) != CategoryInput {
			t.Errorf("%+v: %v", tc, err)
		}
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("state dir touched")
	}
}
