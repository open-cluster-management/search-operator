// Copyright (c) 2020 Red Hat, Inc.

package searchservice

import (
	"context"
	"reflect"
	"testing"

	errors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/open-cluster-management/search-operator/pkg/apis/search/v1alpha1"
	searchv1alpha1 "github.com/open-cluster-management/search-operator/pkg/apis/search/v1alpha1"
	assert "github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func commonSetup() (*runtime.Scheme, reconcile.Request, *v1alpha1.SearchService, *corev1.Secret) {
	testScheme := scheme.Scheme
	testScheme.AddKnownTypes(searchv1alpha1.SchemeGroupVersion, &searchv1alpha1.SearchService{})

	testSearchService := &searchv1alpha1.SearchService{
		TypeMeta: metav1.TypeMeta{
			APIVersion: searchv1alpha1.SchemeGroupVersion.String(),
			Kind:       "SearchService",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-cluster",
		},
		Spec: searchv1alpha1.SearchServiceSpec{},
	}
	testSecret := newRedisSecret(testSearchService)
	// testSecret1 := &corev1.Secret{
	// 	TypeMeta: metav1.TypeMeta{
	// 		APIVersion: corev1.SchemeGroupVersion.String(),
	// 		Kind:       "Secret",
	// 	},
	// 	ObjectMeta: metav1.ObjectMeta{
	// 		Name:      "redisgraph-user-secret",
	// 		Namespace: "test-cluster",
	// 		Labels: map[string]string{
	// 			"app": "search",
	// 		},
	// 	},
	// 	Data: map[string][]byte{
	// 		"redispwd": generatePass(16),
	// 	},
	// }
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-cluster",
			Namespace: "test-cluster",
		},
	}
	return testScheme, req, testSearchService, testSecret
}

func Test_searchServiceNotFound(t *testing.T) {
	testScheme, req, _, _ := commonSetup()

	client := fake.NewFakeClientWithScheme(testScheme)
	nilSearchService := ReconcileSearchService{client, testScheme}

	_, err := nilSearchService.Reconcile(req)
	if !assert.Nil(t, err) {
		t.Error("Expected Nil. Got error: ", err.Error())
	}
	instance := &searchv1alpha1.SearchService{}
	err = client.Get(context.TODO(), req.NamespacedName, instance)
	if !errors.IsNotFound(err) {
		t.Error("Expected Not Found error. Got ", err.Error())
	}
}

func Test_secretNotFound(t *testing.T) {
	testScheme, req, searchService, testSecret := commonSetup()

	client := fake.NewFakeClientWithScheme(testScheme, searchService)
	nilSearchService := ReconcileSearchService{client, testScheme}

	_, err := nilSearchService.Reconcile(req)
	if !assert.Nil(t, err) {
		t.Error("Expected Nil. Got error: ", err.Error())
	}
	instance := &searchv1alpha1.SearchService{}
	err = client.Get(context.TODO(), req.NamespacedName, instance)
	if !assert.Nil(t, err) {
		t.Error("Expected search Service. Got ", err.Error())
	}

	found := &corev1.Secret{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: testSecret.Name, Namespace: testSecret.Namespace}, found)
	if err != nil {
		t.Error("Expected secret to be created. Got error: ", err.Error())
	}
	if found.Name != testSecret.Name || found.Namespace != testSecret.Namespace || !reflect.DeepEqual(found.GetLabels(), testSecret.GetLabels()) {
		t.Errorf("Found secret = %v/%v/%v, Expected =  %v/%v/%v", found.Name, found.Namespace, found.GetLabels(), testSecret.Name, testSecret.Namespace, testSecret.GetLabels())
	}

}

func Test_secretAlreadyExists(t *testing.T) {
	testScheme, req, searchService, testSecret := commonSetup()

	client := fake.NewFakeClientWithScheme(testScheme, searchService, testSecret)
	nilSearchService := ReconcileSearchService{client, testScheme}

	_, err := nilSearchService.Reconcile(req)
	if !assert.Nil(t, err) {
		t.Error("Expected Nil. Got error: ", err.Error())
	}
	instance := &searchv1alpha1.SearchService{}
	err = client.Get(context.TODO(), req.NamespacedName, instance)
	if !assert.Nil(t, err) {
		t.Error("Expected search Service. Got ", err.Error())
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
