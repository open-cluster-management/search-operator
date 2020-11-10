// Copyright (c) 2020 Red Hat, Inc.

package searchservice

import (
	"context"
	"reflect"
	"testing"
	"time"

	searchv1alpha1 "github.com/open-cluster-management/search-operator/pkg/apis/search/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestReconcileSearchService_Reconcile(t *testing.T) {
	testscheme := scheme.Scheme
	testscheme.AddKnownTypes(searchv1alpha1.SchemeGroupVersion, &searchv1alpha1.SearchService{})

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

	type args struct {
		request reconcile.Request
	}
	type fields struct {
		client client.Client
		scheme *runtime.Scheme
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    reconcile.Result
		wantErr bool
	}{
		{
			name: "search service not found",
			fields: fields{
				client: fake.NewFakeClientWithScheme(testscheme),
				scheme: testscheme,
			},
			args: args{
				request: req,
			},
			want: reconcile.Result{
				Requeue:      false,
				RequeueAfter: 0 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "secret not found", //check if secret got created
			fields: fields{
				client: fake.NewFakeClientWithScheme(testscheme, testSearchService),
				scheme: testscheme,
			},
			args: args{
				request: req,
			},
			want: reconcile.Result{
				Requeue:      false,
				RequeueAfter: 0 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "secret already exists", //check if existing secret is same as testSecret
			fields: fields{
				client: fake.NewFakeClientWithScheme(testscheme, testSearchService, testSecret),
				scheme: testscheme,
			},
			args: args{
				request: req,
			},
			want: reconcile.Result{
				Requeue:      false,
				RequeueAfter: 0 * time.Second,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &ReconcileSearchService{
				client: tt.fields.client,
				scheme: tt.fields.scheme,
			}

			got, err := r.Reconcile(tt.args.request)

			if (err != (error)(nil)) != tt.wantErr {
				t.Errorf("ReconcileSearchService.Reconcile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.name == "secret not found" || tt.name == "secret already exists" {
				found := &corev1.Secret{}
				err = r.client.Get(context.TODO(), types.NamespacedName{Name: testSecret.Name, Namespace: testSecret.Namespace}, found)

				if found.Name != testSecret.Name || found.Namespace != testSecret.Namespace || !reflect.DeepEqual(found.GetLabels(), testSecret.GetLabels()) {
					t.Errorf("Secrets not the same = %v/%v/%v, want %v/%v/%v", found.Name, found.Namespace, found.GetLabels(), testSecret.Name, testSecret.Namespace, testSecret.GetLabels())
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ReconcileSearchService.Reconcile() = %v, want %v", got, tt.want)
			}
		})
	}
}
