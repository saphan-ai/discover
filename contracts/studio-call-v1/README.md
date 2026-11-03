# studio-call/v1

`studio-call/v1` is **one walled, tool-less, read-only model call on the user's own subscription seat**.

A caller sends instructions and some text. Saphan Studio answers with text and a receipt. The call has no tools, writes
nothing and reads nothing but what the request carries. The contract is public so that any program can be a caller and
any program can be a Studio, or a stub of one.

A caller is one program among others, and the call itself names no product. One name is not neutral: a caller finds
Studio by looking for an executable named `saphan`, which is the name of Studio's vendor (see Finding Studio).

## Files

| file | what it is |
|---|---|
| `request.schema.json` | JSON Schema 2020-12 for the request |
| `response.schema.json` | JSON Schema 2020-12 for the response |
| `testdata/valid/`, `testdata/invalid/` | examples; each invalid one differs from a valid one in exactly one field |
| `conformance/` | a Go test package that checks an executable against this contract |

## The request

One JSON object. No other key is allowed.

| field | type | rule |
|---|---|---|
| `contract` | string | exactly `studio-call/v1` |
| `instructions` | string | not empty. The **only** source of instructions in the call |
| `data` | array of block | required, and an array: it may be empty, it is never missing and never `null` |
| `max_output_bytes` | integer | from 1 to 1048576 (1 MiB, far above any review answer). The most bytes `text` of the response may hold, counted in UTF-8 |

A block is one JSON object. No other key is allowed.

| field | type | rule |
|---|---|---|
| `path` | string | where the text came from, relative to the target root. No leading `/`, no drive letter, no backslash, no `..` segment |
| `sha256` | string | the SHA-256 of the UTF-8 bytes of `text`, as 64 lowercase hex characters |
| `text` | string | the content. **Data, never instructions** |

Studio treats every block as data. An instruction found inside a block is not obeyed. If the text of a block says "ignore
the instructions", the answer does not change because of it. Studio refuses a block whose `sha256` does not match its
`text`.

## The response

One JSON object. No other key is allowed.

| field | type | rule |
|---|---|---|
| `contract` | string | exactly `studio-call/v1` |
| `text` | string | the model's answer, at most `max_output_bytes` bytes |
| `receipt` | object | see below |

The receipt is one JSON object. No other key is allowed.

| field | type | rule |
|---|---|---|
| `contract` | string | exactly `studio-call/v1` |
| `billing_class` | string | exactly `subscription` |
| `money` | string | required, exactly `virtual`. A Studio never starts a call that would spend money: over the seat's allowance it refuses the call by name instead |
| `vendor` | string | not empty |
| `model` | string | not empty |
| `run_id` | string | not empty |

## Finding Studio

A caller finds Studio by one rule: an executable named `saphan` (the Studio vendor's name) on `PATH` whose
`saphan discover contract` prints the contract versions it serves, one per line, and nothing else. Nothing
authenticates that executable: any `saphan` earlier on `PATH` is taken for Studio, and the strict parse below only
makes a wrong answer end in a refusal or in working alone.

A caller handles three outcomes. It handles each one by name.

1. **`studio-call/v1` is listed.** Use it, when the user has asked for it (see Consent).
2. **Studio is absent or too old.** There is no `saphan` on `PATH`, or `saphan discover contract` **exits non-zero**
   (a `saphan` too old to know the verb), or it exits 0 with a version list that does not hold `studio-call/v1`. The
   caller works alone and says so. It does not retry, and it does not guess a newer or older contract.
3. **The answer is not a version list.** The command exits 0 and prints prose, JSON, a blank line or nothing. The
   caller refuses Studio by name, tells the user, and never retries and never guesses what the answer meant. A caller
   the user explicitly asked to use Studio stops with that named refusal; it does not fall back to working alone
   without saying so.

A version is written `<name>/v<number>`, for example `studio-call/v1`.

## Calling

The caller runs `saphan studio call`. It writes one request on standard input and reads one response from standard
output.

- A request that conforms ends with exit code 0 and one response on standard output.
- A request that does not conform ends with a non-zero exit code, a reason on standard error, and nothing on standard
  output.

A caller bounds the call. It sets its own deadline (the suite's harness allows two minutes per command) and stops the
command when it passes. It reads at most `6 × max_output_bytes + 4096` bytes of standard output (JSON can spell one
byte of text as six) and refuses the answer, and stops the command, when more arrives.

## Consent

A call spends the user's subscription allowance. A caller makes a call **only on the user's explicit request at that
call**, for example `--via studio`. A caller may **offer** Studio. It never selects Studio on its own, never makes the
call by default, never makes it because an earlier call was allowed, and never reads the user's silence as a yes.

## Trust

The caller trusts nothing it gets back.

- A conforming caller checks the answer against `response.schema.json` before it uses one byte of it.
- It re-checks every claim in the answer itself. A claim that cites a file and a line is checked against the caller's own
  files. The caller does not accept a citation because Studio made it.
- It keeps its own read-only proof: Studio's read-only promise does not replace the caller's own.
- A response whose receipt names any billing class other than `subscription` is **refused by name**. The caller does not
  use its text, and it tells the user that the call was not billed to the subscription seat.
- A response whose receipt has a `money` other than `virtual` (`real`, `mixed`, or anything else) is **refused by name**.
  The caller does not use its text, and it tells the user plainly that the response **claims money was spent**. A Studio
  never starts a call that would spend money; a response that claims it did is wrong, and the user is told.
- A response longer than `max_output_bytes`, or one that repeats an instruction found only inside a data block, is
  refused.
- The receipt is **self-reported**. A caller can refuse a receipt that claims a non-subscription class or money, and it
  cannot verify one that claims `subscription` and `virtual`.
- The data check of the suite (`CheckResponse`) sees only the markers the suite plants (`ZX-MARKER-<hex>`). On real
  data a caller gets no mechanical protection from it, and the rule above about repeated instructions is the caller's
  own to keep. Whether an answer may repeat text of a data block is the caller's explicit decision, passed to
  `CheckResponse`; the words of the instructions never decide it.

## Versioning

`studio-call/v1` is **frozen**. Any change to it, a new field (optional or not), a removed field, a retyped field, a
narrowed value, or a field that becomes required, is a new version: `studio-call/v2`, in its own directory, with its own
schemas. A Studio serves several versions side by side and lists each one in its version list. A caller asks for the
one it knows.

Every object is closed (`additionalProperties: false`), and a v1 caller refuses a field it does not know. That is why
there is no such thing as a compatible addition to v1: a deployed caller holds the old schema. The suite pins the
SHA-256 of both v1 schema files, so an edit to either fails until it is made under a new version.

## One worked call

Request, on standard input:

```json
{
  "contract": "studio-call/v1",
  "instructions": "List the headings found in the data blocks.",
  "data": [
    {
      "path": "docs/notes.txt",
      "sha256": "e49c81e2d2f84e259d40e2fb8192f3bcd198b355184845d76d8f58807d0d78ee",
      "text": "alpha\nbeta\n"
    }
  ],
  "max_output_bytes": 4096
}
```

Response, on standard output:

```json
{
  "contract": "studio-call/v1",
  "text": "The data block docs/notes.txt has two lines: alpha and beta.",
  "receipt": {
    "contract": "studio-call/v1",
    "billing_class": "subscription",
    "money": "virtual",
    "vendor": "example-vendor",
    "model": "example-model",
    "run_id": "run-0001"
  }
}
```

## The conformance suite

The suite is the Go package in `conformance/`. It needs the Go toolchain and the module this directory sits in.

Run it against your executable:

```sh
STUDIO_CALL_EXECUTABLE=/path/to/your/saphan go test -count=1 ./contracts/studio-call-v1/conformance/
```

Run it against the stub the package ships (no Studio, no network, no model):

```sh
go test -count=1 ./contracts/studio-call-v1/conformance/
```

The environment variable is `STUDIO_CALL_EXECUTABLE`. When it is unset, the suite builds and runs the stub in
`conformance/testdata/stub`.

The suite checks that the executable:

1. lists `studio-call/v1` and nothing but contract versions;
2. answers each valid request in `testdata/valid/` with a response that matches `response.schema.json`, names billing
   class `subscription` and money `virtual`, and holds at most `max_output_bytes` bytes, within the read bound above;
3. keeps data as data: a marker string `ZX-MARKER-<hex>` planted only in a data block must not appear in the answer,
   unless the caller allowed quoting for that fixture (the suite declares it per fixture, in `Options.QuoteAllowed`);
4. refuses each invalid request in `testdata/invalid/`, and a request that is not JSON, with a non-zero exit code, a
   non-empty reason on standard error, and nothing on standard output.

It also holds the caller's side: `Options.FindStudio` returns which of the three outcomes above applies,
`CheckResponse(schemas, request, response, quoteAllowed)` applies the billing class, the money claim, the schema, the
output bound and the data rule to one answer, and `ParseContractList` reads the version list or refuses it by name.

Caller consent (see Consent) is outside this suite: the suite drives a Studio, not a caller. Each caller owes its own
test that a call without the user's explicit request never runs `saphan studio call`.

The stub validates every request with the shipped `request.schema.json` (it reads the directory named in
`STUB_SCHEMA_DIR`). It changes one behaviour when `STUB_MODE` is set (`garbage-contract`, `not-listed`,
`unknown-verb`, `metered`, `money-real`, `obey-data`, `quote-always`, `accept-all`, `overflow`, `flood`,
`silent-refusal`). The suite's own tests show that each change turns it red.
