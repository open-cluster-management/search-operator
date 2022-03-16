// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package controllers

import (
	"crypto/rand"
	"math/big"

	searchv1alpha1 "github.com/stolostron/search-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
)

func generatePass(length int) []byte {
	chars := "ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
		"abcdefghijklmnopqrstuvwxyz" +
		"0123456789"

	buf := make([]byte, length)
	for i := 0; i < length; i++ {
		nBig, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		buf[i] = chars[nBig.Int64()]
	}
	return buf
}

// newRedisSecret returns a redisgraph-user-secret with the same name/namespace as the cr
func newRedisSecret(cr *searchv1alpha1.SearchOperator, scheme *runtime.Scheme) *corev1.Secret {
	labels := map[string]string{
		"app": "search",
	}

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "redisgraph-user-secret",
			Namespace: namespace,
			Labels:    labels,
		},
		Data: map[string][]byte{
			"redispwd": generatePass(16),
		},
	}
	if err := ctrl.SetControllerReference(cr, sec, scheme); err != nil {
		log.Info("Cannot set secret OwnerReference", err.Error())
	}
	return sec
}
