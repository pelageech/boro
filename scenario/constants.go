package scenario

const (
	ResetReadOnly     = "read_only"
	DocumentLimit     = 16 << 20
	SetupPhase        = "setup"
	MatchAll          = "all"
	MatchFirst        = "first"
	ResetOutcomeError = "error"
	ResetContainer    = "container"
	ResetGRPC         = "grpc"
	ResetHTTP         = "http"
	ResetHTTPS        = "https"
	ResetUnix         = "unix"
	ResetDocker       = "docker"
	ResetNotRun       = "not_run"
	ResetMatched      = "matched"
	ResetMismatch     = "mismatch"
	ResetProcess      = "process"
	ResetOwned        = "owned"
	resetMax          = "max"
	resetMin          = "min"
)
