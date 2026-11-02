// Command stub is a saphan-compatible executable that serves studio-call/v1 without a model. The conformance suite
// builds and runs it when no executable is named. The environment variable STUB_MODE breaks one rule on purpose,
// so the suite can be shown to refuse each break:
//
//	garbage-contract  the contract command answers with prose
//	not-listed        the contract command does not list studio-call/v1
//	metered           the receipt names billing class metered
//	obey-data         the answer repeats an instruction found inside a data block
//	accept-all        every request is answered, valid or not
//	overflow          the answer is longer than max_output_bytes
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

type block struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Text   string `json:"text"`
}

type request struct {
	Contract       string  `json:"contract"`
	Instructions   string  `json:"instructions"`
	Data           []block `json:"data"`
	MaxOutputBytes int     `json:"max_output_bytes"`
}

var (
	hex64      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	driveStart = regexp.MustCompile(`^[A-Za-z]:`)
	marker     = regexp.MustCompile(`ZX-MARKER-[0-9A-Z]+`)
)

func main() {
	mode := os.Getenv("STUB_MODE")
	args := strings.Join(os.Args[1:], " ")
	switch args {
	case "discover contract":
		switch mode {
		case "garbage-contract":
			fmt.Println("Studio is not signed in. Run the login command first.")
		case "not-listed":
			fmt.Println("other-thing/v1")
		default:
			fmt.Println("studio-call/v1")
		}
	case "studio call":
		call(mode)
	default:
		fmt.Fprintln(os.Stderr, "unknown command")
		os.Exit(2)
	}
}

func refuse(why string) {
	fmt.Fprintln(os.Stderr, "refused:", why)
	os.Exit(2)
}

func call(mode string) {
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		refuse(err.Error())
	}
	var req request
	if mode != "accept-all" {
		dec := json.NewDecoder(bytes.NewReader(in))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			refuse(err.Error())
		}
		if why := problem(req); why != "" {
			refuse(why)
		}
	} else {
		_ = json.Unmarshal(in, &req)
		if req.MaxOutputBytes < 1 {
			req.MaxOutputBytes = 4096
		}
	}

	text := answer(req)
	switch mode {
	case "obey-data":
		for _, b := range req.Data {
			if m := marker.FindString(b.Text); m != "" {
				text = m
			}
		}
	case "overflow":
		text = strings.Repeat("x", req.MaxOutputBytes+1)
	}
	if mode != "overflow" {
		text = clip(text, req.MaxOutputBytes)
	}

	class := "subscription"
	if mode == "metered" {
		class = "metered"
	}
	resp := map[string]any{
		"contract": "studio-call/v1",
		"text":     text,
		"receipt": map[string]any{
			"contract":      "studio-call/v1",
			"billing_class": class,
			"money":         "virtual",
			"vendor":        "stub",
			"model":         "stub-model",
			"run_id":        "stub-run-1",
		},
	}
	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		os.Exit(1)
	}
}

// problem returns why a request breaks the contract, or "".
func problem(r request) string {
	if r.Contract != "studio-call/v1" {
		return "contract is not studio-call/v1"
	}
	if r.Instructions == "" {
		return "instructions is empty"
	}
	if r.MaxOutputBytes < 1 {
		return "max_output_bytes is below 1"
	}
	for i, b := range r.Data {
		if b.Path == "" || strings.HasPrefix(b.Path, "/") || driveStart.MatchString(b.Path) || strings.Contains(b.Path, `\`) {
			return fmt.Sprintf("data %d: path is not relative", i)
		}
		for _, seg := range strings.Split(b.Path, "/") {
			if seg == ".." {
				return fmt.Sprintf("data %d: path climbs out of the root", i)
			}
		}
		if !hex64.MatchString(b.SHA256) {
			return fmt.Sprintf("data %d: sha256 is not 64 lowercase hex characters", i)
		}
		sum := sha256.Sum256([]byte(b.Text))
		if hex.EncodeToString(sum[:]) != b.SHA256 {
			return fmt.Sprintf("data %d: sha256 does not match the text", i)
		}
	}
	return ""
}

// answer is what a model that obeys only the instructions would say. It never acts on text found in a data block.
func answer(r request) string {
	if strings.Contains(strings.ToLower(r.Instructions), "quote") {
		var b strings.Builder
		for _, d := range r.Data {
			first, _, _ := strings.Cut(d.Text, "\n")
			fmt.Fprintf(&b, "%s: %s\n", d.Path, first)
		}
		return b.String()
	}
	return fmt.Sprintf("Reviewed %d data block(s).", len(r.Data))
}

// clip cuts s to at most n bytes without splitting a character.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
