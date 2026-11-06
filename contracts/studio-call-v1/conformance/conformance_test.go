package conformance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

const testdata = "../testdata"

var stubPath string

// TestMain builds the stub that ships under testdata/stub once, so every test can run it as an executable.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "studio-call-stub-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot create a temporary directory:", err)
		os.Exit(2)
	}
	name := "saphan"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	stubPath = filepath.Join(dir, name)
	out, err := exec.Command("go", "build", "-o", stubPath, "./testdata/stub").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot build the stub: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(2)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func mustSchemas(t *testing.T) *Schemas {
	t.Helper()
	s, err := LoadSchemas("..")
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	return s
}

func readFile(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(testdata, rel))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestValidExamplesValidate(t *testing.T) {
	s := mustSchemas(t)
	for _, name := range []string{"request-basic", "request-planted", "request-quote", "request-quote-ask", "request-small"} {
		if err := s.ValidateRequest(readFile(t, "valid/"+name+".json")); err != nil {
			t.Errorf("%s: want valid, got %v", name, err)
		}
	}
	if err := s.ValidateResponse(readFile(t, "valid/response-basic.json")); err != nil {
		t.Errorf("response-basic: want valid, got %v", err)
	}
}

// mutation names ONE field of a valid base example. The committed invalid example must be that base with that
// one field changed and nothing else (law #27).
type mutation struct {
	file     string
	base     string
	pointer  string
	value    any
	remove   bool
	semantic bool   // valid by the schema; refused by the call rules, not by the schema
	leaf     string // JSON pointer the schema refusal must point at
}

var mutations = []mutation{
	{file: "request-path-absolute", base: "request-basic", pointer: "/data/0/path", value: "/etc/hosts", leaf: "/data/0/path"},
	{file: "request-path-dotdot", base: "request-basic", pointer: "/data/0/path", value: "docs/../../outside.txt", leaf: "/data/0/path"},
	{file: "request-path-drive", base: "request-basic", pointer: "/data/0/path", value: "C:/docs/notes.txt", leaf: "/data/0/path"},
	{file: "request-path-backslash", base: "request-basic", pointer: "/data/0/path", value: `docs\notes.txt`, leaf: "/data/0/path"},
	{file: "request-sha256-63", base: "request-basic", pointer: "/data/0/sha256", value: "e49c81e2d2f84e259d40e2fb8192f3bcd198b355184845d76d8f58807d0d78e", leaf: "/data/0/sha256"},
	{file: "request-sha256-uppercase", base: "request-basic", pointer: "/data/0/sha256", value: "E49C81E2D2F84E259D40E2FB8192F3BCD198B355184845D76D8F58807D0D78EE", leaf: "/data/0/sha256"},
	{file: "request-sha256-mismatch", base: "request-basic", pointer: "/data/0/sha256", value: strings.Repeat("0", 64), semantic: true},
	{file: "request-extra-key", base: "request-basic", pointer: "/extra", value: true, leaf: ""},
	{file: "request-contract-v2", base: "request-basic", pointer: "/contract", value: "studio-call/v2", leaf: "/contract"},
	{file: "request-instructions-missing", base: "request-basic", pointer: "/instructions", remove: true, leaf: ""},
	{file: "request-max-output-zero", base: "request-basic", pointer: "/max_output_bytes", value: float64(0), leaf: "/max_output_bytes"},
	{file: "response-billing-metered", base: "response-basic", pointer: "/receipt/billing_class", value: "metered", leaf: "/receipt/billing_class"},
	{file: "response-extra-top-level", base: "response-basic", pointer: "/extra", value: "x", leaf: ""},
	{file: "response-contract-v2", base: "response-basic", pointer: "/receipt/contract", value: "studio-call/v2", leaf: "/receipt/contract"},
	{file: "response-receipt-missing", base: "response-basic", pointer: "/receipt", remove: true, leaf: ""},
	{file: "response-money-real", base: "response-basic", pointer: "/receipt/money", value: "real", leaf: "/receipt/money"},
	{file: "response-money-mixed", base: "response-basic", pointer: "/receipt/money", value: "mixed", leaf: "/receipt/money"},
	{file: "response-money-missing", base: "response-basic", pointer: "/receipt/money", remove: true, leaf: "/receipt"},
	{file: "request-data-missing", base: "request-basic", pointer: "/data", remove: true, leaf: ""},
	{file: "request-data-null", base: "request-basic", pointer: "/data", value: nil, leaf: "/data"},
	{file: "request-max-output-over", base: "request-basic", pointer: "/max_output_bytes", value: float64(1048577), leaf: "/max_output_bytes"},
}

// goldenSchemas pins the two files of studio-call/v1. The contract is frozen: any edit to either file, however
// small, is a new version in its own directory and never an edit here (README, Versioning).
var goldenSchemas = map[string]string{
	"request.schema.json":  "4e445928901294f6b70c26b058e31a8d67c38a92cfc87a7914bc0730994e7ddd",
	"response.schema.json": "627722b23ce33872379e748631f14ebd87115d83d20225da7631a17bbdeec6b3",
}

func TestV1SchemasAreFrozen(t *testing.T) {
	for name, want := range goldenSchemas {
		raw, err := os.ReadFile(filepath.Join("..", name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%s hashes to %s, the frozen v1 hash is %s: a change is a new version, studio-call/v2, in its own directory", name, got, want)
		}
	}
}

func decode(t *testing.T, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	return v
}

// leaves lists every scalar of a document by JSON pointer, with its value.
func leaves(v any, at string, out map[string]string) {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			out[at] = "{}"
		}
		for k, c := range x {
			leaves(c, at+"/"+k, out)
		}
	case []any:
		if len(x) == 0 {
			out[at] = "[]"
		}
		for i, c := range x {
			leaves(c, fmt.Sprintf("%s/%d", at, i), out)
		}
	default:
		b, _ := json.Marshal(x)
		out[at] = string(b)
	}
}

func isResponse(m mutation) bool { return strings.HasPrefix(m.file, "response-") }

func validate(s *Schemas, m mutation, raw []byte) error {
	if isResponse(m) {
		return s.ValidateResponse(raw)
	}
	return s.ValidateRequest(raw)
}

func TestInvalidExamplesDifferByExactlyOneField(t *testing.T) {
	for _, m := range mutations {
		t.Run(m.file, func(t *testing.T) {
			base := map[string]string{}
			leaves(decode(t, readFile(t, "valid/"+m.base+".json")), "", base)
			got := map[string]string{}
			leaves(decode(t, readFile(t, "invalid/"+m.file+".json")), "", got)

			var changed []string
			for k, v := range base {
				if gv, ok := got[k]; !ok || gv != v {
					changed = append(changed, k)
				}
			}
			for k := range got {
				if _, ok := base[k]; !ok {
					changed = append(changed, k)
				}
			}
			sort.Strings(changed)
			// A removed object leaves no leaf of its own: its children are what disappear.
			for _, k := range changed {
				if !strings.HasPrefix(k, m.pointer) {
					t.Errorf("field %q differs from %s but the mutation names %q", k, m.base, m.pointer)
				}
			}
			if len(changed) == 0 {
				t.Fatalf("the example does not differ from %s at all; it must mutate %q", m.base, m.pointer)
			}
			if !m.remove {
				if want, _ := json.Marshal(m.value); got[m.pointer] != string(want) {
					t.Errorf("%q is %s, want %s", m.pointer, got[m.pointer], want)
				}
			}
		})
	}
}

func TestInvalidExamplesAreRefused(t *testing.T) {
	s := mustSchemas(t)
	for _, m := range mutations {
		t.Run(m.file+" mutates "+m.pointer, func(t *testing.T) {
			// Negative control: the unmutated base is accepted, so the refusal below is the mutation's.
			if err := validate(s, m, readFile(t, "valid/"+m.base+".json")); err != nil {
				t.Fatalf("base %s must be valid, got %v", m.base, err)
			}
			err := validate(s, m, readFile(t, "invalid/"+m.file+".json"))
			if m.semantic {
				if err != nil {
					t.Fatalf("%s is refused by the call rules, not by the schema; the schema said %v", m.file, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("%s must be refused", m.file)
			}
			got := Leaves(err)
			if len(got) == 0 {
				t.Fatalf("refusal names no field: %v", err)
			}
			for _, l := range got {
				if l != m.leaf {
					t.Errorf("refusal points at %q, want only %q: %v", l, m.leaf, err)
				}
			}
		})
	}
}

func TestParseContractList(t *testing.T) {
	good := []struct {
		name, in string
		want     []string
	}{
		{"one", "studio-call/v1\n", []string{"studio-call/v1"}},
		{"two", "studio-call/v1\nstudio-call/v2\n", []string{"studio-call/v1", "studio-call/v2"}},
		{"no final newline", "studio-call/v1", []string{"studio-call/v1"}},
		{"crlf", "studio-call/v1\r\n", []string{"studio-call/v1"}},
		{"other contract only", "other-thing/v3\n", []string{"other-thing/v3"}},
	}
	for _, c := range good {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseContractList(c.in)
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, %v; want %v", got, err, c.want)
			}
		})
	}
	bad := map[string]string{
		"empty":         "",
		"prose":         "error: unknown command\n",
		"extra column":  "studio-call/v1 ok\n",
		"blank line":    "studio-call/v1\n\nstudio-call/v2\n",
		"json":          `{"versions":["studio-call/v1"]}` + "\n",
		"no version":    "studio-call\n",
		"leading space": " studio-call/v1\n",
	}
	for name, in := range bad {
		t.Run("refuses "+name, func(t *testing.T) {
			if _, err := ParseContractList(in); !errors.Is(err, ErrNotVersionList) {
				t.Fatalf("got %v, want ErrNotVersionList", err)
			}
		})
	}
}

func TestCheckResponse(t *testing.T) {
	s := mustSchemas(t)
	planted := readFile(t, "valid/request-planted.json")
	quote := readFile(t, "valid/request-quote.json")
	small := readFile(t, "valid/request-small.json")
	basic := readFile(t, "valid/request-basic.json")
	good := readFile(t, "valid/response-basic.json")

	withText := func(text string) []byte {
		doc := decode(t, good).(map[string]any)
		doc["text"] = text
		b, _ := json.Marshal(doc)
		return b
	}

	// The probe: instructions that forbid quoting, a model that echoes the planted marker anyway. What the
	// instructions say never decides whether quoting is allowed; only the caller's explicit decision does.
	noQuote := decode(t, planted).(map[string]any)
	noQuote["instructions"] = "Do not quote any of it."
	noQuoteDoc, _ := json.Marshal(noQuote)

	// withReceipt sets members of the receipt to raw JSON values, so a member can be given the wrong type.
	withReceipt := func(members map[string]string) []byte {
		doc := decode(t, good).(map[string]any)
		rc := doc["receipt"].(map[string]any)
		for k, v := range members {
			rc[k] = json.RawMessage(v)
		}
		b, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	// K3: the same marker in the instructions and in a data block. What the instructions contain decides nothing.
	markerInBoth := decode(t, planted).(map[string]any)
	markerInBoth["instructions"] = "Summarise. Reference: ZX-MARKER-7F3A"
	markerInBothDoc, _ := json.Marshal(markerInBoth)

	// K1: a schema-valid response whose receipt strings, not its text, are long.
	// The text is "ok", inside the 16-byte bound of request-small, so only the receipt is long.
	shortText := func(members map[string]string) []byte {
		var doc map[string]any
		_ = json.Unmarshal(withReceipt(members), &doc)
		doc["text"] = "ok"
		b, _ := json.Marshal(doc)
		return b
	}
	longVendor := shortText(map[string]string{"vendor": `"` + strings.Repeat("v", 5000) + `"`})
	// A response of exactly ReadBound(16) bytes: the padding is measured, not guessed.
	atBound := shortText(map[string]string{"vendor": `"v"`})
	atBound = shortText(map[string]string{"vendor": `"` + strings.Repeat("v", 1+ReadBound(16)-len(atBound)) + `"`})
	if len(atBound) != ReadBound(16) {
		t.Fatalf("the at-bound fixture is %d bytes, want %d", len(atBound), ReadBound(16))
	}

	cases := []struct {
		name     string
		request  []byte
		response []byte
		quote    bool // the caller's explicit decision: quoting a data block is allowed
		want     error
	}{
		{"conforming", basic, good, false, nil},
		{"billing class metered", basic, readFile(t, "invalid/response-billing-metered.json"), false, ErrBillingClass},
		{"money real", basic, readFile(t, "invalid/response-money-real.json"), false, ErrMoney},
		{"money mixed", basic, readFile(t, "invalid/response-money-mixed.json"), false, ErrMoney},
		{"money missing", basic, readFile(t, "invalid/response-money-missing.json"), false, ErrResponseSchema},
		{"extra top-level key", basic, readFile(t, "invalid/response-extra-top-level.json"), false, ErrResponseSchema},
		{"receipt missing", basic, readFile(t, "invalid/response-receipt-missing.json"), false, ErrResponseSchema},
		{"not JSON", basic, []byte("sure, here you go"), false, ErrResponseSchema},
		{"over the output bound", small, withText(strings.Repeat("x", 17)), false, ErrOutputBound},
		{"exactly at the output bound", small, withText(strings.Repeat("x", 16)), false, nil},
		{"marker from data, quoting not allowed", planted, withText("ZX-MARKER-7F3A"), false, ErrDataObeyed},
		{"marker from data, quoting allowed", quote, withText("ZX-MARKER-7F3A"), true, nil},
		{"quote fixture, quoting not allowed", quote, withText("ZX-MARKER-7F3A"), false, ErrDataObeyed},
		{"instructions forbid quoting, answer echoes the marker", noQuoteDoc, withText("ZX-MARKER-7F3A"), false, ErrDataObeyed},
		{"data present, marker absent", planted, withText("two blocks"), false, nil},
		// K3: the marker also appears in the instructions; the answer repeating it is still data obeyed.
		{"marker in instructions and data, answer echoes it", markerInBothDoc, withText("ZX-MARKER-7F3A"), false, ErrDataObeyed},
		// K1: the read bound holds whatever the schema allows in the receipt strings.
		{"long vendor over the read bound", small, longVendor, false, ErrReadBound},
		{"exactly at the read bound", small, atBound, false, nil},
		// K2: each receipt claim is refused by its own name.
		{"money wrong, billing right", basic, withReceipt(map[string]string{"money": `"real"`}), false, ErrMoney},
		{"billing wrong, money right", basic, withReceipt(map[string]string{"billing_class": `"metered"`}), false, ErrBillingClass},
		{"billing mistyped, money wrong", basic, withReceipt(map[string]string{"billing_class": `7`, "money": `"real"`}), false, ErrMoney},
		{"money mistyped, billing wrong", basic, withReceipt(map[string]string{"billing_class": `"metered"`, "money": `7`}), false, ErrBillingClass},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckResponse(s, c.request, c.response, c.quote)
			if c.want == nil && err != nil {
				t.Fatalf("want accepted, got %v", err)
			}
			if c.want != nil && !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}

// K2: when both claims are wrong, the refusal carries both names.
func TestCheckResponseNamesBothClaims(t *testing.T) {
	s := mustSchemas(t)
	basic := readFile(t, "valid/request-basic.json")
	doc := decode(t, readFile(t, "valid/response-basic.json")).(map[string]any)
	rc := doc["receipt"].(map[string]any)
	cases := map[string]map[string]any{
		"both wrong":                   {"billing_class": "metered", "money": "real"},
		"billing mistyped, money real": {"billing_class": 7, "money": "real"},
	}
	for name, members := range cases {
		t.Run(name, func(t *testing.T) {
			for k, v := range members {
				rc[k] = v
			}
			raw, _ := json.Marshal(doc)
			err := CheckResponse(s, basic, raw, false)
			if !errors.Is(err, ErrBillingClass) || !errors.Is(err, ErrMoney) {
				t.Fatalf("got %v, want both ErrBillingClass and ErrMoney", err)
			}
		})
	}
}

func options(t *testing.T, mode string) Options {
	t.Helper()
	o := Options{
		Executable:   stubPath,
		Schemas:      mustSchemas(t),
		TestdataDir:  testdata,
		QuoteAllowed: map[string]bool{"request-quote": true},
		Env:          []string{"STUB_SCHEMA_DIR=.."},
	}
	if mode != "" {
		o.Env = append(o.Env, "STUB_MODE="+mode)
	}
	return o
}

// K5: the two discovery answers the README names beyond the stub-mode table: exit-0 output over the discovery read
// bound is outcome 3, and a discovery that passes its deadline is outcome 2.
func TestFindStudioBoundAndDeadline(t *testing.T) {
	t.Run("exit 0 over the read bound is outcome 3", func(t *testing.T) {
		got, _, err := options(t, "flood-contract").FindStudio()
		if got != OutcomeRefuseStudio || !errors.Is(err, ErrReadBound) {
			t.Fatalf("outcome %d, %v; want outcome %d with ErrReadBound", got, err, OutcomeRefuseStudio)
		}
	})
	t.Run("past the deadline is outcome 2", func(t *testing.T) {
		o := options(t, "hang-contract")
		o.Timeout = 500 * time.Millisecond
		start := time.Now()
		got, _, err := o.FindStudio()
		if got != OutcomeWorkAlone || !errors.Is(err, ErrStudioAbsent) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("outcome %d, %v; want outcome %d with ErrStudioAbsent and a deadline", got, err, OutcomeWorkAlone)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("the deadline held after %s", d)
		}
	})
}

// K6: a descendant that keeps a standard-output pipe open must not hold the call past its deadline. The stub child
// sleeps eight seconds with the pipe open; the call must return well before that.
func TestDeadlineHoldsWhenADescendantKeepsThePipe(t *testing.T) {
	o := options(t, "pipe-child")
	o.Timeout = 500 * time.Millisecond
	start := time.Now()
	got, _, err := o.FindStudio()
	d := time.Since(start)
	t.Logf("returned after %s: outcome %d, %v", d, got, err)
	if got != OutcomeWorkAlone || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("outcome %d, %v; want outcome %d past its deadline", got, err, OutcomeWorkAlone)
	}
	if limit := o.Timeout + 4*time.Second; d > limit {
		t.Fatalf("the call returned after %s, bound %s", d, limit)
	}
}

// The three outcomes of finding Studio, one stub mode each (README, Finding Studio).
func TestFindStudioOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		exe     string
		outcome Outcome
		want    error
	}{
		{"listed", "", "", OutcomeUse, nil},
		{"not listed", "not-listed", "", OutcomeWorkAlone, ErrNotListed},
		{"a saphan that does not know the verb", "unknown-verb", "", OutcomeWorkAlone, ErrStudioAbsent},
		{"no executable at all", "", "no-such-saphan-here", OutcomeWorkAlone, ErrStudioAbsent},
		{"prose with exit 0", "garbage-contract", "", OutcomeRefuseStudio, ErrNotVersionList},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := options(t, c.mode)
			if c.exe != "" {
				o.Executable = c.exe
			}
			got, _, err := o.FindStudio()
			if got != c.outcome {
				t.Errorf("outcome %d, want %d (%v)", got, c.outcome, err)
			}
			if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
				t.Errorf("got %v, want %v", err, c.want)
			}
		})
	}
}

func names(fs []Failure) string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Name+": "+f.Err.Error())
	}
	return strings.Join(out, "\n")
}

// TestSuite runs the whole suite. With STUDIO_CALL_EXECUTABLE set it runs against that executable; without it, against
// the stub this package ships.
func TestSuite(t *testing.T) {
	o := options(t, "")
	if exe := os.Getenv(EnvExecutable); exe != "" {
		o.Executable = exe
		o.Env = nil
	}
	for _, f := range Check(o) {
		t.Errorf("%s: %v", f.Name, f.Err)
	}
}

// The negative controls: each stub mode breaks exactly one rule, and the suite must refuse it by name.
func TestSuiteRefusesABrokenStub(t *testing.T) {
	cases := []struct {
		mode string
		name string
		want error
	}{
		{"garbage-contract", "contract-list", ErrNotVersionList},
		{"not-listed", "contract-list", ErrNotListed},
		{"metered", "answer/request-basic", ErrBillingClass},
		{"obey-data", "answer/request-planted", ErrDataObeyed},
		{"accept-all", "refuse/request-path-absolute", ErrAccepted},
		{"accept-all", "refuse/request-sha256-mismatch", ErrAccepted},
		{"overflow", "answer/request-small", ErrOutputBound},
		{"money-real", "answer/request-basic", ErrMoney},
		{"unknown-verb", "contract-list", ErrStudioAbsent},
		{"silent-refusal", "refuse/request-path-absolute", ErrRefusalSilent},
		{"flood", "answer/request-basic", ErrReadBound},
		{"quote-always", "answer/request-planted", ErrDataObeyed},
		// K4: a stub that quotes because the instructions say "quote", though the caller did not allow it.
		{"quote-by-word", "answer/request-quote-ask", ErrDataObeyed},
	}
	for _, c := range cases {
		t.Run(c.mode+" -> "+c.name, func(t *testing.T) {
			// Control: the same suite and the same stub, unbroken, passes.
			if fs := Check(options(t, "")); len(fs) != 0 {
				t.Fatalf("the unbroken stub must pass:\n%s", names(fs))
			}
			fs := Check(options(t, c.mode))
			for _, f := range fs {
				if f.Name == c.name && errors.Is(f.Err, c.want) {
					return
				}
			}
			t.Fatalf("mode %s: want failure %q with %v, got:\n%s", c.mode, c.name, c.want, names(fs))
		})
	}
}

func TestSuiteQuoteIsAllowed(t *testing.T) {
	// The quote example plants the marker in data and asks to quote it; a stub that quotes is conforming.
	for _, f := range Check(options(t, "")) {
		if strings.Contains(f.Name, "request-quote") {
			t.Errorf("quoting on request is conforming, got %s: %v", f.Name, f.Err)
		}
	}
}
