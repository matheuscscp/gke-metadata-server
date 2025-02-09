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

	"github.com/stretchr/testify/require"
)

type pod struct {
	name               string
	serviceAccountName string
	hostNetwork        bool
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
		name           string
		emulatorValues string
		pods           []pod
	}{
		{
			name:           "helm",
			emulatorValues: "helm-values.yaml",
			pods: []pod{{
				name:               "test-impersonation",
				serviceAccountName: "test-impersonated",
			}},
		},
		{
			name:           "timoni without watch",
			emulatorValues: "timoni-values-no-watch.cue",
			pods: []pod{{
				name:               "test-impersonation",
				serviceAccountName: "test-impersonated",
			}},
		},
		{
			name:           "timoni with watch",
			emulatorValues: "timoni-values-watch.cue",
			pods: []pod{
				{
					name:               "test-impersonation",
					serviceAccountName: "test-impersonated",
				},
				{
					name:               "test-direct-access",
					serviceAccountName: "test",
				},
				{
					name:               "test-host-network",
					serviceAccountName: "", // in this case the service account is retrieved from the emulator config
					hostNetwork:        true,
				},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			uninstallHelmRelease(t)
			deleteTimoniInstance(t)
			applyEmulator(t, tt.emulatorValues)
			waitForEmulator(t)
			applyPods(t, tt.pods)
			exitCodes := waitForPods(t, tt.pods)
			checkExitCodes(t, tt.pods, exitCodes)
		})
	}
}

func uninstallHelmRelease(t *testing.T) {
	t.Helper()

	_ = exec.Command(
		"helm",
		"--kube-context", "kind-gke-metadata-server",
		"--namespace", "kube-system",
		"uninstall", "gke-metadata-server",
		"--wait").Run()
}

func deleteTimoniInstance(t *testing.T) {
	t.Helper()

	_ = exec.Command(
		"timoni",
		"--kube-context", "kind-gke-metadata-server",
		"--namespace", "kube-system",
		"delete", "gke-metadata-server",
		"--wait").Run()
}

func applyEmulator(t *testing.T, valuesFile string) {
	t.Helper()

	b, err := os.ReadFile(fmt.Sprintf("testdata/%s", valuesFile))
	require.NoError(t, err)

	emulatorValues := strings.ReplaceAll(string(b), "<TEST_ID>", testID)
	emulatorValues = strings.ReplaceAll(emulatorValues, "<CONTAINER_DIGEST>", containerDigest)

	var emulatorCmdName string
	var emulatorCmdArgs []string
	if strings.Contains(valuesFile, "helm") {
		emulatorCmdName = "helm"
		emulatorCmdArgs = []string{
			"--kube-context", "kind-gke-metadata-server",
			"--namespace", "kube-system",
			"upgrade", "--install", "gke-metadata-server", helmPackage,
			"--wait",
			"-f", "-",
		}
	} else {
		emulatorCmdName = "timoni"
		emulatorCmdArgs = []string{
			"--kube-context", "kind-gke-metadata-server",
			"--namespace", "kube-system",
			"apply", "gke-metadata-server", fmt.Sprintf("oci://%s/timoni", testImage),
			"--digest", timoniDigest,
			"--wait",
			"-f", "-",
		}
	}

	emulatorCmd := exec.Command(emulatorCmdName, emulatorCmdArgs...)
	emulatorCmd.Stdin = strings.NewReader(emulatorValues)
	if b, err := emulatorCmd.CombinedOutput(); err != nil {
		t.Fatalf("error applying emulator from %s: %v: %s", valuesFile, err, string(b))
	}
}

func waitForEmulator(t *testing.T) {
	t.Helper()

	for {
		cmd := exec.Command("sh", "-c",
			"kubectl --context kind-gke-metadata-server -n kube-system get ds gke-metadata-server | grep gke | awk '{print $4}'")
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("error getting emulator state: %v: %s", err, string(b))
		}

		if strings.TrimSpace(string(b)) == "1" {
			t.Log("Emulator is ready")
			return
		}

		const sleepSecs = 10
		t.Logf("Sleeping for %d secs and checking DaemonSet status...", sleepSecs)
		time.Sleep(sleepSecs * time.Second)
	}
}

func applyPods(t *testing.T, pods []pod) {
	t.Helper()

	for _, p := range pods {
		_ = exec.Command(
			"kubectl",
			"--context", "kind-gke-metadata-server",
			"--namespace", "default",
			"delete", "po", p.name).Run()

		b, err := os.ReadFile("testdata/pod.yaml")
		require.NoError(t, err)

		serviceAccountName := "default"
		if sa := p.serviceAccountName; sa != "" {
			serviceAccountName = sa
		}

		pod := strings.ReplaceAll(string(b), "<POD_NAME>", p.name)
		pod = strings.ReplaceAll(pod, "<SERVICE_ACCOUNT>", serviceAccountName)
		pod = strings.ReplaceAll(pod, "<HOST_NETWORK>", fmt.Sprint(p.hostNetwork))
		pod = strings.ReplaceAll(pod, "<GO_TEST_DIGEST>", fmt.Sprint(goTestDigest))

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

	for {
		var exitCodes []int
		for _, p := range pods {
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
			}
		}

		if len(exitCodes) == len(pods) {
			t.Log("All containers exited")
			return exitCodes
		}

		const sleepSecs = 10
		t.Logf("Sleeping for %d secs and checking test Pod status...", sleepSecs)
		time.Sleep(sleepSecs * time.Second)
	}
}

func checkExitCodes(t *testing.T, pods []pod, exitCodes []int) {
	t.Helper()

	var failed bool
	for i := range pods {
		t.Logf("Pod '%s' exit code: %d", pods[i].name, exitCodes[i])
		if exitCodes[i] != 0 {
			failed = true
		}
	}

	if !failed {
		return
	}

	printDebugInfo(t, pods)
	t.Fail()
}

func printDebugInfo(t *testing.T, pods []pod) {
	t.Helper()

	// describe emulator pod
	runDebugCommand(t, "sh", "-c",
		"kubectl --context kind-gke-metadata-server -n kube-system describe $(kubectl --context kind-gke-metadata-server -n kube-system get po -o name | grep gke)")

	// print emulator logs
	runDebugCommand(t,
		"kubectl",
		"--context", "kind-gke-metadata-server",
		"--namespace", "kube-system",
		"logs", "ds/gke-metadata-server")

	for _, p := range pods {
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

	cmd := exec.Command(cmdName, cmdArgs...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("error running debug command %s %v: %v: %s", cmdName, cmdArgs, err, string(b))
	}
	t.Log(string(b))
}
