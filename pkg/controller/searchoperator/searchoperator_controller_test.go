// Copyright (c) 2020 Red Hat, Inc.

package searchoperator

import (
	"context"
	"reflect"
	"testing"

	"github.com/open-cluster-management/search-operator/pkg/apis/search/v1alpha1"
	searchv1alpha1 "github.com/open-cluster-management/search-operator/pkg/apis/search/v1alpha1"
	"github.com/stretchr/testify/assert"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func commonSetup() (*runtime.Scheme, reconcile.Request, *v1alpha1.SearchOperator, *corev1.Secret, *appv1.Deployment) {
	testScheme := scheme.Scheme
	testScheme.AddKnownTypes(searchv1alpha1.SchemeGroupVersion, &searchv1alpha1.SearchOperator{})
	testScheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})

	testSearchOperator := &searchv1alpha1.SearchOperator{
		TypeMeta: metav1.TypeMeta{
			APIVersion: searchv1alpha1.SchemeGroupVersion.String(),
			Kind:       "SearchOperator",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-cluster",
		},
		Spec: searchv1alpha1.SearchOperatorSpec{
			Persistence: false,
		},
	}
	testSecret := newRedisSecret(testSearchOperator)
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-cluster",
			Namespace: "test-cluster",
		},
	}

	emptyDirVolume := v1.VolumeSource{
		EmptyDir: &v1.EmptyDirVolumeSource{},
	}
	deployment := getDeployment(testSearchOperator, emptyDirVolume)
	return testScheme, req, testSearchOperator, testSecret, deployment
}

func Test_searchOperatorNotFound(t *testing.T) {
	testScheme, req, _, _, _ := commonSetup()

	client := fake.NewFakeClientWithScheme(testScheme)
	nilSearchOperator := ReconcileSearchOperator{client, testScheme}

	_, err := nilSearchOperator.Reconcile(req)
	if !assert.Nil(t, err) {
		t.Error("Expected Nil. Got error: ", err.Error())
	}
	instance := &searchv1alpha1.SearchOperator{}
	err = client.Get(context.TODO(), req.NamespacedName, instance)
	if !errors.IsNotFound(err) {
		t.Error("Expected Not Found error. Got ", err.Error())
	}
}

func Test_secretCreatedWithOwnerRef(t *testing.T) {
	testScheme, req, searchOperator, testSecret, _ := commonSetup()

	client := fake.NewFakeClientWithScheme(testScheme, searchOperator)
	nilSearchOperator := ReconcileSearchOperator{client, testScheme}

	_, err := nilSearchOperator.Reconcile(req)
	if !assert.Nil(t, err) {
		t.Error("Expected Nil. Got error: ", err.Error())
	}
	instance := &searchv1alpha1.SearchOperator{}
	err = client.Get(context.TODO(), req.NamespacedName, instance)
	if !assert.Nil(t, err) {
		t.Error("Expected search Operator. Got ", err.Error())
	}

	found := &corev1.Secret{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: testSecret.Name, Namespace: testSecret.Namespace}, found)
	if err != nil {
		t.Error("Expected secret to be created. Got error: ", err.Error())
	}
	if found.Name != testSecret.Name || found.Namespace != testSecret.Namespace || !reflect.DeepEqual(found.GetLabels(), testSecret.GetLabels()) {
		t.Errorf("Found secret = %v/%v/%v, Expected =  %v/%v/%v", found.Name, found.Namespace, found.GetLabels(), testSecret.Name, testSecret.Namespace, testSecret.GetLabels())
	}
	ownerRefArray := found.GetOwnerReferences()
	if ownerRefArray == nil {
		t.Error("Secret does not have ownerReference set")
	} else {
		ownerRef := ownerRefArray[0]
		if ownerRef.APIVersion != searchOperator.APIVersion || ownerRef.Kind != searchOperator.Kind || ownerRef.Name != searchOperator.Name {
			t.Errorf("Secret does not have correct ownerReference set. Owner should be searchOperator. Found %v/%v/%v. Expected %v/%v/%v", ownerRef.APIVersion, ownerRef.Kind, ownerRef.Name, searchOperator.APIVersion, searchOperator.Kind, searchOperator.Name)
		}
	}
}

func Test_secretAlreadyExists(t *testing.T) {
	testScheme, req, searchOperator, testSecret, _ := commonSetup()

	client := fake.NewFakeClientWithScheme(testScheme, searchOperator, testSecret)
	nilSearchOperator := ReconcileSearchOperator{client, testScheme}

	_, err := nilSearchOperator.Reconcile(req)
	if !assert.Nil(t, err) {
		t.Error("Expected Nil. Got error: ", err.Error())
	}
	instance := &searchv1alpha1.SearchOperator{}
	err = client.Get(context.TODO(), req.NamespacedName, instance)
	if !assert.Nil(t, err) {
		t.Error("Expected search Operator. Got ", err.Error())
	}

	found := &corev1.Secret{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: testSecret.Name, Namespace: testSecret.Namespace}, found)
	if err != nil {
		t.Error("Expected secret not found. Got error: ", err.Error())
	}

	if !reflect.DeepEqual(found.GetObjectMeta(), testSecret.GetObjectMeta()) || !reflect.DeepEqual(found.Data, testSecret.Data) {
		t.Errorf("Expected secret not found. Secrets not the same - Check data part. Found secret = %v/%v/%v Data: %v, Expected =  %v/%v/%v Data: %v", found.Name, found.Namespace, found.GetLabels(), found.Data, testSecret.Name, testSecret.Namespace, testSecret.GetLabels(), testSecret.Data)
	}
}

func Test_DeploymentCreatedWithOwnerRef(t *testing.T) {
	testScheme, req, searchOperator, testSecret, testDeployment := commonSetup()

	//TODO: Passing already existing secret doesn't set ownerRef - testSecret
	client := fake.NewFakeClientWithScheme(testScheme, searchOperator)
	nilSearchOperator := ReconcileSearchOperator{client, testScheme}

	_, err := nilSearchOperator.Reconcile(req)

	instance := &searchv1alpha1.SearchOperator{}
	err = client.Get(context.TODO(), req.NamespacedName, instance)
	if !assert.Nil(t, err) {
		t.Error("Expected search Operator. Got ", err.Error())
	}

	found := &corev1.Secret{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: testSecret.Name, Namespace: searchOperator.Namespace}, found)
	if err != nil {
		t.Error("Expected secret not found. Got error: ", err.Error())
	}

	foundDeployment := &appv1.Deployment{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: testDeployment.Name, Namespace: testDeployment.Namespace}, foundDeployment)
	if err != nil {
		t.Error("Expected deployment not found. Got error: ", err.Error())
	}

	if foundDeployment.Name != testDeployment.Name || foundDeployment.Namespace != testDeployment.Namespace || !reflect.DeepEqual(foundDeployment.Spec, testDeployment.Spec) {
		t.Errorf("Expected deployment not found. Deployments not the same. Found deployment = %v/%v/%v Spec: %v, Expected =  %v/%v/%v Spec: %v", foundDeployment.Name, foundDeployment.Namespace, foundDeployment.GetLabels(), foundDeployment.Spec, testDeployment.Name, testDeployment.Namespace, testDeployment.GetLabels(), testDeployment.Spec)
	}

	ownerRefArray := foundDeployment.GetOwnerReferences()
	if ownerRefArray == nil {
		t.Error("Deployment does not have ownerReference set")
	} else {
		ownerRef := ownerRefArray[0]
		if ownerRef.APIVersion != searchOperator.APIVersion || ownerRef.Kind != searchOperator.Kind || ownerRef.Name != searchOperator.Name {
			t.Errorf("Deployment does not have correct ownerReference set. Owner should be searchOperator. Found %v/%v/%v. Expected %v/%v/%v", ownerRef.APIVersion, ownerRef.Kind, ownerRef.Name, searchOperator.APIVersion, searchOperator.Kind, searchOperator.Name)
		}
	}

}
