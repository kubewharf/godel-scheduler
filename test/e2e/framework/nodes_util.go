/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package framework

import (
	"fmt"
	"os"
	"path"
	"sync"
	"time"

	"github.com/onsi/ginkgo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"

	e2enode "github.com/kubewharf/godel-scheduler/test/e2e/framework/node"
	e2essh "github.com/kubewharf/godel-scheduler/test/e2e/framework/ssh"
)

const etcdImage = "3.4.13-0"

// EtcdUpgrade upgrades etcd on GCE.
func EtcdUpgrade(targetStorage, targetVersion string) error {
	switch TestContext.Provider {
	case "gce":
		return etcdUpgradeGCE(targetStorage, targetVersion)
	default:
		return fmt.Errorf("EtcdUpgrade() is not implemented for provider %s", TestContext.Provider)
	}
}

// MasterUpgrade upgrades master node on GCE/GKE.
func MasterUpgrade(f *Framework, v string, extraEnvs []string) error {
	switch TestContext.Provider {
	case "gce":
		return masterUpgradeGCE(v, extraEnvs)
	case "gke":
		return MasterUpgradeGKE(f.Namespace.Name, v)
	default:
		return fmt.Errorf("MasterUpgrade() is not implemented for provider %s", TestContext.Provider)
	}
}

func etcdUpgradeGCE(targetStorage, targetVersion string) error {
	env := append(
		os.Environ(),
		"TEST_ETCD_VERSION="+targetVersion,
		"STORAGE_BACKEND="+targetStorage,
		"TEST_ETCD_IMAGE="+etcdImage)

	_, _, err := RunCmdEnv(env, GCEUpgradeScript(), "-l", "-M")
	return err
}

// MasterUpgradeGCEWithKubeProxyDaemonSet upgrades master node on GCE with enabling/disabling the daemon set of kube-proxy.
// TODO(mrhohn): Remove this function when kube-proxy is run as a DaemonSet by default.
func MasterUpgradeGCEWithKubeProxyDaemonSet(v string, enableKubeProxyDaemonSet bool) error {
	return masterUpgradeGCE(v, []string{fmt.Sprintf("KUBE_PROXY_DAEMONSET=%v", enableKubeProxyDaemonSet)})
}

// TODO(mrhohn): Remove 'enableKubeProxyDaemonSet' when kube-proxy is run as a DaemonSet by default.
func masterUpgradeGCE(rawV string, extraEnvs []string) error {
	env := append(os.Environ(), extraEnvs...)
	// TODO: Remove these variables when they're no longer needed for downgrades.
	if TestContext.EtcdUpgradeVersion != "" && TestContext.EtcdUpgradeStorage != "" {
		env = append(env,
			"TEST_ETCD_VERSION="+TestContext.EtcdUpgradeVersion,
			"STORAGE_BACKEND="+TestContext.EtcdUpgradeStorage,
			"TEST_ETCD_IMAGE="+etcdImage)
	} else {
		// In e2e tests, we skip the confirmation prompt about
		// implicit etcd upgrades to simulate the user entering "y".
		env = append(env, "TEST_ALLOW_IMPLICIT_ETCD_UPGRADE=true")
	}

	v := "v" + rawV
	_, _, err := RunCmdEnv(env, GCEUpgradeScript(), "-M", v)
	return err
}

// LocationParamGKE returns parameter related to location for gcloud command.
func LocationParamGKE() string {
	if TestContext.CloudConfig.MultiMaster {
		// GKE Regional Clusters are being tested.
		return fmt.Sprintf("--region=%s", TestContext.CloudConfig.Region)
	}
	return fmt.Sprintf("--zone=%s", TestContext.CloudConfig.Zone)
}

// AppendContainerCommandGroupIfNeeded returns container command group parameter if necessary.
func AppendContainerCommandGroupIfNeeded(args []string) []string {
	if TestContext.CloudConfig.Region != "" {
		// TODO(wojtek-t): Get rid of it once Regional Clusters go to GA.
		return append([]string{"beta"}, args...)
	}
	return args
}

// MasterUpgradeGKE upgrades master node to the specified version on GKE.
func MasterUpgradeGKE(namespace string, v string) error {
	Logf("Upgrading master to %q", v)
	args := []string{
		"container",
		"clusters",
		fmt.Sprintf("--project=%s", TestContext.CloudConfig.ProjectID),
		LocationParamGKE(),
		"upgrade",
		TestContext.CloudConfig.Cluster,
		"--master",
		fmt.Sprintf("--cluster-version=%s", v),
		"--quiet",
	}
	_, _, err := RunCmd("gcloud", AppendContainerCommandGroupIfNeeded(args)...)
	if err != nil {
		return err
	}

	WaitForSSHTunnels(namespace)

	return nil
}

// GCEUpgradeScript returns path of script for upgrading on GCE.
func GCEUpgradeScript() string {
	if len(TestContext.GCEUpgradeScript) == 0 {
		return path.Join(TestContext.RepoRoot, "cluster/gce/upgrade.sh")
	}
	return TestContext.GCEUpgradeScript
}

// WaitForSSHTunnels waits for establishing SSH tunnel to busybox pod.
func WaitForSSHTunnels(namespace string) {
	Logf("Waiting for SSH tunnels to establish")
	RunKubectl(namespace, "run", "ssh-tunnel-test",
		"--image=busybox",
		"--restart=Never",
		"--command", "--",
		"echo", "Hello")
	defer RunKubectl(namespace, "delete", "pod", "ssh-tunnel-test")

	// allow up to a minute for new ssh tunnels to establish
	wait.PollImmediate(5*time.Second, time.Minute, func() (bool, error) {
		_, err := RunKubectl(namespace, "logs", "ssh-tunnel-test")
		return err == nil, nil
	})
}

// NodeKiller is a utility to simulate node failures.
type NodeKiller struct {
	config   NodeKillerConfig
	client   clientset.Interface
	provider string
}

// NewNodeKiller creates new NodeKiller.
func NewNodeKiller(config NodeKillerConfig, client clientset.Interface, provider string) *NodeKiller {
	config.NodeKillerStopCh = make(chan struct{})
	return &NodeKiller{config, client, provider}
}

// Run starts NodeKiller until stopCh is closed.
func (k *NodeKiller) Run(stopCh <-chan struct{}) {
	// wait.JitterUntil starts work immediately, so wait first.
	time.Sleep(wait.Jitter(k.config.Interval, k.config.JitterFactor))
	wait.JitterUntil(func() {
		nodes := k.pickNodes()
		k.kill(nodes)
	}, k.config.Interval, k.config.JitterFactor, true, stopCh)
}

func (k *NodeKiller) pickNodes() []v1.Node {
	nodes, err := e2enode.GetReadySchedulableNodes(k.client)
	ExpectNoError(err)
	numNodes := int(k.config.FailureRatio * float64(len(nodes.Items)))

	nodes, err = e2enode.GetBoundedReadySchedulableNodes(k.client, numNodes)
	ExpectNoError(err)
	return nodes.Items
}

func (k *NodeKiller) kill(nodes []v1.Node) {
	wg := sync.WaitGroup{}
	wg.Add(len(nodes))
	for _, node := range nodes {
		node := node
		go func() {
			defer ginkgo.GinkgoRecover()
			defer wg.Done()

			Logf("Stopping docker and kubelet on %q to simulate failure", node.Name)
			err := e2essh.IssueSSHCommand("sudo systemctl stop docker kubelet", k.provider, &node)
			if err != nil {
				Logf("ERROR while stopping node %q: %v", node.Name, err)
				return
			}

			time.Sleep(k.config.SimulatedDowntime)

			Logf("Rebooting %q to repair the node", node.Name)
			err = e2essh.IssueSSHCommand("sudo reboot", k.provider, &node)
			if err != nil {
				Logf("ERROR while rebooting node %q: %v", node.Name, err)
				return
			}
		}()
	}
	wg.Wait()
}
