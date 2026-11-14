# Counter example

Counter shows how to use the same Go object directly and through a YAML
scenario. It has two operations: add to a named counter and read its value.
The in-memory backend needs no external services.

See the [main README](../../README.md#try-it) to run the scenario. To run the
example tests from the repository root:

```sh
go test ./examples/counter/...
```

## Use Counter in Go

Create a backend, open a counter, and call its methods:

```go
backend := counter.NewMemoryBackend()
c, err := counter.New(ctx, counter.Spec{Name: "requests"}, backend)
if err != nil {
    return err
}
if err := c.Add(ctx, 2); err != nil {
    return errors.Join(err, c.Close(cleanupCtx))
}
value, readErr := c.Value(ctx)
if err := errors.Join(readErr, c.Close(cleanupCtx)); err != nil {
    return err
}
fmt.Println(value) // 2
```

This snippet assumes a work context `ctx` and a separate, bounded
`cleanupCtx` that stays usable if the work is canceled. The caller closes the
Counter once. Closing it releases its client, but leaves the backend and stored
value intact.

You can also construct the same object from YAML:

```go
c, err := counter.FromYAML(ctx, []byte("name: requests\n"), backend)
```

This accepts a resource spec rather than a full scenario. `counter.ParseYAML`
validates a spec without opening a client. Unknown fields and other malformed
YAML are rejected before the backend is opened.

Counters with the same name share a value within one `MemoryBackend`.
Separate backends keep their values isolated.

## Use Counter in YAML

The [example scenario](testdata/scenario.yaml) declares a counter resource,
increments it, and checks its value. The runner calls the same `Counter.Add`
and `Counter.Value` methods used above.

To include Counter in your runner:

1. Call `counter.NewCapability(factory)` with a backend factory.
2. Pass the capability to `boro.Compose("example.counter.v1", capability)`.
3. Pass the composition and `schema.Profile()` to `command.New`.

See [the bundled runner](../../cmd/boro/main.go) for the complete setup. The
factory supplies a backend after environment bindings are resolved. The runner
owns and closes the Counter objects it opens. The backend remains caller-owned.

Counter has no reset handler. Start with a fresh backend for each scenario
instead of reusing values left by an earlier run.

## Build a similar capability

Use these files as a starting point:

| File | Purpose |
| --- | --- |
| [counter.go](counter.go) | Go object, validation, operations, and cleanup |
| [schema/spec.go](schema/spec.go) | YAML types, resources, operations, and profile |
| [managed.go](managed.go) | Connect YAML operations to Counter methods |
| [memory.go](memory.go) | In-memory backend |

The [extension guide](../../EXTENDING.md) explains declarations, preparation,
resource ownership, and reset handlers.
