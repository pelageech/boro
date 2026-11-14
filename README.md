# Boro

Boro is a Go SDK for writing integration tests in Go or YAML. It runs test
steps, checks results, handles cancellation and cleanup, and produces Ginkgo
JSON reports.

Write your service operations as ordinary Go methods, then expose them to YAML
through a capability. The same objects work in Go tests without the runner.

Requires Go 1.26 or newer.

## Try it

The bundled Counter example runs in memory. It needs no Docker or external
services. From the repository root:

```sh
out=$(mktemp -d)
go run ./cmd/boro run \
  --scenario examples/counter/testdata/scenario.yaml \
  --environment examples/counter/testdata/environment.json \
  --invocation counter-demo \
  --result "$out/report.json"
```

The scenario increments a counter to two, checks its value, then increments
and checks it again during teardown. The report is saved to `$out/report.json`.
Each run needs a new report path because existing files are never overwritten.

To explore and validate the example:

```sh
go run ./cmd/boro list --catalog examples/counter/testdata
go run ./cmd/boro describe operations
go run ./cmd/boro validate --catalog examples/counter/testdata --report "$out/manifest.json"
```

The environment file identifies the invocation and the exact scenario contents.
If you change the YAML, update its scenario hash in the environment file using
the validation manifest.

## Use your own service

Start with the [Counter guide](examples/counter/README.md), then follow
[Add a capability](EXTENDING.md) to build your runner.

A capability defines the YAML inputs and connects operations to your Go
methods. A profile describes the services a scenario needs. An environment
manifest tells the runner how to reach those services. Your application starts
and stops the services, whether they run locally, in containers, or remotely.

Actions run once. Checks can run once, poll until they pass (`eventually`), or
keep checking for a duration (`remains`). Preparation acquires clients before
execution and must not change service state.

Run each scenario in a fresh process. Cleanup closes acquired clients and
helpers, but restoring service state requires a reset handler or a fresh
environment. Use `--mode configure` to run configuration without the full set
of checks.

## Packages

| Package | Purpose |
| --- | --- |
| `boro` | Assemble capabilities, bind environments, and manage run resources |
| `scenario` | Load and validate YAML scenarios |
| `result` | Represent action results and observations |
| `suite` | Run scenarios with Ginkgo and Gomega |
| `command` | Build a CLI for your capabilities |
| `examples/counter` | A complete capability with an in-memory backend |

## Development

```sh
go build ./...
go test -race -shuffle=on ./...
```
