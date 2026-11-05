package catalog

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/Novirmor/homonto/internal/schema"
)

// ContractVersion is a package contract's MAJOR.MINOR (ADR 0108). A minor
// adds to the contract without changing what an existing package means; a
// major may change meaning, so a binary never interprets a major it does not
// implement.
type ContractVersion struct {
	Major, Minor int
}

func (v ContractVersion) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

// Contracts are the package contract versions this binary implements, per
// kind. They are published in docs/contracts/README.md and by `homonto
// package contracts`; a change here is a contract change and needs the
// conformance suite (ConformanceSuiteContracts) and the docs updated with it.
var Contracts = map[string]ContractVersion{
	KindEngine:   {1, 0},
	KindWorkflow: {1, 0},
	KindService:  {1, 0},
}

var contractVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// ParseContractVersion parses "MAJOR.MINOR".
func ParseContractVersion(s string) (ContractVersion, error) {
	m := contractVersion.FindStringSubmatch(s)
	if m == nil {
		return ContractVersion{}, fmt.Errorf("%q is not MAJOR.MINOR (for example \"1.0\")", s)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return ContractVersion{major, minor}, nil
}

// ErrContractUnsupported marks a package written against a contract major
// this binary does not implement.
var ErrContractUnsupported = errors.New("unsupported package contract")

// checkContractVersion validates a schema 2 manifest's [compat].contract
// against the contract this binary implements for its kind. The kind is
// validated first, so an unknown kind reports that instead.
func checkContractVersion(label, kind, declared string) (ContractVersion, error) {
	implemented, ok := Contracts[kind]
	if !ok {
		return ContractVersion{}, nil
	}
	if declared == "" {
		return ContractVersion{}, fmt.Errorf("%s: [compat].contract is required; declare the %s contract the package was written against, for example contract = %q (docs/contracts/README.md)", label, kind, implemented.String())
	}
	v, err := ParseContractVersion(declared)
	if err != nil {
		return ContractVersion{}, fmt.Errorf("%s: [compat].contract %v", label, err)
	}
	switch {
	case v.Major > implemented.Major:
		return v, fmt.Errorf("%s requires %s contract %s, but this homonto implements %s contract %s and cannot interpret a newer major; upgrade homonto: %w", label, kind, v, kind, implemented, schema.ErrTooNew)
	case v.Major < implemented.Major:
		return v, fmt.Errorf("%s was written against %s contract %s, which this homonto no longer implements (it implements %s); update the package to contract %s: %w", label, kind, v, implemented, implemented, ErrContractUnsupported)
	case v.Minor > implemented.Minor:
		return v, fmt.Errorf("%s requires %s contract %s, but this homonto implements %s contract %s; upgrade homonto: %w", label, kind, v, kind, implemented, schema.ErrTooNew)
	}
	return v, nil
}
