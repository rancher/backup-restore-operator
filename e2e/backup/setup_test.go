package backup_test

import (
	"context"
	"fmt"

	. "github.com/kralicky/kmatch"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/backup-restore-operator/e2e/test"
	backupv1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/rancher/backup-restore-operator/pkg/operator"
	backuputil "github.com/rancher/backup-restore-operator/pkg/util"
	"github.com/testcontainers/testcontainers-go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/rest"
)

func SetupRancherResourceSet(o *ObjectTracker) {
	rsc := test.Data("rancher-resource-set.yaml")
	rscObj := &backupv1.ResourceSet{}
	Expect(yaml.Unmarshal(rsc, rscObj)).To(Succeed())
	o.Add(rscObj)
	Expect(k8sClient.Create(testCtx, rscObj))
	Eventually(Object(rscObj)).Should(Exist())
}

func SetupOperator(ctx context.Context, kubeconfig *rest.Config, options operator.RunOptions) (chan error, context.CancelFunc) {
	By("running the operator locally")
	ctxca, ca := context.WithCancel(ctx)
	errC := make(chan error, 1)
	go func() {
		err := operator.Run(ctxca, kubeconfig, options)
		errC <- err
	}()
	return errC, ca
}

func SetupEncryption(o *ObjectTracker) {
	By("creating a generic secret for encryption configuration")
	payload := test.Data("encryption.yaml")
	encsecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      encSecret,
			Namespace: ts.ChartNamespace,
		},
		Data: map[string][]byte{
			backuputil.EncryptionProviderConfigKey: payload,
		},
	}
	o.Add(encsecret)
	Expect(k8sClient.Create(testCtx, encsecret)).To(Succeed())
	Eventually(encsecret).Should(Exist())
}

func SetupMinio(o *ObjectTracker) (client *minio.Client, minioEndpoint string) {
	By("deploying minio locally")

	req := testcontainers.ContainerRequest{
		Image: "minio/minio",
		Env: map[string]string{
			"MINIO_ROOT_USER":     accessKey,
			"MINIO_ROOT_PASSWORD": secretKey,
		},
		Entrypoint: []string{"minio", "server", "/data"},
		LogConsumerCfg: &testcontainers.LogConsumerConfig{
			Consumers: []testcontainers.LogConsumer{&containerLogWrapper{}},
		},
		ExposedPorts: []string{"9000"},
	}

	minioC, err := testcontainers.GenericContainer(testCtx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Logger:           GinkgoWriter,
	})
	Expect(err).To(Succeed())

	err = minioC.Start(testCtx)
	Expect(err).To(Succeed())

	DeferCleanup(func() {
		minioC.Terminate(testCtx)
	})

	By("verifying minio is successfully deployed")
	fmt.Fprintf(GinkgoWriter, "Minio container ID : %s\n", minioC.GetContainerID())

	port, err := minioC.MappedPort(testCtx, "9000")
	Expect(err).To(Succeed())

	fmt.Fprintf(GinkgoWriter, "Container port : %s\n", port)
	minioEndpoint = fmt.Sprintf("localhost:%s", port.Port())
	fmt.Fprintf(GinkgoWriter, "Minio endpoint : %s\n", minioEndpoint)
	client, err = minio.New(minioEndpoint, &minio.Options{
		Creds: credentials.NewStaticV4(accessKey, secretKey, ""),
	})
	Expect(err).To(Succeed())

	By("deploying a secret that containers the authentication for minio")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      credentialSecretName,
			Namespace: ts.ChartNamespace,
		},
		Data: map[string][]byte{
			"accessKey":  []byte(accessKey),
			"secretKey":  []byte(secretKey),
			"disableSSL": []byte("true"),
		},
	}
	o.Add(secret)
	Expect(k8sClient.Create(testCtx, secret)).To(Succeed())
	Eventually(Object(secret)).Should(Exist())
	return client, minioEndpoint
}
