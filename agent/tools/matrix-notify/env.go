package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
)

// HermesCredentials are Hermes's Matrix account settings, read in place from its .env on every run.
type HermesCredentials struct {
	Homeserver string
	UserID     string
	Password   string
	// HermesDeviceID is Hermes's own E2EE device; it is read only to make sure this tool never uses it.
	HermesDeviceID string
}

// parseEnv reads KEY=VALUE lines as data; nothing is expanded or executed.
func parseEnv(r io.Reader) map[string]string {
	env := map[string]string{}
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		env[strings.TrimSpace(k)] = v
	}
	return env
}

func loadHermesCredentials(path string) (HermesCredentials, error) {
	f, err := os.Open(path)
	if err != nil {
		return HermesCredentials{}, fmt.Errorf("%w: open Hermes env: %w", errCredentials, err)
	}
	defer f.Close()
	env := parseEnv(f)
	creds := HermesCredentials{
		Homeserver:     strings.TrimRight(env["MATRIX_HOMESERVER"], "/"),
		UserID:         env["MATRIX_USER_ID"],
		Password:       env["MATRIX_PASSWORD"],
		HermesDeviceID: env["MATRIX_DEVICE_ID"],
	}
	var errs []error
	for _, kv := range [][2]string{{"MATRIX_HOMESERVER", creds.Homeserver}, {"MATRIX_USER_ID", creds.UserID}, {"MATRIX_PASSWORD", creds.Password}} {
		if kv[1] == "" {
			errs = append(errs, fmt.Errorf("%w: %s missing in %s", errCredentials, kv[0], path))
		}
	}
	if u, err := url.Parse(creds.Homeserver); creds.Homeserver != "" && (err != nil || u.Scheme != "https" || u.Host == "") {
		errs = append(errs, fmt.Errorf("%w: MATRIX_HOMESERVER must be an https origin", errCredentials))
	}
	return creds, errors.Join(errs...)
}
