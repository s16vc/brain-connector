package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/s16vc/brain-connector/internal/tokentest"
)

func environment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestUsageNeverPrintsTheToken(t *testing.T) {
	token := tokentest.Token(t)
	env := environment(map[string]string{"BRAIN_CONNECTOR_TOKEN": token, "BRAIN_CONNECTOR_TARGET": "db.internal:5432"})
	cases := map[string]struct {
		args []string
		code int
	}{
		"a mistyped flag": {[]string{"--targte", "db:5432"}, 2},
		"help":            {[]string{"-h"}, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			if code := run(tc.args, env, &output, &output); code != tc.code {
				t.Fatalf("exit code = %d, want %d", code, tc.code)
			}
			if !strings.Contains(output.String(), "BRAIN_CONNECTOR_TOKEN") {
				t.Fatalf("usage was not printed: %q", output.String())
			}
			if strings.Contains(output.String(), strings.TrimPrefix(token, "bdc_")[:20]) {
				t.Fatal("the usage text contains the token")
			}
		})
	}
}

func TestVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version"}, environment(nil), &stdout, &stderr); code != 0 || stdout.String() != version+"\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

func TestSettingsComeFromFlagsThenEnvironment(t *testing.T) {
	token := tokentest.Token(t)
	cases := map[string]struct {
		args []string
		env  map[string]string
		want string
	}{
		"token from the environment": {
			[]string{"--target", "db:5432", "--server", "ws://127.0.0.1:1"},
			map[string]string{"BRAIN_CONNECTOR_TOKEN": token},
			"--server must use wss://",
		},
		"target from the environment": {
			[]string{"--token", token, "--server", "ws://127.0.0.1:1"},
			map[string]string{"BRAIN_CONNECTOR_TARGET": "db:5432"},
			"--server must use wss://",
		},
		"server from the environment": {
			[]string{"--token", token, "--target", "db:5432"},
			map[string]string{"BRAIN_CONNECTOR_SERVER": "ws://127.0.0.1:1"},
			"--server must use wss://",
		},
		"a flag wins over the environment": {
			[]string{"--token", "not-a-token", "--target", "db:5432"},
			map[string]string{"BRAIN_CONNECTOR_TOKEN": token},
			"--token is not a connector token",
		},
		"no target at all": {
			[]string{"--token", token},
			nil,
			"--target must be host:port",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, environment(tc.env), &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("exit %d, stderr %q, want %q", code, stderr.String(), tc.want)
			}
		})
	}
}
