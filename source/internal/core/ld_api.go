package core

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// parseLdQuery reads the arguments of a search.
var LdNameRE = regexp.MustCompile(`^[A-Za-z0-9._*-]{1,200}$`)

func ParseAddrLoose(s string) (string, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return "", fmt.Errorf("%q is not an address", s)
	}
	return a.Unmap().String(), nil
}
