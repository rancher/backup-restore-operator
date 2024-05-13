package hull

import (
	"strconv"
	"strings"

	"github.com/rancher/hull/pkg/chart"
	"github.com/rancher/hull/pkg/checker"
	"github.com/rancher/hull/pkg/test"
	"github.com/rancher/hull/pkg/utils"
	"github.com/stretchr/testify/assert"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	rbacv1 "k8s.io/api/rbac/v1"
	resource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var ChartPath = utils.MustGetPathFromModuleRoot("..", "dist", "artifacts", GetChartVersionFromEnv())

var (
	DefaultReleaseName = "rancher-backup"
	DefaultNamespace   = "cattle-resources-system"
)

var suite = test.Suite{
	ChartPath: ChartPath,

	Cases: []test.Case{
		{
			Name: "Using Defaults",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace),
		},
		{
			Name: "Set .Values.debug to true",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				SetValue(
					"debug", "true",
				),
		},
		{
			Name: "Set .Values.trace to true",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				SetValue(
					"trace", "true",
				),
		},
		{
			Name: "Set .Values.debug and .Values.trace to true",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				SetValue(
					"trace", "true",
				).
				SetValue(
					"debug", "true",
				),
		},
		{
			Name: "Set .Values.imagePullPolicy to Always",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				SetValue(
					"imagePullPolicy", "Always",
				),
		},
		{
			Name: "Set .Values.imagePullPolicy to Never",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				SetValue(
					"imagePullPolicy", "Never",
				),
		},
		{
			Name: "Set .Values.imagePullPolicy to IfNotPresent",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				SetValue(
					"imagePullPolicy", "IfNotPresent",
				),
		},
		{
			Name: "Set PriorityClassName",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				SetValue(
					"priorityClassName", "high-priority-nonpreempting",
				),
		},
		{
			Name: "Set .Values.persistence false",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				SetValue(
					"persistence.enabled", "false",
				).
				SetValue(
					"persistence.volumeName", "pv_test_name",
				),
		},
		{
			Name: "Set .Values.persistence true",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				Set("persistence", map[string]interface{}{
					"enabled":      true,
					"volumeName":   "pv_test_name",
					"size":         "4Gi",
					"storageClass": "aws",
				}),
		},
		{
			Name: "Set .Values.s3 false",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				Set(
					"s3", map[string]interface{}{
						"enabled": false,
					},
				),
		},
		{
			Name: "Set .Values.s3 true",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				Set(
					"s3", map[string]interface{}{
						"enabled":                   true,
						"bucketName":                "test-bucket",
						"credentialSecretName":      "s3-secret",
						"credentialSecretNamespace": "cattle-resources-system",
						"endpoint":                  "s3.us-east-2.amazonaws.com",
						"endpointCA":                "test-ca",
						"folder":                    "rancher",
						"insecureTLSSkipVerify":     true,
						"region":                    "us-east-2",
					},
				),
		},
		{
			Name: "Set Node Affinity",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				Set(
					"affinity", corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{{
									MatchExpressions: []corev1.NodeSelectorRequirement{{
										Key:      "topology.kubernetes.io/zone",
										Operator: corev1.NodeSelectorOpIn,
										Values: []string{
											"antarctica-east1",
											"antarctica-west1",
										}},
									}},
								},
							},
						},
					},
				),
		},
		{
			Name: "Adding Node Selectors",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				Set(
					"nodeSelector", map[string]string{
						"test-label": "true",
					},
				),
		},
		{
			Name: "Adding Tolerations",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				Set(
					"tolerations", []corev1.Toleration{
						{
							Key:      "key1",
							Operator: corev1.TolerationOpEqual,
							Value:    "value1",
							Effect:   corev1.TaintEffectNoSchedule,
						},
					},
				),
		},
		{
			Name: "Set systemDefaultRegistry and image",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				Set(
					"image", map[string]string{
						"tag": "enc", "repository": "test/backup-restore-operator",
					},
				).
				SetValue(
					"global.cattle.systemDefaultRegistry", "registry.test.com:8000",
				),
		},
		{
			Name: "Set Kubectl Image and tag",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				SetValue(
					"global.kubectl.tag", "enc",
				).
				SetValue(
					"global.kubectl.repository", "test/kubectl",
				),
		},
		{
			Name: "With proxy",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				SetValue(
					"proxy", "http://test@pass:proxy.com/test:9080",
				),
		},
		{
			Name: "With noProxy",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				SetValue(
					"proxy", "http://test@pass:proxy.com/test:9080",
				).
				SetValue(
					"noProxy", "192.168.0.1",
				),
		},
		{
			Name: "Set ImagePullSecrets",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				Set(
					"imagePullSecrets", []corev1.LocalObjectReference{
						{
							Name: "test-secret",
						},
					},
				),
		},
		{
			Name: "Add serviceAccount annotations",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				Set(
					"serviceAccount.annotations", map[string]string{
						"test": "hull-test",
					},
				),
		},
		{
			Name: "Enable PSPs",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				SetValue(
					"global.cattle.psp.enabled", "true",
				),
		},
		{
			Name: "Disable PSPs",

			TemplateOptions: chart.NewTemplateOptions(DefaultReleaseName, DefaultNamespace).
				SetValue(
					"global.cattle.psp.enabled", "false",
				),
		},
	},

	NamedChecks: []test.NamedCheck{
		{ // Check Container Args
			Name: "Check Container Args",
			Covers: []string{
				".Values.debug",
				".Values.trace",
			},

			Checks: test.Checks{
				checker.PerWorkload(func(tc *checker.TestContext, obj metav1.Object, podTemplateSpec corev1.PodTemplateSpec) {
					if obj.GetNamespace() != checker.MustRenderValue[string](tc, ".Release.Namespace") {
						return
					}
					if obj.GetName() != checker.MustRenderValue[string](tc, ".Release.Name") {
						return
					}
					debug := checker.MustRenderValue[bool](tc, ".Values.debug")
					trace := checker.MustRenderValue[bool](tc, ".Values.trace")
					for _, container := range podTemplateSpec.Spec.Containers {
						if (trace) && (debug) {
							assert.Contains(tc.T, container.Args, "--debug",
								"expected container %s in %T %s to have --debug arg",
								container.Name, obj, checker.Key(obj),
							)
							assert.Contains(tc.T, container.Args, "--trace",
								"expected container %s in %T %s to have --trace arg",
								container.Name, obj, checker.Key(obj),
							)
							assert.Equal(tc.T,
								2, len(container.Args),
								"container %s in %T %s does not have correct args",
								container.Name, obj, checker.Key(obj),
							)
						} else if debug {
							assert.Contains(tc.T, container.Args, "--debug",
								"expected container %s in %T %s to have --debug arg",
								container.Name, obj, checker.Key(obj),
							)
							assert.Equal(tc.T,
								1, len(container.Args),
								"container %s in %T %s does not have correct # of args",
								container.Name, obj, checker.Key(obj),
							)
						} else if trace {
							assert.Contains(tc.T, container.Args, "--trace",
								"expected container %s in %T %s to have --trace arg",
								container.Name, obj, checker.Key(obj),
							)
							assert.Equal(tc.T,
								1, len(container.Args),
								"container %s in %T %s does not have correct # of args",
								container.Name, obj, checker.Key(obj),
							)
						} else if !(trace) && !(debug) {
							assert.Equal(tc.T,
								0, len(container.Args),
								"container %s in %T %s does not have correct args",
								container.Name, obj, checker.Key(obj),
							)
						}
					}
				}),
			},
		},
		{ // Override Image Pull Policy
			Name: "Override Image Pull Policy",
			Covers: []string{
				".Values.imagePullPolicy",
			},

			Checks: test.Checks{
				checker.PerWorkload(func(tc *checker.TestContext, obj metav1.Object, podTemplateSpec corev1.PodTemplateSpec) {
					if checker.Select("rancher-backup-patch-sa", "cattle-resources-system", obj) {
						return
					}
					ipp := checker.MustRenderValue[corev1.PullPolicy](tc, ".Values.imagePullPolicy")
					for _, container := range podTemplateSpec.Spec.Containers {
						assert.Equal(tc.T, container.ImagePullPolicy, ipp,
							"expected container %s in %T %s to have no args",
							container.Name, obj, checker.Key(obj),
						)
					}
				}),
			},
		},
		{ // Override PriorityClassName
			Name: "Set PriorityClassName",
			Covers: []string{
				".Values.priorityClassName",
			},

			Checks: test.Checks{
				checker.PerWorkload(func(tc *checker.TestContext, obj metav1.Object, podTemplateSpec corev1.PodTemplateSpec) {
					if checker.Select("rancher-backup-patch-sa", "cattle-resources-system", obj) {
						return
					}
					pcn := checker.MustRenderValue[string](tc, ".Values.priorityClassName")
					assert.Equal(
						tc.T, podTemplateSpec.Spec.PriorityClassName,
						pcn, "Deployment %s/%s does not have correct PriorityClassName",
						obj.GetNamespace(), obj.GetName(),
					)
				}),
			},
		},
		{ // Set persistence.enabled
			Name: "Set persistence",
			Covers: []string{
				".Values.persistence",
				".Values.persistence.enabled",
				".Values.persistence.size",
				".Values.persistence.storageClass",
				".Values.persistence.volumeName",
			},

			Checks: test.Checks{
				checker.PerWorkload(func(tc *checker.TestContext, obj metav1.Object, podTemplateSpec corev1.PodTemplateSpec) {
					persistenceEnabled := checker.MustRenderValue[bool](tc, ".Values.persistence.enabled")
					if !persistenceEnabled {
						assert.Equal(tc.T, len(podTemplateSpec.Spec.Volumes), 0, "Length of deployment.Spec.Template.Spec.Volumes is not zero when persistence is disabled")

					}
				}),

				checker.PerResource[*corev1.PersistentVolumeClaim](func(tc *checker.TestContext, pvc *corev1.PersistentVolumeClaim) {
					persistenceEnabled := checker.MustRenderValue[bool](tc, ".Values.persistence.enabled")
					pvName := checker.MustRenderValue[string](tc, ".Values.persistence.volumeName")
					size := checker.MustRenderValue[resource.Quantity](tc, ".Values.persistence.size")
					storageClass := checker.MustRenderValue[string](tc, ".Values.persistence.storageClass")
					if !persistenceEnabled {
						assert.Equal(tc.T, pvName, pvc.ObjectMeta.Name, "PersistentVolumeClaim %s/%s does not have correct PersistentVolumeClaim Name in Metadata", pvc.ObjectMeta.Namespace, pvc.ObjectMeta.Name)
						assert.Equal(tc.T, size, *pvc.Spec.Resources.Requests.Storage(), "PersistentVolumeClaim %s/%s does not have correct Storage Size ", pvc.ObjectMeta.Namespace, pvc.ObjectMeta.Name)
						assert.Equal(tc.T, pvName, pvc.Spec.VolumeName, "PersistentVolumeClaim %s/%s does not have correct PersistentVolumeClaim Name in volumeName field", pvc.ObjectMeta.Namespace, pvc.ObjectMeta.Name)
						assert.Equal(tc.T, storageClass, *pvc.Spec.StorageClassName, "PersistentVolumeClaim %s/%s does not have correct PersistentVolumeClaim Name in volumeName field", pvc.ObjectMeta.Namespace, pvc.ObjectMeta.Name)
					}
				}),
			},
		},
		{ // Set s3.enabled
			Name: "Set s3",

			Covers: []string{
				".Values.s3",
				".Values.s3.enabled",
				".Values.s3.bucketName",
				".Values.s3.credentialSecretName",
				".Values.s3.credentialSecretNamespace",
				".Values.s3.endpoint",
				".Values.s3.endpointCA",
				".Values.s3.folder",
				".Values.s3.insecureTLSSkipVerify",
				".Values.s3.region",
			},

			Checks: test.Checks{
				checker.PerWorkload(func(tc *checker.TestContext, obj metav1.Object, podTemplateSpec corev1.PodTemplateSpec) {
					envVar := []corev1.EnvVar([]corev1.EnvVar{corev1.EnvVar{Name: "CHART_NAMESPACE", Value: "cattle-resources-system", ValueFrom: (*corev1.EnvVarSource)(nil)}, corev1.EnvVar{Name: "DEFAULT_S3_BACKUP_STORAGE_LOCATION", Value: "rancher-backup-s3", ValueFrom: (*corev1.EnvVarSource)(nil)}})
					s3Enabled := checker.MustRenderValue[bool](tc, ".Values.s3.enabled")
					if s3Enabled {
						for _, container := range podTemplateSpec.Spec.Containers {
							if !strings.Contains(container.Name, "patch-sa") {
								assert.Equal(tc.T, envVar, container.Env, "container %s in Deployment %s/%s does not have correct S3 image env variable", container.Name, obj.GetNamespace(), obj.GetName())
							}
						}
					}
				}),

				checker.PerResource[*corev1.Secret](func(tc *checker.TestContext, secret *corev1.Secret) {
					s3Enabled := checker.MustRenderValue[bool](tc, ".Values.s3.enabled")
					bucketName := checker.MustRenderValue[string](tc, ".Values.s3.bucketName")
					credentialSecretName := checker.MustRenderValue[string](tc, ".Values.s3.credentialSecretName")
					credentialSecretNamespace := checker.MustRenderValue[string](tc, ".Values.s3.credentialSecretNamespace")
					endpoint := checker.MustRenderValue[string](tc, ".Values.s3.endpoint")
					endpointCA := checker.MustRenderValue[string](tc, ".Values.s3.endpointCA")
					folder := checker.MustRenderValue[string](tc, ".Values.s3.folder")
					insecureTLSSkipVerify := strconv.FormatBool(checker.MustRenderValue[bool](tc, ".Values.s3.insecureTLSSkipVerify"))
					region := checker.MustRenderValue[string](tc, ".Values.s3.region")
					if s3Enabled {
						assert.Equal(tc.T, bucketName, secret.StringData["bucketName"], "S3 Secret Improperly configured (bucketName)")
						assert.Equal(tc.T, credentialSecretNamespace, secret.StringData["credentialSecretNamespace"], "S3 Secret Improperly configured (credentialSecretNamespace)")
						assert.Equal(tc.T, credentialSecretName, secret.StringData["credentialSecretName"], "S3 Secret Improperly configured (credentialSecretNamespace)")
						assert.Equal(tc.T, endpoint, secret.StringData["endpoint"], "S3 Secret Improperly configured (endpoint)")
						assert.Equal(tc.T, endpointCA, secret.StringData["endpointCA"], "S3 Secret Improperly configured (endpointCA)")
						assert.Equal(tc.T, folder, secret.StringData["folder"], "S3 Secret Improperly configured (folder)")
						assert.Equal(tc.T, insecureTLSSkipVerify, secret.StringData["insecureTLSSkipVerify"], "S3 Secret Improperly configured (insecureTLSSkipVerify)")
						assert.Equal(tc.T, region, secret.StringData["region"], "S3 Secret Improperly configured (region)")
					}
				}),
			},
		},
		{ // Adding Node Affinity
			Name: "Adding Node Affinity",

			Covers: []string{
				".Values.affinity",
			},

			Checks: test.Checks{
				checker.PerWorkload(func(tc *checker.TestContext, obj metav1.Object, podTemplateSpec corev1.PodTemplateSpec) {
					if checker.Select("rancher-backup-patch-sa", "cattle-resources-system", obj) {
						return
					}
					affinity, ok := checker.RenderValue[*corev1.Affinity](tc, ".Values.affinity")
					if ok {
						assert.Equal(tc.T, affinity, podTemplateSpec.Spec.Affinity, "Deployment %s/%s has incorrect affinity configuration", obj.GetNamespace(), obj.GetName())
					}
				}),
			},
		},
		{ // Adding Node Selectors
			Name: "Adding Node Selectors",

			Covers: []string{
				".Values.nodeSelector",
			},

			Checks: test.Checks{
				checker.PerWorkload(func(tc *checker.TestContext, obj metav1.Object, podTemplateSpec corev1.PodTemplateSpec) {
					nodeSelectorValue := checker.MustRenderValue[map[string]string](tc, ".Values.nodeSelector")
					if len(nodeSelectorValue) > 1 {
						assert.Equal(tc.T, nodeSelectorValue, podTemplateSpec.Spec.NodeSelector, "Deployment %s/%s has incorrect NodeSelector configuration", obj.GetNamespace(), obj.GetName())
					}
				}),

				checker.PerResource[*batchv1.Job](func(tc *checker.TestContext, job *batchv1.Job) {
					nodeSelectorValue := checker.MustRenderValue[map[string]string](tc, ".Values.nodeSelector")
					if len(nodeSelectorValue) > 1 {
						assert.Equal(tc.T, nodeSelectorValue, job.Spec.Template.Spec.NodeSelector, "Job %s/%s has incorrect NodeSelector configuration", job.Namespace, job.Name)
					}
				}),
			},
		},
		{ // Adding Tolerations (needs review)
			Name: "Adding Tolerations",

			Covers: []string{
				".Values.tolerations",
			},

			Checks: test.Checks{
				checker.PerWorkload(func(tc *checker.TestContext, obj metav1.Object, podTemplateSpec corev1.PodTemplateSpec) {
					tv, _ := checker.RenderValue[[]corev1.Toleration](tc, ".Values.tolerations")
					if len(tv) > 0 {
						tolerations := []corev1.Toleration{
							{
								Key:      "cattle.io/os",
								Operator: corev1.TolerationOpEqual,
								Value:    "linux",
								Effect:   corev1.TaintEffectNoSchedule,
							},
							{
								Key:      "key1",
								Operator: corev1.TolerationOpEqual,
								Value:    "value1",
								Effect:   corev1.TaintEffectNoSchedule,
							},
						}
						assert.Equal(tc.T, tolerations, podTemplateSpec.Spec.Tolerations, "Deployment %s/%s has incorrect toleration configuration", obj.GetNamespace(), obj.GetName())
					}
				}),

				checker.PerResource[*batchv1.Job](func(tc *checker.TestContext, job *batchv1.Job) {
					tv, _ := checker.RenderValue[[]corev1.Toleration](tc, ".Values.tolerations")
					if len(tv) > 0 {
						tolerations := []corev1.Toleration{
							{
								Key:      "cattle.io/os",
								Operator: corev1.TolerationOpEqual,
								Value:    "linux",
								Effect:   corev1.TaintEffectNoSchedule,
							},
							{
								Key:      "key1",
								Operator: corev1.TolerationOpEqual,
								Value:    "value1",
								Effect:   corev1.TaintEffectNoSchedule,
							},
						}
						assert.Equal(tc.T, tolerations, job.Spec.Template.Spec.Tolerations, "Job %s/%s has incorrect toleration configuration", job.Namespace, job.Name)
					}
				}),
			},
		},
		{ // Override Image tags
			Name: "Override systemDefaultRegistry and image",

			Covers: []string{
				".Values.global.cattle.systemDefaultRegistry",
				".Values.image.repository",
				".Values.image.tag",
			},

			Checks: test.Checks{
				checker.PerWorkload(func(tc *checker.TestContext, obj metav1.Object, podTemplateSpec corev1.PodTemplateSpec) {
					if checker.Select("rancher-backup-patch-sa", "cattle-resources-system", obj) {
						return
					}
					sdr, _ := checker.RenderValue[string](tc, ".Values.global.cattle.systemDefaultRegistry")
					repo, _ := checker.RenderValue[string](tc, ".Values.image.repository")
					tag, _ := checker.RenderValue[string](tc, ".Values.image.tag")
					expected := repo + ":" + tag
					if sdr != "" {
						expected = sdr + "/" + repo + ":" + tag
					}
					for _, container := range podTemplateSpec.Spec.Containers {
						assert.Equal(tc.T, expected, container.Image, "container %s in Deployment %s/%s does not have correct image", container.Name, obj.GetNamespace(), obj.GetName())
					}
				}),
			},
		},
		{ // Override Kubectl Image and tag
			Name: "Override Kubectl Image and tag",

			Covers: []string{
				".Values.global.kubectl.repository",
				".Values.global.kubectl.tag",
				".Values.global.cattle.systemDefaultRegistry",
			},

			Checks: test.Checks{
				checker.PerResource[*batchv1.Job](func(tc *checker.TestContext, job *batchv1.Job) {
					sdr, _ := checker.RenderValue[string](tc, ".Values.global.cattle.systemDefaultRegistry")
					kubectlRepo, _ := checker.RenderValue[string](tc, ".Values.global.kubectl.repository")
					kubectlTag, _ := checker.RenderValue[string](tc, ".Values.global.kubectl.tag")
					expected := kubectlRepo + ":" + kubectlTag
					if sdr != "" {
						expected = sdr + "/" + kubectlRepo + ":" + kubectlTag
					}
					for _, container := range job.Spec.Template.Spec.Containers {
						assert.Equal(tc.T, expected, container.Image, "Job %s/%s has incorrect kubectl image configuration", job.Namespace, job.Name)
					}
				}),
			},
		},
		{ // With proxy
			Name: "With proxy",

			Covers: []string{
				".Values.proxy",
				".Values.noProxy",
			},

			Checks: test.Checks{
				checker.PerWorkload(func(tc *checker.TestContext, obj metav1.Object, podTemplateSpec corev1.PodTemplateSpec) {
					proxy, _ := checker.RenderValue[string](tc, ".Values.proxy")
					noProxy, _ := checker.RenderValue[string](tc, ".Values.noProxy")
					if checker.Select("rancher-backup-patch-sa", "cattle-resources-system", obj) || proxy == "" {
						return
					}
					envVar := []corev1.EnvVar([]corev1.EnvVar{
						corev1.EnvVar{
							Name:      "CHART_NAMESPACE",
							Value:     "cattle-resources-system",
							ValueFrom: (*corev1.EnvVarSource)(nil),
						},
						corev1.EnvVar{
							Name:      "HTTP_PROXY",
							Value:     proxy,
							ValueFrom: (*corev1.EnvVarSource)(nil),
						},
						corev1.EnvVar{
							Name:      "HTTPS_PROXY",
							Value:     proxy,
							ValueFrom: (*corev1.EnvVarSource)(nil),
						},
						corev1.EnvVar{
							Name:      "NO_PROXY",
							Value:     noProxy,
							ValueFrom: (*corev1.EnvVarSource)(nil),
						},
					})
					for _, container := range podTemplateSpec.Spec.Containers {
						assert.Equal(tc.T, envVar, container.Env, "container %s in Deployment %s/%s does not have correct Proxy image env variables", container.Name, obj.GetNamespace(), obj.GetName())
					}
				}),
			},
		},
		{ // Set ImagePullSecrets
			Name: "Set ImagePullSecrets",

			Covers: []string{
				".Values.imagePullSecrets",
			},

			Checks: test.Checks{
				checker.PerWorkload(func(tc *checker.TestContext, obj metav1.Object, podTemplateSpec corev1.PodTemplateSpec) {
					if checker.Select("rancher-backup-patch-sa", "cattle-resources-system", obj) {
						return
					}
					imagePullSecrets, _ := checker.RenderValue[[]corev1.LocalObjectReference](tc, ".Values.imagePullSecrets")
					if len(imagePullSecrets) > 0 {
						assert.Equal(tc.T, imagePullSecrets, podTemplateSpec.Spec.ImagePullSecrets, "ImagePullSecrets in Deployment %s/%s do not have correct configuration", obj.GetNamespace(), obj.GetName())
					}
				}),
			},
		},
		{ //Add serviceAccount annotations
			Name: "Add serviceAccount annotations",

			Covers: []string{
				".Values.serviceAccount.annotations",
			},

			Checks: test.Checks{
				checker.PerResource[*corev1.ServiceAccount](func(tc *checker.TestContext, sa *corev1.ServiceAccount) {
					if checker.Select("rancher-backup-patch-sa", "cattle-resources-system", sa) {
						return
					}
					annotations, _ := checker.RenderValue[map[string]string](tc, ".Values.serviceAccount.annotations")
					if len(annotations) > 0 {
						assert.Equal(tc.T, annotations, sa.ObjectMeta.Annotations, "Job %s/%s has incorrect image configuration", sa.Namespace, sa.Name)
					}
				}),
			},
		},
		{ //Set PSPs
			Name: "Set PSPs",

			Covers: []string{
				".Values.global.cattle.psp.enabled",
			},

			Checks: test.Checks{
				checker.PerResource[*rbacv1.ClusterRole](func(tc *checker.TestContext, cr *rbacv1.ClusterRole) {
					pspsEnabled, _ := checker.RenderValue[bool](tc, ".Values.global.cattle.psp.enabled")
					pspsFound := false
					for _, rule := range cr.Rules {
						for _, resource := range rule.Resources {
							if resource == "podsecuritypolicies" {
								pspsFound = true
							}
						}
					}
					if pspsEnabled {
						assert.True(tc.T, pspsFound, "ClusterRole %s has incorrect PSP configuration", cr.Name)
					} else {
						assert.False(tc.T, pspsFound, "ClusterRole %s has incorrect PSP configuration", cr.Name)
					}
				}),
			},
		},
	},
}
