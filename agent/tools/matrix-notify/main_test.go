package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportCategorizesWithoutLeakingSecrets(t *testing.T) {
	cause := categorize(CategoryAuth, fmt.Errorf("login https://hs password=hunter2 token syt_abc"))
	var buf bytes.Buffer
	code := report(&buf, Result{To: RecipientVoid}, cause)
	out := buf.String()
	if code != exitError || !strings.Contains(out, `"error":"auth"`) || !strings.Contains(out, `"status":"error"`) {
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

func TestReportExitCodesAndSentRequiresEvent(t *testing.T) {
	cases := []struct {
		res  Result
		code int
		want string
	}{
		{Result{To: RecipientVoid, Status: StatusSent, EventID: "$e"}, exitOK, `"status":"sent"`},
		{Result{To: RecipientVoid, Status: StatusSent}, exitError, `"error":"send"`},
		{Result{To: RecipientVoid, Status: StatusKeyPending}, exitPending, `"status":"key_pending"`},
		{Result{To: RecipientVickey, Status: StatusResolved}, exitOK, `"to":"vickey"`},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		if code := report(&buf, c.res, nil); code != c.code || !strings.Contains(buf.String(), c.want) {
			t.Errorf("%+v: %d %s", c.res, code, buf.String())
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestReportFailsWhenResultCannotBeWritten(t *testing.T) {
	if code := report(failingWriter{}, Result{To: RecipientVoid, Status: StatusSent, EventID: "$e"}, nil); code != exitError {
		t.Fatalf("code %d", code)
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
