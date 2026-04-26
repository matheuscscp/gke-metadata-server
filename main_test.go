// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package main_test

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

type pod struct {
	name               string
	file               string
	serviceAccountName string
	nodeSelector       map[string]string
	hostNetwork        bool
	expectedExitCode   int
}

var (
	// testID is used to isolate the GCP workload-identity-pool provider
	// across CI runs.
	testID = os.Getenv("TEST_ID")

	// localImage and the per-target tags identify the daemon and test
	// container images that `make build`/`make build-go-test` produced
	// locally and that `make kind-load-images` loaded into the kind nodes'
	// containerd. Pods reference them by tag with imagePullPolicy: Never.
	localImage     = envOrDefault("LOCAL_IMAGE", "gke-metadata-server-test")
	localTagDaemon = envOrDefault("LOCAL_TAG_DAEMON", "container")
	localTagGoTest = envOrDefault("LOCAL_TAG_GOTEST", "go-test")
)

func envOrDefault(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func TestEndToEnd(t *testing.T) {
	for _, tt := range []struct {
		name           string
		emulatorValues string
		allNodes       bool
		pods           []pod
	}{
		{
			name:           "helm",
			emulatorValues: "helm.yaml",
			pods: []pod{{
				name:               "test-impersonation",
				serviceAccountName: "test-impersonated",
				nodeSelector: map[string]string{
					"hasRoutingMode": "true",
				},
			}},
		},
		{
			name:           "timoni without watch",
			emulatorValues: "timoni-no-watch.cue",
			pods: []pod{{
				name:               "test-impersonation",
				serviceAccountName: "test-impersonated",
				nodeSelector: map[string]string{
					"hasRoutingMode": "true",
				},
			}},
		},
		{
			name:           "timoni all nodes",
			emulatorValues: "timoni-all-nodes.cue",
			allNodes:       true,
			pods: []pod{{
				name:               "test-impersonation",
				serviceAccountName: "test-impersonated",
				nodeSelector: map[string]string{
					"allNodesTest": "true",
				},
			}},
		},
		{
			name:           "timoni with watch",
			emulatorValues: "timoni.cue",
			pods: []pod{
				// Each (routing mode, pod kind) combination has exactly one
				// dedicated test pod, pinned to the node with the matching
				// routing mode, so every supported attestation path is
				// exercised deterministically.
				{
					name:               "test-impersonation",
					serviceAccountName: "test-impersonated",
					nodeSelector: map[string]string{
						"node.gke-metadata-server.matheuscscp.io/routingMode": "eBPF",
					},
				},
				{
					name:               "test-direct-access",
					serviceAccountName: "test",
					nodeSelector: map[string]string{
						"node.gke-metadata-server.matheuscscp.io/routingMode": "eBPF",
					},
				},
				{
					name:               "test-gcloud",
					file:               "pod-gcloud.yaml",
					serviceAccountName: "test",
					nodeSelector: map[string]string{
						"node.gke-metadata-server.matheuscscp.io/routingMode": "eBPF",
					},
				},
				{
					name:               "test-loopback-routing",
					serviceAccountName: "test-impersonated",
					nodeSelector: map[string]string{
						"node.gke-metadata-server.matheuscscp.io/routingMode": "Loopback",
					},
				},
				{
					name:               "test-none-routing",
					serviceAccountName: "test-impersonated",
					nodeSelector: map[string]string{
						"node.gke-metadata-server.matheuscscp.io/routingMode": "None",
					},
				},
				// hostNetwork pods on each routing mode. The eBPF case exercises
				// the BPF sockops attestation map; Loopback and None exercise
				// the netlink SOCK_DIAG + /proc walk fallback.
				{
					name:               "test-host-network-ebpf",
					serviceAccountName: "test-impersonated",
					hostNetwork:        true,
					nodeSelector: map[string]string{
						"node.gke-metadata-server.matheuscscp.io/routingMode": "eBPF",
					},
				},
				// Second hostNetwork pod on the SAME eBPF node bound to a
				// different ServiceAccount. Both share the node IP, so the
				// pre-attestation source-IP path would have collapsed them to
				// a single identity. Both succeeding concurrently with their
				// own SAs is the canonical proof that kernel attestation
				// disambiguates hostNetwork pods.
				{
					name:               "test-host-network-ebpf-disambig",
					serviceAccountName: "test",
					hostNetwork:        true,
					nodeSelector: map[string]string{
						"node.gke-metadata-server.matheuscscp.io/routingMode": "eBPF",
					},
				},
				{
					name:               "test-host-network-loopback",
					serviceAccountName: "test-impersonated",
					hostNetwork:        true,
					nodeSelector: map[string]string{
						"node.gke-metadata-server.matheuscscp.io/routingMode": "Loopback",
					},
				},
				{
					name:               "test-host-network-none",
					serviceAccountName: "test-impersonated",
					hostNetwork:        true,
					nodeSelector: map[string]string{
						"node.gke-metadata-server.matheuscscp.io/routingMode": "None",
					},
				},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if !applyEmulator(t, tt.emulatorValues, tt.allNodes) {
				// Emulator failed to start, skip to debug logging
				printDebugInfo(t, nil, nil)
				t.FailNow()
			}
			applyPods(t, tt.pods)
			exitCodes := waitForPods(t, tt.pods)
			checkExitCodes(t, tt.pods, exitCodes)
		})
	}
}

func countExpectedNodes(t *testing.T, allNodes bool) int {
	t.Helper()

	type kindNode struct {
		Role   string            `yaml:"role"`
		Labels map[string]string `yaml:"labels"`
	}

	type kindCluster struct {
		Nodes []kindNode `yaml:"nodes"`
	}

	b, err := os.ReadFile("testdata/kind.yaml")
	require.NoError(t, err)

	var cluster kindCluster
	err = yaml.Unmarshal(b, &cluster)
	require.NoError(t, err)

	if allNodes {
		return len(cluster.Nodes)
	}

	count := 0
	for _, node := range cluster.Nodes {
		if node.Role == "worker" && node.Labels["iam.gke.io/gke-metadata-server-enabled"] == "true" {
			count++
		}
	}

	return count
}

func applyEmulator(t *testing.T, valuesFile string, allNodes bool) bool {
	t.Helper()

	// count expected worker nodes from kind.yaml
	expectedNodes := countExpectedNodes(t, allNodes)

	// delete previous instances
	_ = exec.Command(
		"helm",
		"--kube-context", "kind-gke-metadata-server",
		"--namespace", "kube-system",
		"uninstall", "gke-metadata-server",
		"--wait").Run()
	_ = exec.Command(
		"timoni",
		"--kube-context", "kind-gke-metadata-server",
		"--namespace", "kube-system",
		"delete", "gke-metadata-server",
		"--wait").Run()

	// execute values template
	var values string
	b, err := os.ReadFile(fmt.Sprintf("testdata/%s", valuesFile))
	require.NoError(t, err)
	values = strings.ReplaceAll(string(b), "<TEST_ID>", testID)
	values = strings.ReplaceAll(values, "<LOCAL_IMAGE>", localImage)
	values = strings.ReplaceAll(values, "<LOCAL_TAG_DAEMON>", localTagDaemon)

	// Apply directly from the on-disk chart/module — no OCI push happens
	// during PR validation (see Makefile), so there is nothing to pull.
	var emulatorCmdName string
	var emulatorCmdArgs []string
	if strings.Contains(valuesFile, "helm") {
		emulatorCmdName = "helm"
		emulatorCmdArgs = []string{
			"--kube-context", "kind-gke-metadata-server",
			"--namespace", "kube-system",
			"upgrade", "--install", "gke-metadata-server", "./helm/gke-metadata-server",
			"-f", "-",
		}
	} else {
		emulatorCmdName = "timoni"
		emulatorCmdArgs = []string{
			"--kube-context", "kind-gke-metadata-server",
			"--namespace", "kube-system",
			"apply", "gke-metadata-server", "./timoni/gke-metadata-server",
			"-f", "-",
		}
	}

	// apply
	cmd := exec.Command(emulatorCmdName, emulatorCmdArgs...)
	cmd.Stdin = strings.NewReader(values)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("error applying emulator from %s: %v: %s", valuesFile, err, string(b))
	}

	// wait with timeout
	const emulatorTimeout = 2 * time.Minute
	const sleepSecs = 10
	timeout := time.Now().Add(emulatorTimeout)

	for time.Now().Before(timeout) {
		cmd := exec.Command(
			"sh",
			"-c",
			"kubectl --context kind-gke-metadata-server --namespace kube-system get ds gke-metadata-server | grep gke | awk '{print $4}'")
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("error getting emulator state: %v: %s", err, string(b))
		}

		if strings.TrimSpace(string(b)) == fmt.Sprint(expectedNodes) {
			t.Log("Emulator is ready")
			return true
		}

		t.Logf("Sleeping for %d secs and checking emulator status...", sleepSecs)
		time.Sleep(sleepSecs * time.Second)
	}

	// If we get here, we timed out
	t.Logf("timed out after %v waiting for emulator to be ready", emulatorTimeout)
	return false
}

func applyPods(t *testing.T, pods []pod) {
	t.Helper()

	for _, p := range pods {
		// delete previous instance
		_ = exec.Command(
			"kubectl",
			"--context", "kind-gke-metadata-server",
			"--namespace", "default",
			"delete", "po", p.name).Run()

		// execute pod template
		var pod string
		file := "pod.yaml"
		if p.file != "" {
			file = p.file
		}
		b, err := os.ReadFile("testdata/" + file)
		require.NoError(t, err)
		serviceAccountName := "default"
		if sa := p.serviceAccountName; sa != "" {
			serviceAccountName = sa
		}
		var noneRoutingEnv string
		if p.nodeSelector["node.gke-metadata-server.matheuscscp.io/routingMode"] == "None" {
			noneRoutingEnv = `
    - name: HOST_IP
      valueFrom:
        fieldRef:
          fieldPath: status.hostIP
    - name: GKE_METADATA_SERVER_PORT
      value: "16321"
    - name: GCE_METADATA_HOST
      value: "$(HOST_IP):$(GKE_METADATA_SERVER_PORT)"
    - name: GCE_METADATA_ROOT
      value: "$(HOST_IP):$(GKE_METADATA_SERVER_PORT)"
    - name: GCE_METADATA_IP
      value: "$(HOST_IP):$(GKE_METADATA_SERVER_PORT)"`
		}
		// On pods pinned to eBPF nodes, signal that the daemon's --test-proxy-upstream
		// marker server is reachable so TestProxyPassthrough can issue a hard
		// assertion rather than skipping. Pods not pinned to eBPF skip the test.
		//
		// Also set GCE_METADATA_HOST so cloud.google.com/go/compute/metadata's
		// OnGCE short-circuits to true without probing 169.254.169.254 — the
		// proxy-passthrough marker server intercepts non-metadata requests on
		// that IP and replies without `Metadata-Flavor: Google`, which races
		// with the library's parallel DNS strategy and flakes OnGCE to false.
		var ebpfEnv string
		if p.nodeSelector["node.gke-metadata-server.matheuscscp.io/routingMode"] == "eBPF" {
			ebpfEnv = `
    - name: EXPECT_PROXY_UPSTREAM
      value: "true"
    - name: GCE_METADATA_HOST
      value: "169.254.169.254"`
		}
		// Loopback mode binds the daemon directly to 169.254.169.254:80; the
		// library's HTTP probe to that IP gets a metadata response from the
		// daemon, but for paths it doesn't recognise (like `/`) it can return
		// without the expected header and lose the OnGCE race the same way.
		// Short-circuit it the same way as eBPF.
		var loopbackEnv string
		if p.nodeSelector["node.gke-metadata-server.matheuscscp.io/routingMode"] == "Loopback" {
			loopbackEnv = `
    - name: GCE_METADATA_HOST
      value: "169.254.169.254"`
		}
		var nodeSelector string
		if len(p.nodeSelector) > 0 {
			var b strings.Builder
			b.WriteString("  nodeSelector:\n")
			for k, v := range p.nodeSelector {
				b.WriteString(fmt.Sprintf(`    %s: "%s"`, k, v))
				b.WriteString("\n")
			}
			nodeSelector = b.String()
		}
		pod = strings.ReplaceAll(string(b), "<POD_NAME>", p.name)
		pod = strings.ReplaceAll(pod, "<SERVICE_ACCOUNT>", serviceAccountName)
		pod = strings.ReplaceAll(pod, "<HOST_NETWORK>", fmt.Sprint(p.hostNetwork))
		pod = strings.ReplaceAll(pod, "<LOCAL_IMAGE>", localImage)
		pod = strings.ReplaceAll(pod, "<LOCAL_TAG_GOTEST>", localTagGoTest)
		pod = strings.ReplaceAll(pod, "<EXTRA_ENV>", noneRoutingEnv+ebpfEnv+loopbackEnv)
		pod = strings.ReplaceAll(pod, "<NODE_SELECTOR>", nodeSelector)

		// apply
		cmd := exec.Command(
			"kubectl",
			"--context", "kind-gke-metadata-server",
			"apply",
			"-f", "-")
		cmd.Stdin = strings.NewReader(pod)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("error applying pod %s: %v: %s", p.name, err, string(b))
		}
	}
}

func waitForPods(t *testing.T, pods []pod) []int {
	t.Helper()

	var exitCodes []int
	for i, p := range pods {
		const podTimeout = time.Minute
		const sleepSecs = 10
		timeout := time.Now().Add(podTimeout)

		for time.Now().Before(timeout) {
			cmd := exec.Command(
				"kubectl",
				"--context", "kind-gke-metadata-server",
				"--namespace", "default",
				"get", "po", p.name,
				"-o", "jsonpath={.status.containerStatuses[0].state.terminated.exitCode}")
			b, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("error getting pod %s state: %v: %s", p.name, err, string(b))
			}

			if ec := strings.TrimSpace(string(b)); ec != "" {
				exitCode, err := strconv.ParseInt(ec, 10, 64)
				if err != nil {
					t.Fatalf("error parsing exit code '%s' for pod %s: %v", ec, p.name, err)
				}
				exitCodes = append(exitCodes, int(exitCode))
				break
			}

			t.Logf("Sleeping for %d secs and checking pod %s status...", sleepSecs, p.name)
			time.Sleep(sleepSecs * time.Second)
		}

		// If we get here and don't have an exit code for this pod, we timed out
		if len(exitCodes) <= i {
			t.Logf("timed out after %v waiting for pod %s to complete", podTimeout, p.name)
			exitCodes = append(exitCodes, -100) // bogus exit code to indicate timeout
		}
	}

	t.Log("All containers exited")
	return exitCodes
}

func checkExitCodes(t *testing.T, pods []pod, exitCodes []int) {
	t.Helper()

	var failed bool
	for i, p := range pods {
		if exitCodes[i] != p.expectedExitCode {
			failed = true
		}
	}

	if failed {
		printDebugInfo(t, pods, exitCodes)
	}

	for i := range pods {
		t.Logf("Pod %s exit code: %d", pods[i].name, exitCodes[i])
	}

	if failed {
		t.Fail()
	}
}

func printDebugInfo(t *testing.T, pods []pod, exitCodes []int) {
	t.Helper()

	// for each emulator pod
	b, err := exec.Command(
		"kubectl",
		"--context", "kind-gke-metadata-server",
		"--namespace", "kube-system",
		"--output", "name",
		"get", "po").CombinedOutput()
	if err != nil {
		t.Logf("error listing emulator pods: %v: %s", err, string(b))
	} else {
		for _, line := range strings.Split(string(b), "\n") {
			if !strings.Contains(line, "pod/gke-metadata-server") {
				continue
			}
			pod := strings.TrimPrefix(line, "pod/")

			// describe emulator pod
			runDebugCommand(t,
				"kubectl",
				"--context", "kind-gke-metadata-server",
				"--namespace", "kube-system",
				"describe", "po", pod)

			// print emulator logs
			runDebugCommand(t,
				"kubectl",
				"--context", "kind-gke-metadata-server",
				"--namespace", "kube-system",
				"logs", pod)
		}
	}

	for i, p := range pods {
		if exitCodes[i] == p.expectedExitCode {
			continue
		}

		// describe pod
		runDebugCommand(t,
			"kubectl",
			"--context", "kind-gke-metadata-server",
			"--namespace", "default",
			"describe", "po", p.name)

		// print pod logs
		runDebugCommand(t,
			"kubectl",
			"--context", "kind-gke-metadata-server",
			"--namespace", "default",
			"logs", p.name)
	}
}

func runDebugCommand(t *testing.T, cmdName string, cmdArgs ...string) {
	t.Helper()

	b, err := exec.Command(cmdName, cmdArgs...).CombinedOutput()
	if err != nil {
		t.Logf("error running debug command %s %v: %v: %s", cmdName, cmdArgs, err, string(b))
		return
	}
	output := string(b)
	t.Log(output)
	if strings.Contains(output, "no space left on device") {
		b, err := exec.Command("df", "-h").CombinedOutput()
		if err != nil {
			t.Logf("error running df -h: %v: %s", err, string(b))
			return
		}
		t.Logf("Disk space usage:\n%s", string(b))
	}
}
