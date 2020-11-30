// Copyright (c) 2020 Red Hat, Inc.

package controllers

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"reflect"
	"time"

	"k8s.io/apimachinery/pkg/util/intstr"

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

const (
	pvcName                 = "redisgraph-pvc"
	appName                 = "search"
	component               = "redisgraph"
	statefulSetName         = "search-redisgraph"
	redisNotRunning         = "Redisgraph Pod not running"
	statusUsingPVC          = "Redisgraph is using PersistenceVolumeClaim"
	statusDegradedEmptyDir  = "Degraded mode using EmptyDir. Unable to use PersistenceVolumeClaim"
	statusUsingNodeEmptyDir = "Node level persistence using EmptyDir"
)

var (
	waitSecondsForPodChk = 180 //Wait for 3 minutes
	log                  = logf.Log.WithName("searchoperator")
)

func (r *SearchOperatorReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	_ = r.Log.WithValues("searchoperator", req.NamespacedName)

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

	persistence := instance.Spec.Persistence
	persistenceStatus := instance.Status.PersistenceStatus
	allowdegrade := instance.Spec.AllowDegradeMode
	// Setup RedisGraph Deployment
	r.Log.Info(fmt.Sprintf("Config in Use Persistence/AllowDegrade %v/%v", persistence, allowdegrade))
	if persistence {
		//If running PVC deployment nothing to do
		if isPodRunning(r.Client, instance, true, 1) && persistenceStatus == statusUsingPVC {
			return ctrl.Result{}, nil
		}
		//If running degraded deployment AND AllowDegradeMode is set
		if isPodRunning(r.Client, instance, false, 1) && allowdegrade &&
			persistenceStatus == statusDegradedEmptyDir {
			return ctrl.Result{}, nil
		}
		setupVolume(r.Client, instance)
		executeDeployment(r.Client, instance, true, r.Scheme)
		podReady := isPodRunning(r.Client, instance, true, waitSecondsForPodChk)
		if podReady {
			//Write Status
			err := updateCR(r.Client, instance, statusUsingPVC)
			if err != nil {
				return ctrl.Result{}, err
			}
		}
		//If Pod cannot be scheduled rollback to EmptyDir if AllowDegradeMode is set
		if !podReady && allowdegrade {
			r.Log.Info("Degrading Redisgraph deployment to use empty dir.")
			err := deleteRedisStatefulSet(r.Client, instance.Namespace)
			if err != nil {
				return ctrl.Result{}, err
			}
			executeDeployment(r.Client, instance, false, r.Scheme)
			if isPodRunning(r.Client, instance, false, waitSecondsForPodChk) {
				//Write Status
				err := updateCR(r.Client, instance, statusDegradedEmptyDir)
				if err != nil {
					return ctrl.Result{}, err
				} else {
					return ctrl.Result{}, nil
				}
			} else {
				r.Log.Info("Unable to create Redisgraph Deployment in Degraded Mode")
				return ctrl.Result{}, fmt.Errorf(redisNotRunning)
			}
		}
		if !podReady && !allowdegrade {
			r.Log.Info("Unable to create Redisgraph Deployment using PVC ")
			return ctrl.Result{}, fmt.Errorf(redisNotRunning)
		}
	} else {
		if !persistence && isPodRunning(r.Client, instance, false, 1) &&
			persistenceStatus == statusUsingNodeEmptyDir {
			return ctrl.Result{}, nil
		}
		r.Log.Info("Using Empty dir Deployment")
		executeDeployment(r.Client, instance, false, r.Scheme)
		if isPodRunning(r.Client, instance, false, waitSecondsForPodChk) {
			//Write Status
			err := updateCR(r.Client, instance, statusUsingNodeEmptyDir)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else {
			r.Log.Info("Unable to create Redisgraph Deployment")
			return ctrl.Result{}, fmt.Errorf(redisNotRunning)
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

func getStatefulSet(cr *searchv1alpha1.SearchOperator, rdbVolumeSource v1.VolumeSource) *appv1.StatefulSet {
	bool := false
	intVal := int64(0)
	return &appv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      statefulSetName,
			Namespace: cr.Namespace,
			Annotations: map[string]string{
				"owner": "search-operator",
			},
		},
		Spec: appv1.StatefulSetSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"component": component,
					"app":       appName,
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"component": component,
						"app":       appName,
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
							Image: cr.Spec.SearchImageOverrides.Redisgraph_TLS,
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
							LivenessProbe: &v1.Probe{
								InitialDelaySeconds: 10,
								TimeoutSeconds:      1,
								PeriodSeconds:       15,
								SuccessThreshold:    1,
								FailureThreshold:    3,
								Handler: v1.Handler{
									TCPSocket: &v1.TCPSocketAction{
										Port: intstr.FromString("6380"),
									},
								},
							},
							ReadinessProbe: &v1.Probe{
								InitialDelaySeconds: 5,
								TimeoutSeconds:      1,
								PeriodSeconds:       15,
								SuccessThreshold:    1,
								FailureThreshold:    3,
								Handler: v1.Handler{
									TCPSocket: &v1.TCPSocketAction{
										Port: intstr.FromString("6380"),
									},
								},
							},
							Resources: v1.ResourceRequirements{
								Limits: v1.ResourceList{
									"memory": resource.MustParse(cr.Spec.Redisgraph_Resource.LimitMemory),
								},
								Requests: v1.ResourceList{
									"cpu":    resource.MustParse(cr.Spec.Redisgraph_Resource.RequestCPU),
									"memory": resource.MustParse(cr.Spec.Redisgraph_Resource.RequestMemory),
								},
							},
							TerminationMessagePolicy: "File",
							TerminationMessagePath:   "/dev/termination-log",
							SecurityContext: &v1.SecurityContext{
								Privileged:               &bool,
								AllowPrivilegeEscalation: &bool,
								RunAsUser:                &intVal,
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
									SecretName: "search-redisgraph-secrets",
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

func updateCR(kclient client.Client, cr *searchv1alpha1.SearchOperator, status string) error {
	found := &searchv1alpha1.SearchOperator{}
	err := kclient.Get(context.TODO(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, found)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to get SearchOperator %s/%s ", cr.Namespace, cr.Name))
		return err
	}
	cr.Status.PersistenceStatus = status
	err = kclient.Status().Update(context.TODO(), cr)
	if err != nil {
		if apierrors.IsConflict(err) {
			log.Info("Failed to update status Object has been modified")
		}
		log.Error(err, fmt.Sprintf("Failed to update %s/%s status ", cr.Namespace, cr.Name))
		return err
	} else {
		log.Info(fmt.Sprintf("Updated CR status with persistence %s  ", cr.Status.PersistenceStatus))
	}
	return nil
}

func updateRedisStatefulSet(client client.Client, deployment *appv1.StatefulSet, namespace string) {
	found := &appv1.StatefulSet{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: statefulSetName, Namespace: namespace}, found)
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
				log.Error(err, "Failed to update deployment")
				return
			}
			log.Info("Volume source updated for redisgraph deployment ")
		} else {
			log.Info("No changes for redisgraph deployment ")
		}
	}
}
func deleteRedisStatefulSet(client client.Client, namespace string) error {
	statefulset := &appv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      statefulSetName,
			Namespace: namespace,
		},
	}
	err := client.Delete(context.TODO(), statefulset)
	if err != nil && !errors.IsNotFound(err) {
		log.Error(err, "Failed to delete search redisgraph statefulset", "name", statefulSetName)
		return err
	}
	time.Sleep(1 * time.Second) //Sleep for a minute to avoid quick update of statefulset
	log.Info("StatefulSet deleted", "name", statefulSetName)
	return nil
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
	logKeyPVCName := "PVC Name"
	if err != nil && errors.IsNotFound(err) {
		err = client.Create(context.TODO(), pvc)
		//Return True if sucessfully created pvc else return False
		if err != nil {
			log.Info("Error creating a new PVC ", logKeyPVCName, pvcName)
			log.Info(err.Error())
			return
		} else {
			log.Info("Created a new PVC ", logKeyPVCName, pvcName)
			return
		}
	} else if err != nil {
		log.Info("Error finding PVC ", logKeyPVCName, pvcName)
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

func isPodRunning(kclient client.Client, cr *searchv1alpha1.SearchOperator, withPVC bool, waitSeconds int) bool {
	log.Info("Checking Redisgraph Pod Status...")
	//Keep checking status until waitSeconds
	// We assume its not running
	count := 0
	for count < waitSeconds {
		podList := &corev1.PodList{}
		opts := []client.ListOption{client.MatchingLabels{"app": appName, "component": "redisgraph"}}
		err := kclient.List(context.TODO(), podList, opts...)
		if err != nil {
			log.Info("Error listing redisgraph pods. ", err)
			return false
		}
		for _, item := range podList.Items {
			if isReady(item, withPVC) {
				log.Info("Redisgraph Pod Running...")
				return true
			}
		}
		count++
		time.Sleep(1 * time.Second)
	}
	log.Info("Redisgraph Pod not Running...")
	return false
}

func isReady(pod v1.Pod, withPVC bool) bool {
	for _, status := range pod.Status.Conditions {
		if status.Reason == "Unschedulable" {
			log.Info("RedisGraph Pod UnScheduleable - likely PVC mount problem")
			return false
		}
	}
	for _, status := range pod.Status.ContainerStatuses {
		if status.Ready {
			for _, name := range pod.Spec.Volumes {
				if withPVC && name.PersistentVolumeClaim != nil && name.PersistentVolumeClaim.ClaimName == pvcName {
					log.Info("RedisGraph Pod with PVC Ready")
					return true
				} else if !withPVC && name.EmptyDir != nil {
					log.Info("RedisGraph Pod with EmptyDir Ready")
					return true
				}
			}
		}
	}
	return false
}

func executeDeployment(client client.Client, cr *searchv1alpha1.SearchOperator, usePVC bool,
	scheme *runtime.Scheme) *appv1.StatefulSet {
	var statefulSet *appv1.StatefulSet
	emptyDirVolume := v1.VolumeSource{
		EmptyDir: &v1.EmptyDirVolumeSource{},
	}
	pvcVolume := v1.VolumeSource{
		PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
			ClaimName: pvcName,
		},
	}
	if !usePVC {
		statefulSet = getStatefulSet(cr, emptyDirVolume)
	} else {
		statefulSet = getStatefulSet(cr, pvcVolume)
	}
	if err := controllerutil.SetControllerReference(cr, statefulSet, scheme); err != nil {
		log.Info("Cannot set statefulSet OwnerReference", err.Error())
	}
	updateRedisStatefulSet(client, statefulSet, cr.Namespace)
	return statefulSet
}
