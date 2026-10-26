package k8smatch

import (
	"context"
	"fmt"
	"reflect"

	"github.com/onsi/gomega"
	gtypes "github.com/onsi/gomega/types"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var ErrUnsupportedObjectType = fmt.Errorf("unsupported object type")

var defaultObjectClient client.Client

func SetDefaultObjectClient(c client.Client) {
	defaultObjectClient = c
}

func defaultClientOr(optionalClient ...client.Client) client.Client {
	if len(optionalClient) > 0 {
		return optionalClient[0]
	}
	if defaultObjectClient == nil {
		panic("default client is not set - use SetDefaultObjectClient to set a default client")
	}
	return defaultObjectClient
}

func Object[T client.Object](obj T, optionalClient ...client.Client) func() (T, error) {
	c := defaultClientOr(optionalClient...)
	key := client.ObjectKeyFromObject(obj)
	typ := reflect.TypeOf(obj).Elem()
	return func() (T, error) {
		t := reflect.New(typ).Interface().(T)
		err := c.Get(context.Background(), key, t)
		if apierrors.IsNotFound(err) {
			err = nil
		}
		return t, err
	}
}

func GVK(gvk schema.GroupVersionKind, optionalClient ...client.Client) func() (*meta.RESTMapping, error) {
	c := defaultClientOr(optionalClient...)
	mapper := c.RESTMapper()
	return func() (*meta.RESTMapping, error) {
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if meta.IsNoMatchError(err) {
			return mapping, nil
		}
		return mapping, err
	}
}

type existenceMatcher struct{}

func (existenceMatcher) Match(target interface{}) (bool, error) {
	if target == nil {
		return false, nil
	}
	if obj, ok := target.(client.Object); ok {
		return obj.GetCreationTimestamp() != metav1.Time{} && obj.GetDeletionTimestamp() == nil, nil
	}
	if mapping, ok := target.(*meta.RESTMapping); ok {
		return mapping != nil &&
			mapping.GroupVersionKind.Group != "" &&
			mapping.GroupVersionKind.Version != "" &&
			mapping.GroupVersionKind.Kind != "", nil
	}
	return false, fmt.Errorf("%w: expected client.Object or *meta.RESTMapping, got %T", ErrUnsupportedObjectType, target)
}

func (existenceMatcher) FailureMessage(target interface{}) string {
	return fmt.Sprintf("expected %v to exist", target)
}

func (existenceMatcher) NegatedFailureMessage(target interface{}) string {
	return fmt.Sprintf("expected %v not to exist", target)
}

func Exist() gtypes.GomegaMatcher {
	return existenceMatcher{}
}

func ExistAnd(matchers ...gtypes.GomegaMatcher) gtypes.GomegaMatcher {
	return gomega.And(append([]gtypes.GomegaMatcher{Exist()}, matchers...)...)
}

func objectName(target interface{}) string {
	if obj, ok := target.(client.Object); ok {
		return obj.GetName()
	}
	return fmt.Sprintf("%v", target)
}

type nameMatcher struct{ name string }

func (m nameMatcher) Match(target interface{}) (bool, error) {
	if obj, ok := target.(client.Object); ok {
		return obj.GetName() == m.name, nil
	}
	val := reflect.ValueOf(target)
	nameField := val.FieldByName("Name")
	if nameField.IsValid() {
		var name string
		if nameField.Kind() == reflect.Pointer {
			name = nameField.Elem().String()
		} else {
			name = nameField.String()
		}
		return name == m.name, nil
	}
	objectMetaField := val.FieldByName("ObjectMeta")
	if objectMetaField.IsValid() {
		nameField := objectMetaField.FieldByName("Name")
		if nameField.IsValid() {
			return nameField.String() == m.name, nil
		}
	}
	return false, fmt.Errorf(
		"%w %T in NameMatcher (allowed types: client.Object)",
		ErrUnsupportedObjectType, target)
}

func (m nameMatcher) FailureMessage(target interface{}) string {
	return "expected " + objectName(target) + " to have name " + m.name
}

func (m nameMatcher) NegatedFailureMessage(target interface{}) string {
	return "expected " + objectName(target) + " not to have name " + m.name
}

func HaveName(name string) gtypes.GomegaMatcher {
	return nameMatcher{name: name}
}

type imageMatcher struct {
	image      string
	pullPolicy *corev1.PullPolicy
}

func (m imageMatcher) Match(target interface{}) (bool, error) {
	container, ok := target.(corev1.Container)
	if !ok {
		return false, fmt.Errorf("%w %T in HaveImage (allowed types: corev1.Container)", ErrUnsupportedObjectType, target)
	}
	match := container.Image == m.image
	if m.pullPolicy != nil {
		match = match && *m.pullPolicy == container.ImagePullPolicy
	}
	return match, nil
}

func (m imageMatcher) FailureMessage(_ interface{}) string {
	return fmt.Sprintf("expected container to have image %s", m.image)
}

func (m imageMatcher) NegatedFailureMessage(_ interface{}) string {
	return fmt.Sprintf("expected container not to have image %s", m.image)
}

func HaveImage(image string, maybeImagePullPolicy ...corev1.PullPolicy) gtypes.GomegaMatcher {
	m := imageMatcher{image: image}
	if len(maybeImagePullPolicy) > 0 {
		m.pullPolicy = &maybeImagePullPolicy[0]
	}
	return m
}

func isSubset(want, have map[string]string) bool {
	for k, v := range want {
		if v2, ok := have[k]; !ok || v != v2 {
			return false
		}
	}
	return true
}

type labelMatcher struct{ labels map[string]string }

func (m labelMatcher) Match(target interface{}) (bool, error) {
	obj, ok := target.(metav1.Object)
	if !ok {
		return false, fmt.Errorf("%w %T in HaveLabels (allowed types: any metav1.Object)", ErrUnsupportedObjectType, target)
	}
	return isSubset(m.labels, obj.GetLabels()), nil
}

func (m labelMatcher) FailureMessage(target interface{}) string {
	return fmt.Sprintf("expected %s to have labels %v", objectName(target), m.labels)
}

func (m labelMatcher) NegatedFailureMessage(target interface{}) string {
	return fmt.Sprintf("expected %s not to have labels %v", objectName(target), m.labels)
}

func HaveLabels(keysAndValues ...string) gtypes.GomegaMatcher {
	m := labelMatcher{labels: make(map[string]string)}
	for i := 0; i < len(keysAndValues); i += 2 {
		m.labels[keysAndValues[i]] = keysAndValues[i+1]
	}
	return m
}

type annotationMatcher struct{ annotations map[string]string }

func (m annotationMatcher) Match(target interface{}) (bool, error) {
	obj, ok := target.(metav1.Object)
	if !ok {
		return false, fmt.Errorf("%w %T in HaveAnnotations (allowed types: any metav1.Object)", ErrUnsupportedObjectType, target)
	}
	return isSubset(m.annotations, obj.GetAnnotations()), nil
}

func (m annotationMatcher) FailureMessage(target interface{}) string {
	return fmt.Sprintf("expected %s to have annotations %v", objectName(target), m.annotations)
}

func (m annotationMatcher) NegatedFailureMessage(target interface{}) string {
	return fmt.Sprintf("expected %s not to have annotations %v", objectName(target), m.annotations)
}

func HaveAnnotations(keysAndValues ...string) gtypes.GomegaMatcher {
	m := annotationMatcher{annotations: make(map[string]string)}
	for i := 0; i < len(keysAndValues); i += 2 {
		m.annotations[keysAndValues[i]] = keysAndValues[i+1]
	}
	return m
}

type dataMatcher struct{ keysAndValues []interface{} }

func (m dataMatcher) Match(target interface{}) (bool, error) {
	data := map[string]string{}
	switch t := target.(type) {
	case *corev1.Secret:
		for k, v := range t.Data {
			data[k] = string(v)
		}
	case *corev1.ConfigMap:
		data = t.Data
	default:
		return false, fmt.Errorf("%w %T in HaveData (allowed types: *corev1.Secret, *corev1.ConfigMap)", ErrUnsupportedObjectType, target)
	}
	for i := 0; i < len(m.keysAndValues); i += 2 {
		key := fmt.Sprint(m.keysAndValues[i])
		value := m.keysAndValues[i+1]
		got, ok := data[key]
		if !ok {
			return false, nil
		}
		if value == nil {
			continue
		}
		if got != fmt.Sprint(value) {
			return false, nil
		}
	}
	return true, nil
}

func (m dataMatcher) FailureMessage(target interface{}) string {
	return fmt.Sprintf("expected %s to contain key-value pairs %v", objectName(target), m.keysAndValues)
}

func (m dataMatcher) NegatedFailureMessage(target interface{}) string {
	return fmt.Sprintf("expected %s not to contain key-value pairs %v", objectName(target), m.keysAndValues)
}

func HaveData(keysAndValues ...interface{}) gtypes.GomegaMatcher {
	if len(keysAndValues)%2 != 0 {
		panic("HaveData requires an even number of arguments")
	}
	return dataMatcher{keysAndValues: keysAndValues}
}

type finalizerMatcher struct {
	finalizers []string
}

func (m finalizerMatcher) Match(target interface{}) (bool, error) {
	obj, ok := target.(client.Object)
	if !ok {
		return false, fmt.Errorf("%w %T in HaveFinalizers (allowed types: client.Object)", ErrUnsupportedObjectType, target)
	}
	return gomega.ContainElements(m.finalizers).Match(obj.GetFinalizers())
}

func (m finalizerMatcher) FailureMessage(target interface{}) string {
	return fmt.Sprintf("expected %s to have finalizers %v", objectName(target), m.finalizers)
}

func (m finalizerMatcher) NegatedFailureMessage(target interface{}) string {
	return fmt.Sprintf("expected %s not to have finalizers %v", objectName(target), m.finalizers)
}

func HaveFinalizers(finalizers ...string) gtypes.GomegaMatcher {
	return finalizerMatcher{finalizers: finalizers}
}

type containerMatcher struct{ matcher gtypes.GomegaMatcher }

func matchAnyContainer(matcher gtypes.GomegaMatcher, containers []corev1.Container) (bool, error) {
	var firstErr error
	for _, c := range containers {
		ok, err := matcher.Match(c)
		if ok {
			return true, nil
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return false, firstErr
}

func (m containerMatcher) Match(target interface{}) (bool, error) {
	switch t := target.(type) {
	case *appsv1.Deployment:
		return matchAnyContainer(m.matcher, t.Spec.Template.Spec.Containers)
	case *appsv1.StatefulSet:
		return matchAnyContainer(m.matcher, t.Spec.Template.Spec.Containers)
	case *appsv1.DaemonSet:
		return matchAnyContainer(m.matcher, t.Spec.Template.Spec.Containers)
	case *corev1.Pod:
		return matchAnyContainer(m.matcher, t.Spec.Containers)
	default:
		return false, fmt.Errorf(
			"%w %T in HaveMatchingContainer (allowed types: *appsv1.Deployment, *appsv1.StatefulSet, *appsv1.DaemonSet, *corev1.Pod)",
			ErrUnsupportedObjectType, target)
	}
}

func (m containerMatcher) FailureMessage(target interface{}) string {
	return "expected " + objectName(target) + " to have a matching container"
}

func (m containerMatcher) NegatedFailureMessage(target interface{}) string {
	return "expected " + objectName(target) + " not to have a matching container"
}

func HaveMatchingContainer(matcher gtypes.GomegaMatcher) gtypes.GomegaMatcher {
	return containerMatcher{matcher: matcher}
}

func matchDeployRollout(d *appsv1.Deployment) (success bool, reason string) {
	if d.Generation > d.Status.ObservedGeneration {
		return false, "waiting for deployment spec update to be observed"
	}
	if ptr.Deref(d.Spec.Replicas, 1) < d.Status.UpdatedReplicas {
		return false, fmt.Sprintf(
			"waiting for deployment %q rollout to finish: %d out of %d new replicas have been updated",
			d.Name, d.Status.UpdatedReplicas, *d.Spec.Replicas)
	}
	if d.Spec.Replicas != nil && *d.Spec.Replicas > d.Status.Replicas {
		return false, fmt.Sprintf(
			"waiting for deployment %q rollout to finish: %d old replicas are pending termination",
			d.Name, d.Status.Replicas-d.Status.UpdatedReplicas)
	}
	if d.Status.AvailableReplicas < d.Status.UpdatedReplicas {
		return false, fmt.Sprintf(
			"waiting for deployment %q rollout to finish: %d of %d updated replicas are available",
			d.Name, d.Status.AvailableReplicas, d.Status.UpdatedReplicas)
	}
	return true, fmt.Sprintf("deployment %q successfully rolled out", d.Name)
}

type rolloutMatcher struct{}

func (rolloutMatcher) Match(target interface{}) (bool, error) {
	d, ok := target.(*appsv1.Deployment)
	if !ok {
		return false, fmt.Errorf("%w %T in HaveSuccessfulRollout (allowed types: *appsv1.Deployment)", ErrUnsupportedObjectType, target)
	}
	success, _ := matchDeployRollout(d)
	return success, nil
}

func (rolloutMatcher) FailureMessage(target interface{}) string {
	d := target.(*appsv1.Deployment)
	_, reason := matchDeployRollout(d)
	return "expected " + objectName(target) + " to have a successful rollout: " + reason
}

func (rolloutMatcher) NegatedFailureMessage(target interface{}) string {
	d := target.(*appsv1.Deployment)
	_, reason := matchDeployRollout(d)
	return "expected " + objectName(target) + " not to have a successful rollout: " + reason
}

func HaveSuccessfulRollout() gtypes.GomegaMatcher {
	return rolloutMatcher{}
}
