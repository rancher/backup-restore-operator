package upgrade_metrics_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/backup-restore-operator/pkg/util"
	"github.com/rancher/wrangler/v3/pkg/kubeconfig"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	backupv1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	. "github.com/kralicky/kmatch"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

var (
	ts           *TestSpec
	k8sClient    client.Client
	cfg          *rest.Config
	testCtx      context.Context
	clientSet    *kubernetes.Clientset
	clientCmdCfg clientcmd.ClientConfig
	restCfg      *rest.Config

	clusterName = "bro-upgrade-metrics"
	k3sVersion  = "v1.36.1-k3s1"
)

func TestUpgradeMetrics(t *testing.T) {
	SetDefaultEventuallyTimeout(20 * time.Second)
	SetDefaultEventuallyPollingInterval(50 * time.Millisecond)
	SetDefaultConsistentlyDuration(10 * time.Second)
	SetDefaultConsistentlyPollingInterval(50 * time.Millisecond)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Upgrade Metrics Suite")
}

type TestSpec struct {
	Kubeconfig     string
	ChartNamespace string
}

func (t *TestSpec) Validate() error {
	return nil
}

var _ = BeforeSuite(func() {
	By("creating k3d cluster for upgrade-metrics tests")
	ts = &TestSpec{
		ChartNamespace: "cattle-resources-system",
	}

	// Delete cluster if it exists (cleanup from previous failed run)
	exec.Command("k3d", "cluster", "delete", clusterName).Run()

	// Create k3d cluster
	cmd := exec.Command("k3d", "cluster", "create", clusterName,
		"--image", fmt.Sprintf("rancher/k3s:%s", k3sVersion),
		"--wait",
		"--timeout", "3m",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("k3d cluster create output: %s\n", string(output))
		Fail(fmt.Sprintf("Failed to create k3d cluster: %v", err))
	}

	// Get kubeconfig
	kubeconfigPath := fmt.Sprintf("/tmp/%s-kubeconfig.yaml", clusterName)
	cmd = exec.Command("k3d", "kubeconfig", "get", clusterName)
	kubeconfigBytes, err := cmd.Output()
	Expect(err).NotTo(HaveOccurred(), "Failed to get kubeconfig")

	err = os.WriteFile(kubeconfigPath, kubeconfigBytes, 0600)
	Expect(err).NotTo(HaveOccurred(), "Failed to write kubeconfig")

	ts.Kubeconfig = kubeconfigPath

	// Set up context
	ctxCa, ca := context.WithCancel(context.Background())
	DeferCleanup(func() {
		ca()
	})
	util.SetDevMode(true)
	testCtx = ctxCa

	// Initialize k8s clients
	clientCmdCfg = kubeconfig.GetNonInteractiveClientConfig(ts.Kubeconfig)

	restConfig, err := clientCmdCfg.ClientConfig()
	Expect(err).NotTo(HaveOccurred(), "Could not initialize kubernetes client config")
	restCfg = restConfig

	newCfg, err := config.GetConfigWithContext("")
	Expect(err).NotTo(HaveOccurred(), "Could not initialize kubernetes client config")
	cfg = newCfg

	newClientset, err := kubernetes.NewForConfig(cfg)
	Expect(err).To(Succeed(), "Could not initialize kubernetes clientset")
	clientSet = newClientset

	newK8sClient, err := client.New(cfg, client.Options{})
	Expect(err).NotTo(HaveOccurred(), "Could not initialize kubernetes client")
	k8sClient = newK8sClient

	apiextensionsv1.AddToScheme(k8sClient.Scheme())
	backupv1.AddToScheme(k8sClient.Scheme())
	SetDefaultObjectClient(k8sClient)

	By("creating chart namespace")
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ts.ChartNamespace,
		},
	}
	err = k8sClient.Create(testCtx, namespace)
	Expect(err).NotTo(HaveOccurred(), "Failed to create chart namespace")

	By("installing CRD chart")
	// Find the packaged CRD chart
	cmd = exec.Command("bash", "-c", "ls ../../build/artifacts/rancher-backup-crd-*.tgz | head -1")
	crdChartPath, err := cmd.Output()
	if err != nil || len(crdChartPath) == 0 {
		Fail("Could not find packaged CRD chart in ../../build/artifacts/")
	}

	cmd = exec.Command("helm", "install", "rancher-backup-crd",
		string(crdChartPath[:len(crdChartPath)-1]), // trim newline
		"--namespace", ts.ChartNamespace,
		"--wait",
		"--timeout", "2m",
	)
	output, err = cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Helm CRD install output: %s\n", string(output))
		Fail(fmt.Sprintf("Failed to install CRD chart: %v", err))
	}

	By("verifying CRDs are installed")
	Eventually(func() bool {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		err := k8sClient.Get(testCtx, client.ObjectKey{Name: "backups.resources.cattle.io"}, crd)
		return err == nil
	}, 30*time.Second, 1*time.Second).Should(BeTrue(), "Backup CRD should exist")

	Eventually(func() bool {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		err := k8sClient.Get(testCtx, client.ObjectKey{Name: "restores.resources.cattle.io"}, crd)
		return err == nil
	}, 30*time.Second, 1*time.Second).Should(BeTrue(), "Restore CRD should exist")

	Eventually(func() bool {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		err := k8sClient.Get(testCtx, client.ObjectKey{Name: "resourcesets.resources.cattle.io"}, crd)
		return err == nil
	}, 30*time.Second, 1*time.Second).Should(BeTrue(), "ResourceSet CRD should exist")
})

var _ = AfterSuite(func() {
	By("destroying k3d cluster")
	cmd := exec.Command("k3d", "cluster", "delete", clusterName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("Warning: Failed to delete k3d cluster: %s\n", string(output))
	}

	// Clean up kubeconfig file
	if ts != nil && ts.Kubeconfig != "" {
		os.Remove(ts.Kubeconfig)
	}
})
