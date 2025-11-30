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
	// testID is used to isolate test resources.
	testID      = os.Getenv("TEST_ID")
	testImage   = os.Getenv("TEST_IMAGE")
	helmVersion = os.Getenv("HELM_VERSION")

	containerDigest = readDigest("container")
	timoniDigest    = readDigest("timoni")
	helmDigest      = readDigest("helm")
	goTestDigest    = readDigest("go-test")
)

func readDigest(s string) string {
	b, err := os.ReadFile(fmt.Sprintf("%s-digest.txt", s))
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(string(b))
}

func TestEndToEnd(t *testing.T) {
	for _, tt := range []struct {
		name           string
		emulatorValues string
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
			name:           "timoni with watch",
			emulatorValues: "timoni.cue",
			pods: []pod{
				{
					name:               "test-impersonation",
					serviceAccountName: "test-impersonated",
					nodeSelector: map[string]string{
						"hasRoutingMode": "true",
					},
				},
				{
					name:               "test-direct-access",
					serviceAccountName: "test",
					nodeSelector: map[string]string{
						"hasRoutingMode": "true",
					},
				},
				{
					name:               "test-gcloud",
					file:               "pod-gcloud.yaml",
					serviceAccountName: "test",
					nodeSelector: map[string]string{
						"hasRoutingMode": "true",
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
				// for host network pods the service account is retrieved from the node
				{
					name:               "test-host-network",
					serviceAccountName: "",
					hostNetwork:        true,
					nodeSelector: map[string]string{
						"hasRoutingMode":    "true",
						"hasServiceAccount": "true",
					},
				},
				{
					name:               "test-host-network-on-node-without-service-account",
					serviceAccountName: "",
					hostNetwork:        true,
					expectedExitCode:   1,
					nodeSelector: map[string]string{
						"hasRoutingMode":    "true",
						"hasServiceAccount": "false",
					},
				},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if !applyEmulator(t, tt.emulatorValues) {
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

func countEnabledWorkerNodes(t *testing.T) int {
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

	count := 0
	for _, node := range cluster.Nodes {
		if node.Role == "worker" && node.Labels["iam.gke.io/gke-metadata-server-enabled"] == "true" {
			count++
		}
	}

	return count
}

func applyEmulator(t *testing.T, valuesFile string) bool {
	t.Helper()

	// count expected worker nodes from kind.yaml
	expectedNodes := countEnabledWorkerNodes(t)

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
	values = strings.ReplaceAll(values, "<CONTAINER_DIGEST>", containerDigest)

	// detect helm or timoni
	var emulatorCmdName string
	var emulatorCmdArgs []string
	if strings.Contains(valuesFile, "helm") {
		// remove previous helm package
		helmPackage := fmt.Sprintf("gke-metadata-server-helm-%s.tgz", helmVersion)
		os.Remove(helmPackage)

		// pull helm package
		cmd := exec.Command(
			"helm",
			"pull",
			fmt.Sprintf("oci://%s/gke-metadata-server-helm", testImage),
			"--version", helmVersion)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("error pulling helm chart: %v: %s", err, string(b))
		}

		// verify helm package digest
		var digest string
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "Digest:") {
				digest = strings.Fields(line)[1]
				break
			}
		}
		if digest != helmDigest {
			t.Fatalf("expected helm digest %s, got %s", helmDigest, digest)
		}

		// set helm command
		emulatorCmdName = "helm"
		emulatorCmdArgs = []string{
			"--kube-context", "kind-gke-metadata-server",
			"--namespace", "kube-system",
			"upgrade", "--install", "gke-metadata-server", helmPackage,
			"-f", "-",
		}
	} else {
		// set timoni command
		emulatorCmdName = "timoni"
		emulatorCmdArgs = []string{
			"--kube-context", "kind-gke-metadata-server",
			"--namespace", "kube-system",
			"apply", "gke-metadata-server", fmt.Sprintf("oci://%s/timoni", testImage),
			"--digest", timoniDigest,
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
	const emulatorTimeout = time.Minute
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
    env:
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
      value: "$(HOST_IP):$(GKE_METADATA_SERVER_PORT)"
`
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
		pod = strings.ReplaceAll(pod, "<GO_TEST_DIGEST>", fmt.Sprint(goTestDigest))
		pod = strings.ReplaceAll(pod, "<NONE_ROUTING_ENV>", noneRoutingEnv)
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
