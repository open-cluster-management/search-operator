// Copyright (c) 2020 Red Hat, Inc.

package controllers

import (
	"testing"

	searchv1alpha1 "github.com/open-cluster-management/search-operator/api/v1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func Test_searchCustReconcile(t *testing.T) {
	testScheme := scheme.Scheme
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "searchcustomization",
			Namespace: namespace,
		},
	}
	namespace = "test-cluster"
	searchv1alpha1.AddToScheme(testScheme)
	testScheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})
	client := fake.NewFakeClientWithScheme(testScheme)
	testSearchCustomizationReconciler := SearchCustomizationReconciler{client, log, testScheme}
	_, err := testSearchCustomizationReconciler.Reconcile(req)
	assert.Nil(t, err, "Expected no error. Got error: %v", err)
}

func Test_setUpWithMgr(t *testing.T) {
	testScheme := scheme.Scheme
	namespace = "test-cluster"
	searchv1alpha1.AddToScheme(testScheme)
	testScheme.AddKnownTypes(corev1.SchemeGroupVersion, &corev1.Secret{})
	client := fake.NewFakeClientWithScheme(testScheme)
	testSearchCustomizationReconciler := SearchCustomizationReconciler{client, log, testScheme}
	var options ctrl.Options
	options.Scheme = testScheme
	cfg, err := config.GetConfig()
	mgr, err := manager.New(cfg, manager.Options{})
	err = testSearchCustomizationReconciler.SetupWithManager(mgr)
	assert.Nil(t, err, "Expected no error. Got error: %v", err)

}
