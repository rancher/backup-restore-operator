package backup_test

import (
	. "github.com/kralicky/kmatch"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	backupv1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	encSecret                = "encryption-config"
	localCattleDevDriverPath = "../../backups"
)

var _ = Describe("Local persistence driver Backup & Restore e2e tests", Ordered, Label("integration"), func() {
	BeforeAll(func() {
		if ts.ifOperatorDeployed() {
			By("validating the rancher backup restore operator chart is healthy")

			deploy := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "rancher-backup",
					Namespace: ts.ChartNamespace,
				},
			}
			Eventually(deploy).Should(Exist())
			Eventually(deploy).Should(HaveSuccessfulRollout())
		}

		By("checking the default rancher resource set exists")
		// TODO
	})

	When("we take a non-encrypted backup", func() {
		It("should create a backup CRD", func() {
			b := backupv1.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "local-driver-non-encrypted",
				},
				Spec: backupv1.BackupSpec{
					ResourceSetName: "rancher-resource-set",
				},
			}

			Expect(k8sClient.Create(testCtx, &b)).To(Succeed())
			Eventually(&b).Should(Exist())
		})

		Specify("the backup should be successful", func() {
			// TODO : check conditions
		})

		Specify("the backup should be persisted", func() {
			if ts.ifOperatorDeployed() {
				// TODO : check local driver
			}
		})

		Specify("the backup should track the correct objects", func() {
			// TODO
		})
	})

	When("we take an encrypted backup", func() {
		It("should be able to create an encrypted backup configuration", func() {
			b := backupv1.Backup{
				ObjectMeta: metav1.ObjectMeta{
					Name: "local-driver-encrypted",
				},
				Spec: backupv1.BackupSpec{
					ResourceSetName:            "rancher-resource-set",
					EncryptionConfigSecretName: encSecret,
				},
			}
			Expect(k8sClient.Create(testCtx, &b)).To(Succeed())
			// DeferCleanup(
			// 	func() {
			// 		_ = k8sClient.Delete(testCtx, &b)
			// 	},
			// )
			// Expect(k8sClient.Create(testCtx, &b)).Should(Succeed())

			By("verifying the backup resource exists")
			Eventually(&b).Should(Exist())
		})

		Specify("the backup should be successful", func() {
			// TODO : check backup resource conditions
		})

		Specify("the backup should be persisted", func() {
			if ts.ifOperatorDeployed() {
				// TODO : check local driver
			}
		})

		Specify("the backup should track the correct objects", func() {

		})
	})
})
