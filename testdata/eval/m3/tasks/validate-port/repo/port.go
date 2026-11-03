package netcfg

import "strconv"

// ParsePort parses a TCP port number.
func ParsePort(s string) (int, error) {
	return strconv.Atoi(s)
}
