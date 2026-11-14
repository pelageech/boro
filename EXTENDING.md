# Add a capability

Keep service behavior in ordinary Go objects. Supply clients through constructors so those objects remain usable without YAML or a runner.

Start with the Counter example. Its files have distinct responsibilities:

1. `counter.go` owns the object, validation before acquisition, operations and close behavior.
2. `schema/spec.go` owns YAML input and expectation structs, typed resource references, validators, descriptors and the logical profile.
3. `managed.go` declares that schema and binds operations to the object's methods.
4. `cmd/boro/main.go` assembles a backend, capability, composition and runner command.

## Declare and bind

A capability implements `Declare(scenario.Schema) (scenario.Schema, error)` and `Register(boro.Bindings) (boro.Bindings, error)`.

Declare typed operations with `scenario.Action[Input]` or `scenario.Observer[Input, Want]`. Add descriptors with `Schema.WithDescriptor`. Add resources with `scenario.WithResource` and refer to instances using `scenario.Ref[Spec]`.

Bind the exact descriptor's `Spec()` with `boro.BindAction` or `boro.BindObserver`. Both assembly methods must remain free of acquisition and effects.

`NewPrepare` creates invocation-local state shared by that operation's steps. The returned preparer resolves inputs and acquires clients. Its returned callback performs the action or one observation. The complete selected tree is prepared before execution, so service mutations belong only inside action callbacks.

Register acquired resources with `RunScope.Own` exactly once. If ownership transfer fails, close the still-unowned handle yourself with an independent bounded context. Do not register an object's private client separately when the object already owns and closes it.

## Assemble a runner

`cmd/boro/main.go` is a complete wiring example. Replace the Counter capability and profile with yours, then pass them to `boro.Compose` and `command.New`.

The semantic revision passed to Compose identifies callback and validator behavior. Change it when those semantics change. YAML cannot load arbitrary Go capabilities. Compile matching compositions into the owner and runner when those execute separately.

The reusable command supports `list`, `validate`, `describe`, `run`, `contract` and `reset`. A caller-supplied probe enables `bindings`, and a caller-supplied baseline capture function enables `capture`. Catalog commands require explicit `--catalog` paths.

## Bind an environment

The harness loads the scenario with its composition schema and explicit profiles, then builds `boro.EnvironmentManifest` with the scenario ID/hash, invocation ID and concrete service bindings. `boro.Resolve` checks those bindings before preparation acquires clients.

Profiles describe logical services. Starting containers, attaching to a VM and destroying physical resources belong to the harness. The Counter manifest is a local in-memory example.

For direct Go execution call `suite.Run` once in a fresh process. A harness can supply caller-owned cleanup through `suite.Options.Cleanup`. Keep reports and validate them with `boro.ValidateSuiteReport`.

## Reset and reuse

Register typed reset declarations through `scenario.Schema` and their runtime handlers with `boro.BindReset`. Restoring state, observing the baseline and releasing reset resources must all finish before reuse. Unsupported reset domains require recreating the environment.

The Counter example deliberately has no restoration handler. Its independently opened handles share a backend, and closing a handle preserves values. A fresh runner creates a fresh backend for the next scenario.
