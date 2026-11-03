// Command stub is a saphan-compatible executable that serves studio-call/v1 without a model. The conformance suite
// builds and runs it when no executable is named.
//
// The stub validates every request against the shipped request.schema.json, read from the directory in
// STUB_SCHEMA_DIR, so it cannot drift from the contract. It quotes the first line of each data block only when
// STUB_QUOTE=1: the suite sets that for a fixture whose caller allows quoting, so the stub never reads the words of
// the instructions to decide.
//
// The environment variable STUB_MODE breaks one rule on purpose, so the suite can be shown to refuse each break:
//
//	garbage-contract  the contract command answers with prose and exits 0
//	not-listed        the contract command does not list studio-call/v1
//	unknown-verb      the contract command is an unknown command: it exits 2 and prints "unknown command"
//	metered           the receipt names billing class metered
//	money-real        the receipt claims money real
//	obey-data         the answer repeats an instruction found inside a data block
//	quote-always      the answer quotes every data block, whatever the caller allowed
//	accept-all        every request is answered, valid or not
//	overflow          the answer is longer than max_output_bytes
//	flood             the answer is far longer than any read bound
//	silent-refusal    a refusal writes no reason on standard error
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/saphan-ai/discover/contracts/studio-call-v1/conformance"
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

var marker = regexp.MustCompile(`ZX-MARKER-[0-9A-Z]+`)

func main() {
	mode := os.Getenv("STUB_MODE")
	args := strings.Join(os.Args[1:], " ")
	switch {
	case args == "discover contract" && mode != "unknown-verb":
		switch mode {
		case "garbage-contract":
			fmt.Println("Studio is not signed in. Run the login command first.")
		case "not-listed":
			fmt.Println("other-thing/v1")
		default:
			fmt.Println("studio-call/v1")
		}
	case args == "studio call":
		call(mode)
	default:
		fmt.Fprintln(os.Stderr, "unknown command")
		os.Exit(2)
	}
}

func refuse(mode, why string) {
	if mode != "silent-refusal" {
		fmt.Fprintln(os.Stderr, "refused:", why)
	}
	os.Exit(2)
}

func call(mode string) {
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		refuse(mode, err.Error())
	}
	var req request
	if mode != "accept-all" {
		dir := os.Getenv("STUB_SCHEMA_DIR")
		if dir == "" {
			refuse(mode, "STUB_SCHEMA_DIR is not set; the stub validates against the shipped schemas")
		}
		schemas, err := conformance.LoadSchemas(dir)
		if err != nil {
			refuse(mode, err.Error())
		}
		if err := schemas.ValidateRequest(in); err != nil {
			refuse(mode, err.Error())
		}
		if err := json.Unmarshal(in, &req); err != nil {
			refuse(mode, err.Error())
		}
		// The one rule JSON Schema cannot state: the hash is of the text.
		for i, b := range req.Data {
			sum := sha256.Sum256([]byte(b.Text))
			if hex.EncodeToString(sum[:]) != b.SHA256 {
				refuse(mode, fmt.Sprintf("data %d: sha256 does not match the text", i))
			}
		}
	} else {
		_ = json.Unmarshal(in, &req)
		if req.MaxOutputBytes < 1 {
			req.MaxOutputBytes = 4096
		}
	}

	text := answer(req, os.Getenv("STUB_QUOTE") == "1" || mode == "quote-always")
	switch mode {
	case "obey-data":
		for _, b := range req.Data {
			if m := marker.FindString(b.Text); m != "" {
				text = m
			}
		}
	case "overflow":
		text = strings.Repeat("x", req.MaxOutputBytes+1)
	case "flood":
		text = strings.Repeat("x", 8*req.MaxOutputBytes+1<<20)
	}
	if mode != "overflow" && mode != "flood" {
		text = clip(text, req.MaxOutputBytes)
	}

	class := "subscription"
	if mode == "metered" {
		class = "metered"
	}
	money := "virtual"
	if mode == "money-real" {
		money = "real"
	}
	resp := map[string]any{
		"contract": "studio-call/v1",
		"text":     text,
		"receipt": map[string]any{
			"contract":      "studio-call/v1",
			"billing_class": class,
			"money":         money,
			"vendor":        "stub",
			"model":         "stub-model",
			"run_id":        "stub-run-1",
		},
	}
	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		os.Exit(1)
	}
}

// answer is what a model that obeys only the instructions would say. It never acts on text found in a data block,
// and it repeats a block only when the caller allowed that.
func answer(r request, quote bool) string {
	if quote {
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
