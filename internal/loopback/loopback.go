// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package loopback

import (
	"fmt"

	"github.com/vishvananda/netlink"
)

const (
	GKEMetadataServerAddr = gkeMetadataServerIP + ":80"

	gkeMetadataServerIP = "169.254.169.254"

	gkeMetadataServerAddrLabel = "lo:gke-md-sv"
)

var gkeMetadataServerAddr = func() *netlink.Addr {
	a, err := netlink.ParseAddr(gkeMetadataServerIP + "/32")
	if err != nil {
		panic(err)
	}
	a.Label = gkeMetadataServerAddrLabel
	a.Scope = int(netlink.SCOPE_HOST)
	return a
}()

func LoadAndAttach() (func() error, error) {
	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return nil, fmt.Errorf("error loading loopback interface: %w", err)
	}
	close := func() error {
		return netlink.AddrDel(lo, gkeMetadataServerAddr)
	}

	loAddrs, err := netlink.AddrList(lo, netlink.FAMILY_V4)
	if err != nil {
		return nil, fmt.Errorf("error listing addresses of the loopback interface: %w", err)
	}

	for _, addr := range loAddrs {
		if !addr.Equal(*gkeMetadataServerAddr) {
			continue
		}
		if a := addr.String(); a != gkeMetadataServerAddr.String() {
			return nil, fmt.Errorf("the gke-metadata-server address is already present in the loopback interface: %s", a)
		}
		// the address is already attached to the loopback interface from a previous execution
		return close, nil
	}

	if err := netlink.AddrAdd(lo, gkeMetadataServerAddr); err != nil {
		return nil, fmt.Errorf("error adding the gke-metadata-server address to the loopback interface: %w", err)
	}

	return close, nil
}
