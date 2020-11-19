// Copyright (c) 2020 Red Hat, Inc.

package controllers

import (
	"context"
	"testing"

	searchv1alpha1 "github.com/open-cluster-management/search-operator/api/v1"
	"github.com/stretchr/testify/assert"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func commonSetup() (*runtime.Scheme, reconcile.Request, *searchv1alpha1.SearchOperator, *corev1.Secret, *appv1.StatefulSet) {
	testScheme := scheme.Scheme

	searchv1alpha1.AddToScheme(testScheme)
	testScheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})
	waitSecondsForPodChk = 5
	testSearchOperator := &searchv1alpha1.SearchOperator{
		TypeMeta: metav1.TypeMeta{
			APIVersion: searchv1alpha1.GroupVersion.String(),
			Kind:       "SearchOperator",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-cluster",
		},
		Spec: searchv1alpha1.SearchOperatorSpec{
			Persistence:      false,
			AllowDegradeMode: true,
			StorageSize:      "1M",
		},
	}
	testSecret := newRedisSecret(testSearchOperator)
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-cluster",
			Namespace: "test-cluster",
		},
	}

	client := fake.NewFakeClientWithScheme(testScheme)
	testStatefulset := executeDeployment(client, testSearchOperator, false, testScheme)
	return testScheme, req, testSearchOperator, testSecret, testStatefulset
}

func Test_searchOperatorNotFound(t *testing.T) {
	testScheme, req, _, _, _ := commonSetup()

	client := fake.NewFakeClientWithScheme(testScheme)
	// log = logf.Log.WithName("searchoperator")

	nilSearchOperator := SearchOperatorReconciler{client, log, testScheme}

	_, err := nilSearchOperator.Reconcile(req)
	assert.Nil(t, err, "Expected Nil. Got error: %v", err)

	instance := &searchv1alpha1.SearchOperator{}
	err = client.Get(context.TODO(), req.NamespacedName, instance)
	assert.True(t, errors.IsNotFound(err), "Expected Not Found error. Got %v", err.Error())
}

func Test_secretCreatedWithOwnerRef(t *testing.T) {
	testScheme, req, searchOperator, testSecret, _ := commonSetup()

	client := fake.NewFakeClientWithScheme(testScheme, searchOperator)
	nilSearchOperator := SearchOperatorReconciler{client, log, testScheme}

	_, err := nilSearchOperator.Reconcile(req)

	found := &corev1.Secret{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: testSecret.Name, Namespace: testSecret.Namespace}, found)
	assert.Nil(t, err, "Expected secret to be created. Got error: %v", err)
	assert.Equal(t, testSecret.Name, found.Name, "Secret is created with expected name.")
	assert.Equal(t, testSecret.Namespace, found.Namespace, "Secret is created in expected namespace.")
	assert.EqualValues(t, testSecret.GetLabels(), found.GetLabels(), "Secret is created with expected labels.")
	ownerRefArray := found.GetOwnerReferences()
	assert.NotNil(t, ownerRefArray, "Created secret should have an ownerReference.")
	assert.Len(t, ownerRefArray, 1, "Created secret should have an ownerReference.")

	ownerRef := ownerRefArray[0]
	assert.Equal(t, searchOperator.APIVersion, ownerRef.APIVersion, "ownerRef has expected APIVersion.")
	assert.Equal(t, searchOperator.Kind, ownerRef.Kind, "ownerRef has expected Kind.")
	assert.Equal(t, searchOperator.Name, ownerRef.Name, "ownerRef has expected Name.")

}

func Test_secretAlreadyExists(t *testing.T) {
	testScheme, req, searchOperator, testSecret, _ := commonSetup()

	client := fake.NewFakeClientWithScheme(testScheme, searchOperator, testSecret)
	nilSearchOperator := SearchOperatorReconciler{client, log, testScheme}

	_, err := nilSearchOperator.Reconcile(req)

	found := &corev1.Secret{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: testSecret.Name, Namespace: testSecret.Namespace}, found)

	assert.Nil(t, err, "Expected secret to be created. Got error: %v", err)
	assert.EqualValues(t, testSecret.GetObjectMeta(), found.GetObjectMeta(), "Secret is created with expected labels.")
	assert.EqualValues(t, testSecret.Data, found.Data, "Secret is created with expected data.")

}

func Test_EmptyDirStatefulsetCreatedWithOwnerRef(t *testing.T) {
	testScheme, req, searchOperator, testSecret, testStatefulset := commonSetup()

	client := fake.NewFakeClientWithScheme(testScheme, searchOperator, testSecret)
	nilSearchOperator := SearchOperatorReconciler{client, log, testScheme}
	var err error

	_, err = nilSearchOperator.Reconcile(req)

	foundStatefulset := &appv1.StatefulSet{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: testStatefulset.Name, Namespace: testStatefulset.Namespace}, foundStatefulset)
	assert.Nil(t, err, "Expected statefulset to be created. Got error: %v", err)

	assert.Equal(t, testStatefulset.Name, foundStatefulset.Name, "Statefulset is created with expected name.")
	assert.Equal(t, testStatefulset.Namespace, foundStatefulset.Namespace, "Statefulset is created in expected namespace.")

	ownerRefArray := foundStatefulset.GetOwnerReferences()

	assert.NotNil(t, ownerRefArray, "Created Statefulset should have an ownerReference.")
	assert.Len(t, ownerRefArray, 1, "Created Statefulset should have an ownerReference.")

	ownerRef := ownerRefArray[0]
	assert.Equal(t, searchOperator.APIVersion, ownerRef.APIVersion, "ownerRef has expected APIVersion.")
	assert.Equal(t, searchOperator.Kind, ownerRef.Kind, "ownerRef has expected Kind.")
	assert.Equal(t, searchOperator.Name, ownerRef.Name, "ownerRef has expected Name.")
}

func Test_StatefulsetDegradedSuccess(t *testing.T) {
	testScheme, req, searchOperator, testSecret, testStatefulset := commonSetup()

	//TODO: Passing already existing secret doesn't set ownerRef - testSecret
	client := fake.NewFakeClientWithScheme(testScheme, searchOperator, testSecret)
	nilSearchOperator := SearchOperatorReconciler{client, log, testScheme}
	var err error

	instance := &searchv1alpha1.SearchOperator{}
	err = client.Get(context.TODO(), req.NamespacedName, instance)
	assert.Nil(t, err, "Expected search Operator to be created. Got error: %v", err)

	//Set persistence to true in operator - this should cause statefulset to fall back to empty dir since we don't have PVC
	instance.Spec.Persistence = true
	err = client.Update(context.TODO(), instance)
	err = client.Get(context.TODO(), req.NamespacedName, instance)

	_, err = nilSearchOperator.Reconcile(req)

	err = client.Get(context.TODO(), req.NamespacedName, instance)
	assert.Nil(t, err, "Expected search Operator to be created. Got error: %v", err)

	foundStatefulset := &appv1.StatefulSet{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: testStatefulset.Name, Namespace: testStatefulset.Namespace}, foundStatefulset)

	assert.Nil(t, err, "Expected Statefulset to be created. Got error: %v", err)

	assert.Equal(t, testStatefulset.Name, foundStatefulset.Name, "Statefulset is created with expected name.")
	assert.Equal(t, testStatefulset.Namespace, foundStatefulset.Namespace, "Statefulset is created in expected namespace.")
	assert.EqualValues(t, testStatefulset.Spec.Template.Spec, foundStatefulset.Spec.Template.Spec, "Statefulset is created with expected template spec.")

}
