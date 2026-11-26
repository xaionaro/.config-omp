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
	cause := fmt.Errorf("%w: login https://hs password=hunter2 token syt_abc", errAuth)
	var buf bytes.Buffer
	code := report(&buf, result{To: "void"}, cause)
	out := buf.String()
	if code == 0 || !strings.Contains(out, `"error":"auth"`) || !strings.Contains(out, `"status":"error"`) {
		t.Fatalf("code %d out %s", code, out)
	}
	for _, leak := range []string{"hunter2", "syt_abc", "https://hs", "login"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q: %s", leak, out)
		}
	}
	buf.Reset()
	if code := report(&buf, result{To: "void"}, errors.New("unexpected")); code == 0 || !strings.Contains(buf.String(), `"error":"internal"`) {
		t.Fatalf("uncategorized: %d %s", code, buf.String())
	}
}

func TestInvalidInputStopsBeforeCredentialsOrState(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	env := filepath.Join(dir, "missing.env")
	for _, tc := range []struct{ to, msg string }{{"bob", "hi"}, {"void", "  \n"}} {
		res := result{To: tc.to}
		err := run(context.Background(), &res, state, env, strings.NewReader(tc.msg), false)
		if !errors.Is(err, errInput) {
			t.Errorf("%+v: %v", tc, err)
		}
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("state dir touched")
	}
}
