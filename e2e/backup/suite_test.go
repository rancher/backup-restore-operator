package backup_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/backup-restore-operator/pkg/operator"
	backuputil "github.com/rancher/backup-restore-operator/pkg/util"
	"github.com/rancher/wrangler/v3/pkg/kubeconfig"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	backupv1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	env "github.com/caarlos0/env/v11"
	"github.com/kralicky/kmatch"
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
)

func TestTests(t *testing.T) {
	SetDefaultEventuallyTimeout(10 * time.Second)
	SetDefaultEventuallyPollingInterval(50 * time.Millisecond)
	SetDefaultConsistentlyDuration(10 * time.Second)
	SetDefaultConsistentlyPollingInterval(50 * time.Millisecond)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tests Suite")
}

type TestSpec struct {
	Kubeconfig     string `env:"KUBECONFIG,required"`
	ChartNamespace string `env:"CHART_NAMESPACE,required"`
}

func (t *TestSpec) Validate() error {
	var errs []error
	if _, err := os.Stat(t.Kubeconfig); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

type ObjectTracker struct {
	mu  sync.Mutex
	arr []client.Object
}

func (o *ObjectTracker) Add(obj client.Object) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.arr = append(o.arr, obj)
}

func (o *ObjectTracker) DeleteAll() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, obj := range o.arr {
		_ = k8sClient.Delete(testCtx, obj)
	}
}

var _ = BeforeSuite(func() {

	ts = &TestSpec{}
	Expect(env.Parse(ts)).To(Succeed(), "Could not parse test spec from environment variables")
	Expect(ts.Validate()).To(Succeed(), "Invalid input e2e test spec")

	ctxCa, ca := context.WithCancel(context.Background())
	DeferCleanup(func() {
		ca()
	})
	backuputil.SetDevMode(true)

	clientCmdCfg = kubeconfig.GetNonInteractiveClientConfig(ts.Kubeconfig)

	restConfig, err := clientCmdCfg.ClientConfig()
	Expect(err).NotTo(HaveOccurred(), "Could not initialize kubernetes client config")
	restCfg = restConfig

	testCtx = ctxCa
	newCfg, err := config.GetConfig()
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
	kmatch.SetDefaultObjectClient(k8sClient)

	o := &ObjectTracker{
		arr: []client.Object{},
		mu:  sync.Mutex{},
	}

	DeferCleanup(func() {
		o.DeleteAll()
	})

	By("verifying the chart namespace exists")
	Eventually(Object(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ts.ChartNamespace,
		},
	})).Should(Exist())

	By("verifying the required crds are present in the cluster")

	backupGvk := schema.GroupVersionKind{
		Group:   "resources.cattle.io",
		Version: "v1",
		Kind:    "Backup",
	}

	restoreGvk := schema.GroupVersionKind{
		Group:   "resources.cattle.io",
		Version: "v1",
		Kind:    "Restore",
	}

	resourceSetGvk := schema.GroupVersionKind{
		Group:   "resources.cattle.io",
		Version: "v1",
		Kind:    "ResourceSet",
	}
	Eventually(GVK(backupGvk)).Should(Exist())
	Eventually(GVK(restoreGvk)).Should(Exist())
	Eventually(GVK(resourceSetGvk)).Should(Exist())

	fmt.Fprintf(GinkgoWriter, "Start with CHART_NAMESPACE : '%s'", ts.ChartNamespace)

	SetupRancherResourceSet(o)

	errC, ca := SetupOperator(testCtx, restConfig, operator.RunOptions{
		ChartNamespace:       ts.ChartNamespace,
		MetricsServerEnabled: true,
		MetricsPort:          8080,
		MetricsInterval:      5,
	})

	DeferCleanup(func() {
		ca()
		select {
		case err := <-errC:
			Expect(err).NotTo(HaveOccurred())
		default:
		}
	})
})
