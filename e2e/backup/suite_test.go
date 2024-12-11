package backup_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/kralicky/kmatch"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/wrangler/v3/pkg/kubeconfig"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	configv1 "k8s.io/apiserver/pkg/apis/apiserver/v1"

	backupv1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	backuputil "github.com/rancher/backup-restore-operator/pkg/util"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	env "github.com/caarlos0/env/v11"
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
)

var (
	encryptionConfig = &configv1.EncryptionConfiguration{
		// Funny: they make it annoying for using non-supported internal api types haha

		// TypeMeta: metav1.TypeMeta{
		// 	Kind:       "EncryptionConfiguration",
		// 	APIVersion: "apiserver.config.k8s.io/v1",
		// },
		Resources: []configv1.ResourceConfiguration{
			{
				Resources: []string{"*.*"},
				Providers: []configv1.ProviderConfiguration{
					{
						Secretbox: &configv1.SecretboxConfiguration{
							Keys: []configv1.Key{
								{
									Name:   "key1",
									Secret: "YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY=",
								},
							},
						},
					},
				},
			},
		},
	}
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
	HelmRelease    string `env:"HELM_RELEASE"`
}

func (t *TestSpec) ifOperatorDeployed() bool {
	return t.HelmRelease != ""
}

func (t *TestSpec) Validate() error {
	var errs []error
	if _, err := os.Stat(t.Kubeconfig); err != nil {
		errs = append(errs, err)
	}
	if t.ChartNamespace == "" {
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

var _ = BeforeSuite(func() {
	ts = &TestSpec{}
	Expect(env.Parse(ts)).To(Succeed(), "Could not parse test spec from environment variables")
	Expect(ts.Validate()).To(Succeed(), "Invalid input e2e test spec")

	ctxCa, ca := context.WithCancel(context.Background())
	DeferCleanup(func() {
		ca()
	})

	clientCmdCfg = kubeconfig.GetNonInteractiveClientConfig(ts.Kubeconfig)

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

	By("creating a generic secret with encryption configuration")
	content := []byte(`apiVersion: apiserver.config.k8s.io/v1
kind: EncryptionConfiguration
resources:
- resources:
  - "*.*"
providers:
  - secretbox:
	  keys:
		- name: key1
		  secret: YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY=
`)

	// TODO : they make marshalling structs difficult to work with since its an internal api struct definition
	// and no an API object
	_, err = yaml.Marshal(encryptionConfig.Resources)
	Expect(err).ToNot(HaveOccurred())

	payload := content

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      encSecret,
			Namespace: ts.ChartNamespace,
		},
		Data: map[string][]byte{
			backuputil.EncryptionProviderConfigKey: payload,
		},
	}

	DeferCleanup(func() {
		_ = k8sClient.Delete(testCtx, secret)
	})

	Expect(k8sClient.Create(testCtx, secret)).To(Succeed())

	DeferCleanup(func() {
		_ = k8sClient.Delete(testCtx, secret)
	})
	Eventually(secret).Should(Exist())
})
