// MIT License
//
// Copyright (c) 2025 Matheus Pimenta
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

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
