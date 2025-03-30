package iptables

import (
	"fmt"
	"net/netip"
	"strconv"

	"github.com/matheuscscp/gke-metadata-server/api"

	"k8s.io/kubernetes/pkg/util/iptables"
	"k8s.io/utils/exec"
)

func LoadAndAttach(emulatorAddr netip.Addr, emulatorPort int) func() (func() error, error) {
	return func() (func() error, error) {
		// Create the following iptables rules:
		// iptables -t nat -A OUTPUT -d 169.254.169.254 -p tcp --dport 80 -j DNAT --to-destination <emulatorAddr>
		// iptables -A FORWARD -d <emulatorIP> -p tcp --dport <emulatorPort> -m state --state NEW,ESTABLISHED,RELATED -j ACCEPT

		// This rule essentially rewrites the destination of packets targeting the
		// GKE metadata server with the ip:port address of the emulator, i.e. it
		// effectively modifies the destination fields of matching packets.
		ipTables := iptables.New(exec.New(), iptables.ProtocolIPv4)

		// match conditions
		natTableArgs := []string{
			"-d", api.GKEMetadataServerAddressDefault,
			"-p", "tcp",
			"--dport", strconv.Itoa(api.GKEMetadataServerPortDefault),

			// action taken
			"-j", "DNAT",
			"--to-destination", emulatorAddr.String(),
		}

		filterTableArgs := []string{
			"-d", emulatorAddr.String(),
			"-p", "tcp",
			"--dport", strconv.Itoa(emulatorPort),
			"-m", "state", "--state", "NEW,ESTABLISHED,RELATED", // new or established connections
			// action taken
			"-j", "ACCEPT",
		}

		close := func() error {
			err1 := ipTables.DeleteRule(
				iptables.Table(iptables.TableNAT),
				iptables.ChainOutput,
				natTableArgs...,
			)
			err2 := ipTables.DeleteRule(
				iptables.Table(iptables.TableFilter),
				iptables.ChainForward,
				filterTableArgs...,
			)

			if err1 != nil {
				return err1
			}
			if err2 != nil {
				return err2
			}
			return nil
		}

		_, err := ipTables.EnsureRule(
			iptables.Append,
			iptables.TableNAT,    // NAT rules are applied before routing
			iptables.ChainOutput, // output chain is for locally generated traffic
			natTableArgs...,
		)
		if err != nil {
			return nil, fmt.Errorf("error adding DNAT rule: %w", err)
		}

		// This rule ensures that packets destined to the emulator IP and port
		// are accepted to be forwarded by the host, i.e. prevents the host from
		// dropping matching packets.
		_, err = ipTables.EnsureRule(
			iptables.Append,
			iptables.TableFilter,  // filter table is for access control (should packets be forwarded or dropped?)
			iptables.ChainForward, // forward chain is for packets that are being routed, i.e. not destined to the local host
			filterTableArgs...,
		)
		return close, err
	}
}
