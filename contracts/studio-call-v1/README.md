# studio-call/v1

`studio-call/v1` is **one walled, tool-less, read-only model call on the user's own subscription seat**.

A caller sends instructions and some text. Saphan Studio answers with text and a receipt. The call has no tools, writes
nothing and reads nothing but what the request carries. The contract is public so that any program can be a caller and
any program can be a Studio, or a stub of one.

The contract is neutral. It names no product. A caller is one program among others.

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
| `data` | array of block | may be empty |
| `max_output_bytes` | integer | at least 1. The most bytes `text` of the response may hold, counted in UTF-8 |

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
| `money` | string | optional. If present, `virtual` |
| `vendor` | string | not empty |
| `model` | string | not empty |
| `run_id` | string | not empty |

## Finding Studio

A caller finds Studio by one rule: a `saphan` executable on `PATH` whose `saphan discover contract` prints the contract
versions it serves, one per line, and nothing else.

A caller handles three outcomes. It handles each one by name.

1. **`studio-call/v1` is listed.** Use it, when the user has asked for it (see Consent).
2. **It is not listed**, or there is no `saphan` on `PATH`. Studio is absent or too old. The caller works alone. It does
   not retry, and it does not guess a newer or older contract.
3. **The answer does not parse as a version list** (prose, JSON, a blank line, an empty answer, a non-zero exit). The
   caller refuses by name. It never guesses what the answer meant.

A version is written `<name>/v<number>`, for example `studio-call/v1`.

## Calling

The caller runs `saphan studio call`. It writes one request on standard input and reads one response from standard
output.

- A request that conforms ends with exit code 0 and one response on standard output.
- A request that does not conform ends with a non-zero exit code, a reason on standard error, and nothing on standard
  output.

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
- A response longer than `max_output_bytes`, or one that repeats an instruction found only inside a data block, is
  refused.

## Versioning

`studio-call/v1` only grows optional fields. A new field is optional, and a caller that does not know it still works. A
removed field, a retyped field, a narrowed value, or a field that becomes required is `studio-call/v2`, a new contract
with a new name in the version list. A Studio may serve several versions at once. A caller asks for the one it knows.

The schemas close every object (`additionalProperties: false`). A new optional field therefore enters the schema first.

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
   class `subscription`, and holds at most `max_output_bytes` bytes;
3. keeps data as data: a marker string `ZX-MARKER-<hex>` planted only in a data block must not appear in the answer,
   unless the instructions asked to quote;
4. refuses each invalid request in `testdata/invalid/`, and a request that is not JSON, with a non-zero exit code and
   nothing on standard output.

It also holds the caller's side: `CheckResponse` applies the billing class, the schema, the output bound and the data
rule to one answer, and `ParseContractList` reads the version list or refuses it by name.

The stub changes one behaviour when `STUB_MODE` is set (`garbage-contract`, `not-listed`, `metered`, `obey-data`,
`accept-all`, `overflow`). The suite's own tests show that each change turns it red.
