package scenario

// ResetValue holds data admitted by a schema's concrete reset registration.
type ResetValue = any

// ResetBaseline is the versioned immutable input described in SCENARIOS.md.
// ReadResetBaseline retains its exact byte identity separately from its fields.
type ResetBaseline struct {
	Version        int             `json:"version"`
	ID             string          `json:"id" format:"id"`
	Profile        string          `json:"profile"`
	Registry       string          `json:"registry"`
	Domains        []ResetDomain   `json:"domains"`
	Assets         []ResetAsset    `json:"assets"`
	ExpectedChecks []ExpectedCheck `json:"expected_checks"`
	sha256         string
	fieldsSHA256   string
	schema         Schema
}

// SHA256 identifies the exact captured baseline bytes, not a re-encoding.
func (b ResetBaseline) SHA256() string { return b.sha256 }

type ResetDomain struct {
	Name     string   `json:"name"`
	AssetIDs []string `json:"asset_ids" format:"id"`
	Mode     string   `json:"mode,omitempty"`
}

type ResetAsset struct {
	ID         string `json:"id" format:"id"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256" format:"sha256"`
	SizeBytes  uint64 `json:"size_bytes" max:"9007199254740991"`
	Service    string `json:"service"`
	Purpose    string `json:"purpose"`
	TargetPath string `json:"target_path"`
}

type ResetTarget struct {
	Service string `json:"service"`
}

type BoundResetTarget struct {
	Service     string `json:"service"`
	ContainerID string `json:"container_id" format:"sha256"`
}

type ExpectedCheck struct {
	ID       string      `json:"id" format:"id"`
	Domain   string      `json:"domain"`
	Target   ResetTarget `json:"target"`
	Kind     string      `json:"kind"`
	Scope    ResetValue  `json:"scope"`
	Expected ResetValue  `json:"expected"`
}

type EmptyScope struct{}

type OwnedScope struct {
	Ownership string `json:"ownership" enum:"case"`
}
