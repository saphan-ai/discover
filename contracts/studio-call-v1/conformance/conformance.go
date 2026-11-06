// Package conformance is the suite any implementation of studio-call/v1, or a stub of it, runs against itself.
//
// Point STUDIO_CALL_EXECUTABLE at a saphan-compatible executable and run `go test` in this directory. Without
// that variable the suite runs against the stub in testdata/stub.
package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// EnvExecutable names the environment variable that holds the path of the executable under test.
const EnvExecutable = "STUDIO_CALL_EXECUTABLE"

// Contract is the version this suite checks.
const Contract = "studio-call/v1"

// The two commands the suite runs on the executable. The first lists the contract versions it serves, one per
// line. The second answers one call: the request is JSON on standard input, the response is JSON on standard
// output, and a request it refuses ends with a non-zero exit code and nothing on standard output.
var (
	contractArgs = []string{"discover", "contract"}
	callArgs     = []string{"studio", "call"}
)

// The names of the refusals. A caller or the suite reports a failure by one of these.
var (
	ErrNotVersionList = errors.New("the answer is not a list of contract versions")
	ErrNotListed      = errors.New("the contract is not listed")
	ErrCall           = errors.New("the call failed")
	ErrResponseSchema = errors.New("the response does not match the response schema")
	ErrBillingClass   = errors.New("the receipt names a billing class other than subscription")
	ErrOutputBound    = errors.New("the response text is longer than max_output_bytes")
	ErrDataObeyed     = errors.New("the response carries text that only a data block held: data was treated as instructions")
	ErrAccepted       = errors.New("a request that breaks the contract was accepted")
	ErrRefusalOutput  = errors.New("a refusal wrote a response")
	ErrMoney          = errors.New("the receipt does not say money is virtual")
	ErrStudioAbsent   = errors.New("Studio is not found or too old")
	ErrRefusalSilent  = errors.New("a refusal gave no reason on standard error")
	ErrReadBound      = errors.New("the output is longer than the caller's read bound")
)

// Outcome is what a caller does after it asked a saphan executable which contracts it serves.
type Outcome int

const (
	OutcomeUse          Outcome = 1 // the contract is listed: use it, when the user asked for it
	OutcomeWorkAlone    Outcome = 2 // Studio is absent or too old: the caller works alone
	OutcomeRefuseStudio Outcome = 3 // the answer is not a version list: the caller refuses Studio by name
)

// FindStudio asks the executable which contracts it serves and says which outcome the caller takes (README, Finding
// Studio). No executable, or one that exits non-zero because it does not know the verb, is outcome 2. Exit 0 with an
// answer that is not a version list is outcome 3, and so is an exit-0 answer longer than the discovery read bound. A
// command that passes its deadline is outcome 2, as is a list without studio-call/v1.
func (o Options) FindStudio() (Outcome, []string, error) {
	r := o.run(nil, 64<<10, nil, contractArgs...)
	if errors.Is(r.err, ErrReadBound) {
		// The command answered with more than a version list can be: that is an answer that is not a list.
		return OutcomeRefuseStudio, nil, fmt.Errorf("%w: the answer passes the %d-byte discovery bound", ErrReadBound, 64<<10)
	}
	if r.err != nil || r.code != 0 {
		return OutcomeWorkAlone, nil, fmt.Errorf("%w: the command did not answer (exit %d, %w)", ErrStudioAbsent, r.code, r.err)
	}
	versions, err := ParseContractList(string(r.stdout))
	if err != nil {
		return OutcomeRefuseStudio, nil, err
	}
	for _, v := range versions {
		if v == Contract {
			return OutcomeUse, versions, nil
		}
	}
	return OutcomeWorkAlone, versions, fmt.Errorf("%w: %s is not among %v", ErrNotListed, Contract, versions)
}

// Schemas holds the compiled request and response schemas.
type Schemas struct {
	request  *jsonschema.Schema
	response *jsonschema.Schema
}

// LoadSchemas compiles request.schema.json and response.schema.json found in dir.
func LoadSchemas(dir string) (*Schemas, error) {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	var out [2]*jsonschema.Schema
	for i, name := range []string{"request.schema.json", "response.schema.json"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		if err := c.AddResource(name, bytes.NewReader(raw)); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if out[i], err = c.Compile(name); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	return &Schemas{request: out[0], response: out[1]}, nil
}

func validateWith(s *jsonschema.Schema, raw []byte) error {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("not JSON: %w", err)
	}
	if dec.More() {
		return errors.New("not JSON: more than one document")
	}
	return s.Validate(v)
}

// ValidateRequest checks a request document against the request schema.
func (s *Schemas) ValidateRequest(raw []byte) error { return validateWith(s.request, raw) }

// ValidateResponse checks a response document against the response schema.
func (s *Schemas) ValidateResponse(raw []byte) error { return validateWith(s.response, raw) }

// Leaves returns the JSON pointers of the document locations a schema refusal points at, sorted, one each.
func Leaves(err error) []string {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return nil
	}
	seen := map[string]bool{}
	var walk func(*jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			seen[e.InstanceLocation] = true
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	var out []string
	for l := range seen {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

var versionLine = regexp.MustCompile(`^[a-z][a-z0-9-]*/v[1-9][0-9]*$`)

// ParseContractList reads the output of the contract command: one contract version per line, nothing else. An
// answer that is not exactly that is refused with ErrNotVersionList; it is never guessed at.
func ParseContractList(out string) ([]string, error) {
	out = strings.TrimSuffix(out, "\n")
	if out == "" {
		return nil, fmt.Errorf("%w: the answer is empty", ErrNotVersionList)
	}
	var versions []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !versionLine.MatchString(line) {
			return nil, fmt.Errorf("%w: %q is not a contract version", ErrNotVersionList, truncate(line, 40))
		}
		versions = append(versions, line)
	}
	return versions, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// markerPattern is the form of the marker the suite plants in a data block. A conforming call never repeats it
// unless the caller explicitly allowed quoting a data block.
var markerPattern = regexp.MustCompile(`ZX-MARKER-[0-9A-Z]+`)

type requestDoc struct {
	Instructions   string `json:"instructions"`
	MaxOutputBytes int    `json:"max_output_bytes"`
	Data           []struct {
		Text string `json:"text"`
	} `json:"data"`
}

// CheckResponse applies the caller's side of the contract to one answer: the billing class, the money claim, the
// response schema, the output bound, and that data stayed data. The request must be a valid request.
//
// quoteAllowed is the caller's own explicit decision that the answer may repeat text of a data block. No decision is
// derived from the instructions, neither from their words nor from their content: a planted marker that also
// appears in the instructions is still data when the answer repeats it. The data check sees only the suite's planted
// ZX-MARKER- markers, so on real data it gives a caller no protection.
func CheckResponse(s *Schemas, request, response []byte, quoteAllowed bool) error {
	var req requestDoc
	if err := json.Unmarshal(request, &req); err != nil {
		return fmt.Errorf("the request is not JSON: %w", err)
	}
	// A caller reads at most ReadBound bytes. The frozen schema puts no length on the receipt strings, so v1 makes the
	// envelope (everything but text) a Studio obligation of 4096 bytes (README, Calling): a larger answer is refused.
	if bound := ReadBound(req.MaxOutputBytes); len(response) > bound {
		return fmt.Errorf("%w: %d bytes, bound %d", ErrReadBound, len(response), bound)
	}
	// The billing class and the money claim are each probed on their own, so that each is refused by its own name
	// whatever else is wrong, and both names are carried when both are wrong. A money claim that is present and is
	// not exactly "virtual" claims that money was spent; a billing class that is present and is not the string
	// "subscription" is not the subscription seat.
	var top map[string]json.RawMessage
	var receipt map[string]json.RawMessage
	if json.Unmarshal(response, &top) == nil && json.Unmarshal(top["receipt"], &receipt) == nil {
		var named []error
		if raw, ok := receipt["billing_class"]; ok {
			var class string
			if json.Unmarshal(raw, &class) != nil || class != "subscription" {
				named = append(named, fmt.Errorf("%w: %s", ErrBillingClass, truncate(string(raw), 40)))
			}
		}
		if raw, ok := receipt["money"]; ok && string(raw) != `"virtual"` {
			named = append(named, fmt.Errorf("%w: the response claims money %s", ErrMoney, truncate(string(raw), 40)))
		}
		if len(named) > 0 {
			return errors.Join(named...)
		}
	}
	if err := s.ValidateResponse(response); err != nil {
		return fmt.Errorf("%w: %v", ErrResponseSchema, err)
	}
	var resp struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(response, &resp); err != nil {
		return fmt.Errorf("%w: %v", ErrResponseSchema, err)
	}
	if len(resp.Text) > req.MaxOutputBytes {
		return fmt.Errorf("%w: %d bytes, bound %d", ErrOutputBound, len(resp.Text), req.MaxOutputBytes)
	}
	if !quoteAllowed {
		for _, d := range req.Data {
			for _, m := range markerPattern.FindAllString(d.Text, -1) {
				if strings.Contains(resp.Text, m) {
					return fmt.Errorf("%w: %s", ErrDataObeyed, m)
				}
			}
		}
	}
	return nil
}

// Options says what to run the suite against.
type Options struct {
	Executable   string          // path of the executable under test
	Env          []string        // extra environment entries, KEY=VALUE
	Schemas      *Schemas        // the compiled schemas
	TestdataDir  string          // the directory that holds valid/ and invalid/
	QuoteAllowed map[string]bool // fixture name to whether the caller allows quoting a data block
	Timeout      time.Duration   // per command; two minutes when zero
}

// Failure is one check that did not hold: its name and the named refusal.
type Failure struct {
	Name string
	Err  error
}

type result struct {
	stdout, stderr []byte
	code           int
	err            error // the command could not run at all
}

// limitWriter keeps at most max bytes. The first byte beyond that stops the command and marks the write as over.
type limitWriter struct {
	buf    bytes.Buffer
	max    int
	over   bool
	cancel context.CancelFunc
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.max {
		w.over = true
		w.cancel()
		return 0, ErrReadBound
	}
	return w.buf.Write(p)
}

// maxEnvelope is what a JSON response may add around the text, and the most a JSON string escape may multiply
// the text: a control character is six bytes on the wire (\u00XX).
const (
	maxEnvelope  = 4096
	maxEscape    = 6
	defaultBound = 64 << 10
	waitDelay    = time.Second
)

// ReadBound is how many bytes of standard output a caller reads from one call before it refuses the answer.
func ReadBound(maxOutputBytes int) int { return maxOutputBytes*maxEscape + maxEnvelope }

// readBoundFor reads max_output_bytes from a request that has one; any other input gets the default bound.
func readBoundFor(request []byte) int {
	var req requestDoc
	if json.Unmarshal(request, &req) != nil || req.MaxOutputBytes < 1 || req.MaxOutputBytes > 1<<20 {
		return defaultBound
	}
	return ReadBound(req.MaxOutputBytes)
}

// run executes the command. Standard output and standard error are each read up to bound bytes (standard error up
// to the default bound); a command that writes more is stopped and the result carries ErrReadBound.
func (o Options) run(stdin []byte, bound int, extraEnv []string, args ...string) result {
	timeout := o.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, o.Executable, args...)
	cmd.Env = append(append(os.Environ(), o.Env...), extraEnv...)
	cmd.Stdin = bytes.NewReader(stdin)
	stdout := &limitWriter{max: bound, cancel: cancel}
	stderr := &limitWriter{max: defaultBound, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// The context kills the command, not its descendants. A descendant that keeps a pipe open would hold Wait for as
	// long as it lives, so after the deadline Wait gives the pipes this long and then closes them.
	cmd.WaitDelay = waitDelay
	err := cmd.Run()
	r := result{stdout: stdout.buf.Bytes(), stderr: stderr.buf.Bytes()}
	var exit *exec.ExitError
	switch {
	case stdout.over || stderr.over:
		r.err = ErrReadBound
		r.code = -1
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		r.err = fmt.Errorf("the command passed its deadline of %s: %w", timeout, context.DeadlineExceeded)
		r.code = -1
	case errors.As(err, &exit):
		r.code = exit.ExitCode()
	case err != nil:
		r.err = err
		r.code = -1
	}
	return r
}

// Check runs the whole suite and returns every failure; none means the executable conforms.
func Check(o Options) []Failure {
	var fails []Failure
	fail := func(name string, err error) { fails = append(fails, Failure{name, err}) }

	if outcome, _, err := o.FindStudio(); outcome != OutcomeUse {
		fail("contract-list", err)
		return fails
	}

	for _, file := range glob(o.TestdataDir, "valid", "request-*.json") {
		name := strings.TrimSuffix(filepath.Base(file), ".json")
		request, err := os.ReadFile(file)
		if err != nil {
			fail("answer/"+name, err)
			continue
		}
		// The caller's decision on quoting is declared per fixture and handed to the executable as the one signal a
		// stub can read; an executable that is a real model ignores it and decides by the instructions alone.
		quote := o.QuoteAllowed[name]
		var env []string
		if quote {
			env = []string{"STUB_QUOTE=1"}
		}
		r := o.run(request, readBoundFor(request), env, callArgs...)
		if errors.Is(r.err, ErrReadBound) {
			fail("answer/"+name, r.err)
			continue
		}
		if r.err != nil || r.code != 0 {
			fail("answer/"+name, fmt.Errorf("%w: exit %d, %v, %s", ErrCall, r.code, r.err, truncate(string(r.stderr), 200)))
			continue
		}
		if err := CheckResponse(o.Schemas, request, r.stdout, quote); err != nil {
			fail("answer/"+name, err)
		}
	}

	refusals := map[string][]byte{"not-json": []byte("hello"), "empty": nil}
	for _, file := range glob(o.TestdataDir, "invalid", "request-*.json") {
		request, err := os.ReadFile(file)
		if err != nil {
			fail("refuse/"+filepath.Base(file), err)
			continue
		}
		refusals[strings.TrimSuffix(filepath.Base(file), ".json")] = request
	}
	var refusalNames []string
	for n := range refusals {
		refusalNames = append(refusalNames, n)
	}
	sort.Strings(refusalNames)
	for _, name := range refusalNames {
		r := o.run(refusals[name], readBoundFor(refusals[name]), nil, callArgs...)
		switch {
		case errors.Is(r.err, ErrReadBound):
			fail("refuse/"+name, r.err)
		case r.err != nil:
			fail("refuse/"+name, fmt.Errorf("%w: %v", ErrCall, r.err))
		case r.code == 0:
			fail("refuse/"+name, ErrAccepted)
		case len(r.stdout) != 0:
			fail("refuse/"+name, ErrRefusalOutput)
		case len(bytes.TrimSpace(r.stderr)) == 0:
			fail("refuse/"+name, ErrRefusalSilent)
		}
	}
	return fails
}

func glob(dir, sub, pattern string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, sub, pattern))
	sort.Strings(files)
	return files
}
