module github.com/noviopenworks/homonto

go 1.23

// Pin a patched toolchain: remote-source fetch introduces reachable call paths
// into crypto/tls, crypto/x509, net/textproto, encoding/asn1, and net/http,
// whose go1.26.3 advisories (GO-2026-5856/5037/5039) and go1.26.5 advisories
// (GO-2026-5972/5026) are fixed in go1.26.6. govulncheck is clean under it.
toolchain go1.26.6

require (
	github.com/pelletier/go-toml/v2 v2.4.2
	github.com/spf13/cobra v1.10.2
	github.com/tailscale/hujson v0.0.0-20260302212456-ecc657c15afd
	github.com/tidwall/gjson v1.19.0
	github.com/tidwall/sjson v1.2.5
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.0 // indirect
)
