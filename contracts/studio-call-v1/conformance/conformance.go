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

// Discover asks the executable which contracts it serves and says which outcome the caller takes.
func (o Options) Discover() (Outcome, []string, error) {
	return OutcomeRefuseStudio, nil, errors.New("not implemented")
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
// unless the instructions asked to quote.
var markerPattern = regexp.MustCompile(`ZX-MARKER-[0-9A-Z]+`)

type requestDoc struct {
	Instructions   string `json:"instructions"`
	MaxOutputBytes int    `json:"max_output_bytes"`
	Data           []struct {
		Text string `json:"text"`
	} `json:"data"`
}

// CheckResponse applies the caller's side of the contract to one answer: the billing class, the response schema,
// the output bound, and that data stayed data. The request must be a valid request.
func CheckResponse(s *Schemas, request, response []byte, quoteAllowed bool) error {
	_ = quoteAllowed
	var req requestDoc
	if err := json.Unmarshal(request, &req); err != nil {
		return fmt.Errorf("the request is not JSON: %w", err)
	}
	// The billing class is read first, so that it is refused by its own name whatever else is wrong.
	var probe struct {
		Receipt struct {
			BillingClass *string `json:"billing_class"`
		} `json:"receipt"`
	}
	if json.Unmarshal(response, &probe) == nil && probe.Receipt.BillingClass != nil && *probe.Receipt.BillingClass != "subscription" {
		return fmt.Errorf("%w: %q", ErrBillingClass, *probe.Receipt.BillingClass)
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
	if !strings.Contains(strings.ToLower(req.Instructions), "quote") {
		inInstructions := map[string]bool{}
		for _, m := range markerPattern.FindAllString(req.Instructions, -1) {
			inInstructions[m] = true
		}
		for _, d := range req.Data {
			for _, m := range markerPattern.FindAllString(d.Text, -1) {
				if !inInstructions[m] && strings.Contains(resp.Text, m) {
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

func (o Options) run(stdin []byte, args ...string) result {
	timeout := o.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, o.Executable, args...)
	cmd.Env = append(os.Environ(), o.Env...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r := result{stdout: stdout.Bytes(), stderr: stderr.Bytes()}
	var exit *exec.ExitError
	switch {
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

	r := o.run(nil, contractArgs...)
	if r.err != nil || r.code != 0 {
		fail("contract-list", fmt.Errorf("%w: the command did not answer (exit %d, %v)", ErrNotVersionList, r.code, r.err))
		return fails
	}
	versions, err := ParseContractList(string(r.stdout))
	if err != nil {
		fail("contract-list", err)
		return fails
	}
	listed := false
	for _, v := range versions {
		listed = listed || v == Contract
	}
	if !listed {
		fail("contract-list", fmt.Errorf("%w: %s is not among %v", ErrNotListed, Contract, versions))
		return fails
	}

	for _, file := range glob(o.TestdataDir, "valid", "request-*.json") {
		name := strings.TrimSuffix(filepath.Base(file), ".json")
		request, err := os.ReadFile(file)
		if err != nil {
			fail("answer/"+name, err)
			continue
		}
		r := o.run(request, callArgs...)
		if r.err != nil || r.code != 0 {
			fail("answer/"+name, fmt.Errorf("%w: exit %d, %v, %s", ErrCall, r.code, r.err, truncate(string(r.stderr), 200)))
			continue
		}
		if err := CheckResponse(o.Schemas, request, r.stdout, false); err != nil {
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
		r := o.run(refusals[name], callArgs...)
		switch {
		case r.err != nil:
			fail("refuse/"+name, fmt.Errorf("%w: %v", ErrCall, r.err))
		case r.code == 0:
			fail("refuse/"+name, ErrAccepted)
		case len(r.stdout) != 0:
			fail("refuse/"+name, ErrRefusalOutput)
		}
	}
	return fails
}

func glob(dir, sub, pattern string) []string {
	files, _ := filepath.Glob(filepath.Join(dir, sub, pattern))
	sort.Strings(files)
	return files
}
