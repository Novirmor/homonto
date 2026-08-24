module github.com/noviopenworks/homonto

go 1.25

// Pin a patched toolchain: remote-source fetch introduces reachable call paths
// into crypto/tls, crypto/x509, and net/textproto, whose go1.26.3 advisories
// (GO-2026-5856/5037/5039) are fixed in go1.26.5. The pin tracks the newest
// patch release (currently go1.26.6) so later stdlib fixes stay applied and
// govulncheck remains clean.
toolchain go1.26.6

// modernc.org/sqlite and golang.org/x/sys are required by the workflow
// runtime foundation being built on this branch; later tasks import them.
require (
	github.com/pelletier/go-toml/v2 v2.4.2
	github.com/spf13/cobra v1.10.2
	github.com/tailscale/hujson v0.0.0-20260302212456-ecc657c15afd
	github.com/tidwall/gjson v1.19.0
	github.com/tidwall/sjson v1.2.5
	golang.org/x/sys v0.47.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.57.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.0 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
