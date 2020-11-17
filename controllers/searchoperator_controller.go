/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	searchv1alpha1 "github.com/open-cluster-management/search-operator/api/v1"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// SearchOperatorReconciler reconciles a SearchOperator object
type SearchOperatorReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

const pvcName = "redisgraph-pvc"

var (
	log = logf.Log.WithName("searchoperator")
)

// +kubebuilder:rbac:groups=search.open-cluster-management.io,resources=searchoperators,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=search.open-cluster-management.io,resources=searchoperators/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=search.open-cluster-management.io,resources=searchoperators,verbs=get;list;watch;create;update;patch;delete

func (r *SearchOperatorReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	_ = r.Log.WithValues("searchoperator", req.NamespacedName)

	// your logic here
	// Fetch the SearchOperator instance
	instance := &searchv1alpha1.SearchOperator{}

	err := r.Client.Get(context.TODO(), req.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	// Define a new Secret object
	secret := newRedisSecret(instance)

	// Set SearchService instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, secret, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	// Check if this Secret already exists
	found := &corev1.Secret{}
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		r.Log.Info("Creating a new Secret", "Secret.Namespace", secret.Namespace, "Secret.Name", secret.Name)
		err = r.Client.Create(context.TODO(), secret)
		if err != nil {
			return ctrl.Result{}, err
		}
		// Secret created successfully - don't requeue
		//return reconcile.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	} else {
		// Secret already exists - don't requeue
		r.Log.Info("Skip reconcile: Secret already exists", "Secret.Namespace", found.Namespace, "Secret.Name", found.Name)
	}

	// Setup RedisGraph Deployment
	r.Log.Info(fmt.Sprintf("Config in Use Persistence/Degrade %v/%v", instance.Spec.Persistence, instance.Spec.Degraded))
	if instance.Spec.Persistence && !instance.Spec.Degraded {
		if podScheduled(r.Client, instance, 1) && instance.Status.PersistenceStatus == "Persistence using PersistenceVolumeClaim" {
			return ctrl.Result{}, nil
		}
		setupVolume(r.Client, instance)

		executeDeployment(r.Client, instance, true, r.Scheme)

		//If Pod cannot be scheduled rollback to EmptyDir
		if !podScheduled(r.Client, instance, 180) {
			r.Log.Info("Degrading to  Empty dir Deployment")
			//Write Status
			updateCR(r.Client, instance, "Degraded mode using EmptyDir. Unable to use PersistenceVolumeClaim", true)
			//re queue
			return ctrl.Result{}, fmt.Errorf("Redisgraph Pod with PVC not running")
		} else {
			//Write Status
			updateCR(r.Client, instance, "Persistence using PersistenceVolumeClaim", false)
		}
	} else {
		if !instance.Spec.Persistence && !instance.Spec.Degraded && isPodRunning(r.Client, instance) && instance.Status.PersistenceStatus == "Node level persistence using EmptyDir" {
			return ctrl.Result{}, nil
		}
		if instance.Spec.Degraded && isPodRunning(r.Client, instance) && instance.Status.PersistenceStatus == "Degraded mode using EmptyDir. Unable to use PersistenceVolumeClaim" {
			return ctrl.Result{}, nil
		}
		r.Log.Info("Using Empty dir Deployment")
		executeDeployment(r.Client, instance, false, r.Scheme)

		if isPodRunning(r.Client, instance) {
			if !instance.Spec.Degraded {
				//Write Status
				updateCR(r.Client, instance, "Node level persistence using EmptyDir", false)
			}
		} else {
			r.Log.Info("Unable to create Redisgraph Deployment")
			return ctrl.Result{}, fmt.Errorf("Redisgraph Pod not running")
		}

	}

	return ctrl.Result{}, nil
}

func (r *SearchOperatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&searchv1alpha1.SearchOperator{}).
		Complete(r)
}

func int32Ptr(i int32) *int32 { return &i }

func getDeployment(cr *searchv1alpha1.SearchOperator, rdbVolumeSource v1.VolumeSource) *appv1.Deployment {

	return &appv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "search-prod-redisgraph",
			Namespace: cr.Namespace,
			Annotations: map[string]string{
				"owner": "search-operator",
			},
		},
		Spec: appv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"component": "redisgraph",
					"app":       "search-prod",
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"component": "redisgraph",
						"app":       "search-prod",
					},
				},
				Spec: v1.PodSpec{
					ServiceAccountName: "search-operator",
					ImagePullSecrets: []v1.LocalObjectReference{{
						Name: cr.Spec.PullSecret,
					}},
					Containers: []v1.Container{
						{
							Name:  "redisgraph",
							Image: cr.Spec.RedisgraphImage,
							Env: []v1.EnvVar{
								{
									Name: "REDIS_PASSWORD",
									ValueFrom: &v1.EnvVarSource{
										SecretKeyRef: &v1.SecretKeySelector{
											LocalObjectReference: v1.LocalObjectReference{
												Name: "redisgraph-user-secret",
											},
											Key: "redispwd",
										},
									},
								},
								{
									Name:  "REDIS_GRAPH_SSL",
									Value: "true",
								},
							},
							VolumeMounts: []v1.VolumeMount{
								{
									Name:      "redis-graph-certs",
									MountPath: "/certs",
								},
								{
									Name:      "stunnel-pid",
									MountPath: "/rg",
								},
								{
									Name:      "persist",
									MountPath: "/redis-data",
								},
							},
						},
					},
					Volumes: []v1.Volume{
						{
							Name: "stunnel-pid",
							VolumeSource: v1.VolumeSource{
								EmptyDir: &v1.EmptyDirVolumeSource{},
							},
						},
						{
							Name:         "persist",
							VolumeSource: rdbVolumeSource,
						},
						{
							Name: "redis-graph-certs",
							VolumeSource: v1.VolumeSource{
								Secret: &v1.SecretVolumeSource{
									SecretName: "search-prod-e5b24-redisgraph-secrets",
									Items: []v1.KeyToPath{
										{
											Key:  "ca.crt",
											Path: "redis.crt",
										},
										{
											Key:  "tls.crt",
											Path: "server.crt",
										},
										{
											Key:  "tls.key",
											Path: "server.key",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func updateCR(kclient client.Client, cr *searchv1alpha1.SearchOperator, status string, degrade bool) {
	found := &searchv1alpha1.SearchOperator{}
	err := kclient.Get(context.TODO(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, found)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to get SearchOperator %s/%s ", cr.Namespace, cr.Name))
	}
	if degrade {
		cr.Spec.Degraded = degrade
		err := kclient.Update(context.TODO(), cr)
		if err != nil {
			log.Error(err, fmt.Sprintf("Failed to update SearchOperator %s/%s  ", cr.Namespace, cr.Name))
			return
		}
	}
	cr.Status.PersistenceStatus = status
	err = kclient.Status().Update(context.TODO(), cr)
	if err != nil {
		if apierrors.IsConflict(err) {
			log.Info("Failed to update status Object has been modified")
		}
		log.Error(err, fmt.Sprintf("Failed to update %s/%s status ", cr.Namespace, cr.Name))

	} else {
		log.Info(fmt.Sprintf("Updated CR status with degrade/persistence %v/%v  ", cr.Spec.Degraded, cr.Spec.Persistence))
	}
}

func updateRedisDeployment(client client.Client, deployment *appv1.Deployment, namespace string) {
	found := &appv1.Deployment{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: "search-prod-redisgraph", Namespace: namespace}, found)
	if err != nil {
		if errors.IsNotFound(err) {
			err = client.Create(context.TODO(), deployment)
			if err != nil {
				log.Error(err, "Failed to create  deployment")
				return
			}
			log.Info("Created new  deployment ")
		} else {
			log.Error(err, "Failed to get deployment")
			return
		}
	} else {
		if !reflect.DeepEqual(found.Spec, deployment.Spec) {
			deployment.ObjectMeta.ResourceVersion = found.ObjectMeta.ResourceVersion
			err = client.Update(context.TODO(), deployment)
			if err != nil {
				log.Error(err, "Failed to update  deployment")
				return
			}
			log.Info("Peristence Volume %s Updated for  redisgraph deployment ")
		} else {
			log.Info("No changes for redisgraph deployment ")
		}
	}
}

func getPVC(cr *searchv1alpha1.SearchOperator) *v1.PersistentVolumeClaim {
	if cr.Spec.StorageClass != "" {
		return &v1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pvcName,
				Namespace: cr.Namespace,
			},
			Spec: v1.PersistentVolumeClaimSpec{
				AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceName(v1.ResourceStorage): resource.MustParse(cr.Spec.StorageSize),
					},
				},
				StorageClassName: &cr.Spec.StorageClass,
			},
		}
	}
	return &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: cr.Namespace,
		},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceName(v1.ResourceStorage): resource.MustParse(cr.Spec.StorageSize),
				},
			},
		},
	}
}

//Remove PVC if you have one
func setupVolume(client client.Client, cr *searchv1alpha1.SearchOperator) {
	found := &v1.PersistentVolumeClaim{}
	pvc := getPVC(cr)
	err := client.Get(context.TODO(), types.NamespacedName{Name: pvcName, Namespace: cr.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		err = client.Create(context.TODO(), pvc)
		//Return True if sucessfully created pvc else return False
		if err != nil {
			log.Info("Error Creating a new PVC ", "PVC.Namespace", cr.Namespace, "PVC.Name", pvcName)
			log.Info(err.Error())
			return
		} else {
			log.Info("Created a new PVC ", "PVC.Namespace", cr.Namespace, "PVC.Name", pvcName)
			return
		}
	} else if err != nil {
		log.Info("Error finding  PVC ", "PVC.Namespace", cr.Namespace, "PVC.Name", pvcName)
		//return False and error if there is Error
		return
	}
	log.Info("Using existing PVC")
}

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
func newRedisSecret(cr *searchv1alpha1.SearchOperator) *corev1.Secret {
	labels := map[string]string{
		"app": "search",
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "redisgraph-user-secret",
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Data: map[string][]byte{
			"redispwd": generatePass(16),
		},
	}
}

func podScheduled(kclient client.Client, cr *searchv1alpha1.SearchOperator, waitSeconds int) bool {
	podList := &corev1.PodList{}
	opts := []client.ListOption{client.MatchingLabels{"component": "redisgraph"}}
	//Check pod in 1 seconds
	time.Sleep(1 * time.Second)
	err := kclient.List(context.TODO(), podList, opts...)
	if err != nil {
		return false
	}
	//Keep checking status until waitSeconds
	// We assume its not running
	count := 0
	for count < waitSeconds {
		for _, item := range podList.Items {
			for _, status := range item.Status.Conditions {
				if status.Reason == "Unschedulable" {
					log.Info("RedisGraph Pod UnScheduleable - likely PVC mount problem")
					return false
				}
			}
			for _, status := range item.Status.ContainerStatuses {
				if status.Ready {
					for _, name := range item.Spec.Volumes {
						if name.PersistentVolumeClaim != nil && name.PersistentVolumeClaim.ClaimName == pvcName {
							log.Info("RedisGraph Pod With PVC Ready")
							return true
						}
					}

				}
			}
		}
		count++
		time.Sleep(1 * time.Second)
	}

	return false
}

func isPodRunning(kclient client.Client, cr *searchv1alpha1.SearchOperator) bool {
	podList := &corev1.PodList{}
	opts := []client.ListOption{client.MatchingLabels{"component": "redisgraph"}}
	err := kclient.List(context.TODO(), podList, opts...)
	if err != nil {
		return false
	}
	for _, item := range podList.Items {
		for _, status := range item.Status.Conditions {
			if status.Reason == "Unschedulable" {
				log.Info("RedisGraph Pod UnScheduleable ")
				return false
			}
		}
		for _, status := range item.Status.ContainerStatuses {
			if status.Ready {
				log.Info("RedisGraph Pod Ready")
				return true
			}
		}
	}

	return false
}

func executeDeployment(client client.Client, cr *searchv1alpha1.SearchOperator, usePVC bool,
	scheme *runtime.Scheme) *appv1.Deployment {
	var deployment *appv1.Deployment
	emptyDirVolume := v1.VolumeSource{
		EmptyDir: &v1.EmptyDirVolumeSource{},
	}
	pvcVolume := v1.VolumeSource{
		PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
			ClaimName: pvcName,
		},
	}
	if !usePVC {
		deployment = getDeployment(cr, emptyDirVolume)
	} else {
		deployment = getDeployment(cr, pvcVolume)
	}
	if err := controllerutil.SetControllerReference(cr, deployment, scheme); err != nil {
		log.Info("Cannot set deployment OwnerReference", err.Error())
	}
	updateRedisDeployment(client, deployment, cr.Namespace)
	return deployment
}
