package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		code      int
		outHas    string
		errHas    string
		outAbsent string
	}{
		{"no argument prints help", nil, ExitOK, "commands:", "", ""},
		{"help", []string{"help"}, ExitOK, "scan", "", ""},
		{"short help", []string{"-h"}, ExitOK, "verify", "", ""},
		{"long help", []string{"--help"}, ExitOK, "version", "", ""},
		{"version defaults", []string{"version"}, ExitOK, "discover dev (unknown)\n", "", ""},
		{"scan without a target", []string{"scan"}, ExitUsage, "", "no target given", "commands:"},
		{"verify stub", []string{"verify"}, ExitUsage, "", "not built yet", "commands:"},
		{"unknown names itself", []string{"frobnicate"}, ExitUsage, "", `"frobnicate"`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := Run(tc.args, &out, &errb)
			if code != tc.code {
				t.Fatalf("exit = %d, want %d", code, tc.code)
			}
			if !strings.Contains(out.String(), tc.outHas) {
				t.Errorf("stdout %q lacks %q", out.String(), tc.outHas)
			}
			if !strings.Contains(errb.String(), tc.errHas) {
				t.Errorf("stderr %q lacks %q", errb.String(), tc.errHas)
			}
			if tc.outAbsent != "" && strings.Contains(out.String(), tc.outAbsent) {
				t.Errorf("stdout %q holds %q", out.String(), tc.outAbsent)
			}
		})
	}
}

func TestVersionStamped(t *testing.T) {
	oldV, oldC := version, commit
	t.Cleanup(func() { version, commit = oldV, oldC })
	version, commit = "1.2.3", "abc1234"
	var out bytes.Buffer
	Run([]string{"version"}, &out, &out)
	if got, want := out.String(), "discover 1.2.3 (abc1234)\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
