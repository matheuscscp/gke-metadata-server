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

package e2e_test

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type emulator struct {
	name      string
	namespace string
	values    string
}

type pod struct {
	name               string
	serviceAccountName string
	hostNetwork        bool
	nodePool           nodePool
	expectedExitCode   int
	expectedLogs       []string
}

type nodePool struct {
	name      string
	namespace string
}

var (
	// testID is used to isolate test resources.
	testID = os.Getenv("TEST_ID")

	testImage       = os.Getenv("TEST_IMAGE")
	containerDigest = os.Getenv("CONTAINER_DIGEST")
	timoniDigest    = os.Getenv("TIMONI_DIGEST")
	helmPackage     = os.Getenv("HELM_PACKAGE")
	goTestDigest    = os.Getenv("GO_TEST_DIGEST")
)

func TestEndToEnd(t *testing.T) {
	for _, tt := range []struct {
		name      string
		emulators []emulator
		pods      []pod
	}{
		{
			name: "helm",
			emulators: []emulator{{
				name:      "gke-metadata-server",
				namespace: "kube-system",
				values:    "helm-values.yaml",
			}},
			pods: []pod{{
				name:               "test-impersonation",
				serviceAccountName: "test-impersonated",
			}},
		},
		{
			name: "timoni without watch",
			emulators: []emulator{{
				name:      "gke-metadata-server",
				namespace: "kube-system",
				values:    "timoni-values-no-watch.cue",
			}},
			pods: []pod{{
				name:               "test-impersonation",
				serviceAccountName: "test-impersonated",
			}},
		},
		{
			name: "timoni with watch",
			emulators: []emulator{{
				name:      "gke-metadata-server",
				namespace: "kube-system",
				values:    "timoni-values-watch.cue",
			}},
			pods: []pod{
				{
					name:               "test-impersonation",
					serviceAccountName: "test-impersonated",
					nodePool: nodePool{
						name:      "gke-metadata-server",
						namespace: "kube-system",
					},
				},
				{
					name:               "test-direct-access",
					serviceAccountName: "test",
					nodePool: nodePool{
						name:      "gke-metadata-server",
						namespace: "kube-system",
					},
				},
				// for host network pods the service account is retrieved from the emulator config
				{
					name:               "test-host-network",
					serviceAccountName: "",
					hostNetwork:        true,
					nodePool: nodePool{
						name:      "gke-metadata-server",
						namespace: "kube-system",
					},
				},
				{
					name:               "test-host-network-outside-node-pool",
					serviceAccountName: "",
					hostNetwork:        true,
					expectedExitCode:   1,
				},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			applyEmulators(t, tt.emulators)
			applyPods(t, tt.pods)
			exitCodes := waitForPods(t, tt.pods)
			checkExitCodes(t, tt.emulators, tt.pods, exitCodes)
		})
	}
}

func applyEmulators(t *testing.T, emulators []emulator) {
	t.Helper()

	// apply
	for _, e := range emulators {
		// delete previous instances
		_ = exec.Command(
			"helm",
			"--kube-context", "kind-gke-metadata-server",
			"--namespace", e.namespace,
			"uninstall", e.name,
			"--wait").Run()
		_ = exec.Command(
			"timoni",
			"--kube-context", "kind-gke-metadata-server",
			"--namespace", e.namespace,
			"delete", e.name,
			"--wait").Run()

		// execute values template
		var values string
		b, err := os.ReadFile(fmt.Sprintf("testdata/%s", e.values))
		require.NoError(t, err)
		values = strings.ReplaceAll(string(b), "<TEST_ID>", testID)
		values = strings.ReplaceAll(values, "<CONTAINER_DIGEST>", containerDigest)

		// detect helm or timoni
		var emulatorCmdName string
		var emulatorCmdArgs []string
		if strings.Contains(e.values, "helm") {
			emulatorCmdName = "helm"
			emulatorCmdArgs = []string{
				"--kube-context", "kind-gke-metadata-server",
				"--namespace", e.namespace,
				"upgrade", "--install", e.name, helmPackage,
				"-f", "-",
			}
		} else {
			emulatorCmdName = "timoni"
			emulatorCmdArgs = []string{
				"--kube-context", "kind-gke-metadata-server",
				"--namespace", e.namespace,
				"apply", e.name, fmt.Sprintf("oci://%s/timoni", testImage),
				"--digest", timoniDigest,
				"-f", "-",
			}
		}

		// apply
		emulatorCmd := exec.Command(emulatorCmdName, emulatorCmdArgs...)
		emulatorCmd.Stdin = strings.NewReader(values)
		if b, err := emulatorCmd.CombinedOutput(); err != nil {
			t.Fatalf("error applying emulator from %s: %v: %s", e.values, err, string(b))
		}
	}

	// wait
	for _, e := range emulators {
		for {
			cmdString := fmt.Sprintf(
				"kubectl --context kind-gke-metadata-server -n %s get ds %s | grep %s | awk '{print $4}'",
				e.namespace, e.name, e.name)
			b, err := exec.Command("sh", "-c", cmdString).CombinedOutput()
			if err != nil {
				t.Fatalf("error getting emulator %s/%s state: %v: %s", e.namespace, e.name, err, string(b))
			}

			if strings.TrimSpace(string(b)) == "1" {
				t.Logf("Emulator %s/%s is ready", e.namespace, e.name)
				break
			}

			const sleepSecs = 10
			t.Logf("Sleeping for %d secs and checking emulator %s/%s status...", sleepSecs, e.namespace, e.name)
			time.Sleep(sleepSecs * time.Second)
		}
	}
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
		b, err := os.ReadFile("testdata/pod.yaml")
		require.NoError(t, err)
		serviceAccountName := "default"
		if sa := p.serviceAccountName; sa != "" {
			serviceAccountName = sa
		}
		var nodeSelectorAndTolerations string
		if p.nodePool != (nodePool{}) {
			nodeSelectorAndTolerations = fmt.Sprintf(`
  nodeSelector:
    gke-metadata-server.matheuscscp.io/nodePoolName: %[1]s
    gke-metadata-server.matheuscscp.io/nodePoolNamespace: %[2]s
  tolerations:
  - key: gke-metadata-server.matheuscscp.io/nodePoolName
    operator: Equal
    value: %[1]s
    effect: NoExecute
  - key: gke-metadata-server.matheuscscp.io/nodePoolNamespace
    operator: Equal
    value: %[2]s
    effect: NoExecute
`,
				p.nodePool.name,
				p.nodePool.namespace)
		}
		pod = strings.ReplaceAll(string(b), "<POD_NAME>", p.name)
		pod = strings.ReplaceAll(pod, "<SERVICE_ACCOUNT>", serviceAccountName)
		pod = strings.ReplaceAll(pod, "<HOST_NETWORK>", fmt.Sprint(p.hostNetwork))
		pod = strings.ReplaceAll(pod, "<GO_TEST_DIGEST>", fmt.Sprint(goTestDigest))
		pod = strings.ReplaceAll(pod, "<NODE_SELECTOR_AND_TOLERATIONS>", nodeSelectorAndTolerations)

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
	for _, p := range pods {
		for {
			cmdString := fmt.Sprintf(
				"kubectl --context kind-gke-metadata-server -n default get po %s -o jsonpath='{.status.containerStatuses[0].state.terminated.exitCode}'",
				p.name)
			b, err := exec.Command("sh", "-c", cmdString).CombinedOutput()
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

			const sleepSecs = 10
			t.Logf("Sleeping for %d secs and checking pod %s status...", sleepSecs, p.name)
			time.Sleep(sleepSecs * time.Second)
		}
	}

	t.Log("All containers exited")
	return exitCodes
}

func checkExitCodes(t *testing.T, emulators []emulator, pods []pod, exitCodes []int) {
	t.Helper()

	var failed bool
	for i, p := range pods {
		if exitCodes[i] != p.expectedExitCode {
			failed = true
		}
	}

	if failed {
		printDebugInfo(t, emulators, pods, exitCodes)
	}

	for _, p := range pods {
		if len(p.expectedLogs) > 0 {
			logs := getTestPodLogs(t, p.name)
			for _, expectedLog := range p.expectedLogs {
				if !strings.Contains(logs, expectedLog) && !failed {
					failed = true
					printDebugInfo(t, emulators, pods, exitCodes)
				}
				assert.Contains(t, logs, expectedLog)
			}
		}
	}

	for i := range pods {
		t.Logf("Pod %s exit code: %d", pods[i].name, exitCodes[i])
	}

	if failed {
		t.Fail()
	}
}

func printDebugInfo(t *testing.T, emulators []emulator, pods []pod, exitCodes []int) {
	t.Helper()

	for _, e := range emulators {
		// describe emulator pod
		cmdString := fmt.Sprintf(
			"kubectl --context kind-gke-metadata-server -n %s describe ds %s",
			e.namespace,
			e.name)
		runDebugCommand(t, "sh", "-c", cmdString)

		// print emulator logs
		runDebugCommand(t,
			"kubectl",
			"--context", "kind-gke-metadata-server",
			"--namespace", e.namespace,
			"logs", "ds/"+e.name)
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
		t.Log(getTestPodLogs(t, p.name))
	}
}

func runDebugCommand(t *testing.T, cmdName string, cmdArgs ...string) {
	t.Helper()

	t.Log(runCommand(t, cmdName, cmdArgs...))
}

func runCommand(t *testing.T, cmdName string, cmdArgs ...string) string {
	t.Helper()

	b, err := exec.Command(cmdName, cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("error running command %s %v: %v: %s", cmdName, cmdArgs, err, string(b))
	}
	return string(b)
}

func getTestPodLogs(t *testing.T, podName string) string {
	t.Helper()

	return runCommand(t,
		"kubectl",
		"--context", "kind-gke-metadata-server",
		"--namespace", "default",
		"logs", podName)
}
