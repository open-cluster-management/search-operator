// Copyright (c) 2020 Red Hat, Inc.

package controllers

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// SearchOperatorReconciler reconciles a SearchOperator object
type SearchOperatorReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

const (
	appName                   = "search"
	component                 = "redisgraph"
	statefulSetName           = "search-redisgraph"
	redisNotRunning           = "Redisgraph Pod not running"
	statusUsingPVC            = "Redisgraph is using PersistenceVolumeClaim"
	statusDegradedEmptyDir    = "Degraded mode using EmptyDir. Unable to use PersistenceVolumeClaim"
	statusUsingNodeEmptyDir   = "Node level persistence using EmptyDir"
	statusFailedDegraded      = "Unable to create Redisgraph Deployment in Degraded Mode"
	statusFailedUsingPVC      = "Unable to create Redisgraph Deployment using PVC"
	statusFailedNoPersistence = "Unable to create Redisgraph Deployment"
	redisUser                 = int64(10001)
)

var (
	pvcName              = "acm-search-redisgraph-0"
	waitSecondsForPodChk = 180 //Wait for 3 minutes
	log                  = logf.Log.WithName("searchoperator")
	persistence          = true
	allowdegrade         = true
	storageClass         = ""
	storageSize          = "10Gi"
	namespace            = os.Getenv("WATCH_NAMESPACE")
)

func (r *SearchOperatorReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	_ = context.Background()
	_ = r.Log.WithValues("searchoperator", req.NamespacedName)

	// Fetch the SearchOperator instance
	instance := &searchv1alpha1.SearchOperator{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: "searchoperator", Namespace: req.Namespace}, instance)
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

	// Fetch the SearchCustomization instance
	custom := &searchv1alpha1.SearchCustomization{}
	customValuesInuse := false
	err = r.Client.Get(context.TODO(), types.NamespacedName{Name: "searchcustomization", Namespace: req.Namespace}, custom)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Set the values to defult
			persistence = true
			allowdegrade = true
			storageClass = ""
			storageSize = "10Gi"
		} else {
			return ctrl.Result{}, err
		}

	} else {
		if custom.Spec.FallbackToEmptyDir != nil {
			allowdegrade = *custom.Spec.FallbackToEmptyDir
		} else {
			allowdegrade = false
		}
		if custom.Spec.Persistence != nil {
			persistence = *custom.Spec.Persistence
		}
		if custom.Spec.StorageClass != "" {
			persistence = true
		}
		//set the  user provided values
		customValuesInuse = true
		storageClass = custom.Spec.StorageClass
		if storageClass != "" {
			pvcName = storageClass + "-search-redisgraph-0"
		}
		if custom.Spec.StorageSize != "" {
			storageSize = custom.Spec.StorageSize
		}
		r.Log.Info(fmt.Sprintf("Storage %s", storageSize))
	}
	r.Log.Info("Checking if customization CR is created..", " Custom Values In use? ", customValuesInuse)
	r.Log.Info("Values in use: ", "persistence? ", persistence, " storageClass? ", storageClass,
		" storageSize? ", storageSize, " fallBackToEmptyDir? ", allowdegrade)

	// Create secret if not found
	err = r.setupSecret(r.Client, instance)
	if err != nil {
		// Error setting up secret - requeue the request.
		return ctrl.Result{}, err
	}

	persistenceStatus := instance.Status.PersistenceStatus

	// Setup RedisGraph Deployment
	r.Log.Info(fmt.Sprintf("Config in  Use Persistence/AllowDegrade %t/%t", persistence, allowdegrade))
	if persistence {
		//If running PVC deployment nothing to do
		if isStatefulSetAvailable(r.Client) && isPodRunning(r.Client, true, 1) && persistenceStatus == statusUsingPVC {
			return ctrl.Result{}, nil
		}
		//If running degraded deployment AND AllowDegradeMode is set
		if isStatefulSetAvailable(r.Client) && isPodRunning(r.Client, false, 1) && allowdegrade &&
			persistenceStatus == statusDegradedEmptyDir {
			return ctrl.Result{}, nil
		}
		pvcError := setupVolume(r.Client)
		if pvcError != nil {
			return ctrl.Result{}, pvcError
		}
		r.executeDeployment(r.Client, instance, true)
		podReady := isPodRunning(r.Client, true, waitSecondsForPodChk)
		if podReady {
			//Write Status
			err := updateCRs(r.Client, instance, statusUsingPVC,
				custom, persistence, storageClass, storageSize, customValuesInuse)
			if err != nil {
				return ctrl.Result{}, err
			}
		}
		//If Pod cannot be scheduled rollback to EmptyDir if AllowDegradeMode is set
		if !podReady && allowdegrade {
			r.Log.Info("Degrading Redisgraph deployment to use empty dir.")
			err := deleteRedisStatefulSet(r.Client)
			if err != nil {
				return ctrl.Result{}, err
			}
			err = deletePVC(r.Client)
			if err != nil {
				return ctrl.Result{}, err
			}
			r.executeDeployment(r.Client, instance, false)
			if isPodRunning(r.Client, false, waitSecondsForPodChk) {
				//Write Status
				err := updateCRs(r.Client, instance, statusDegradedEmptyDir, custom, false, "", "", customValuesInuse)
				if err != nil {
					return ctrl.Result{}, err
				} else {
					return ctrl.Result{}, nil
				}
			} else {
				r.Log.Info("Unable to create Redisgraph Deployment in Degraded Mode")
				//TODO: Move to a common function as these steps are common whenever redis pod is not running
				//Write Status, delete statefulset and requeue
				if err = updateCRs(r.Client, instance, statusFailedDegraded, custom, false, "", "", customValuesInuse); err != nil {
					r.Log.Info("Error updating operator/customization status. ", "Error: ", err)
				}
				if err = deleteRedisStatefulSet(r.Client); err != nil {
					r.Log.Info("Error deleting statefulset. ", "Error: ", err)
				}
				return ctrl.Result{RequeueAfter: 5 * time.Second}, fmt.Errorf(redisNotRunning)
			}
		}
		if !podReady && !allowdegrade {
			r.Log.Info("Unable to create Redisgraph Deployment using PVC ")
			//Write Status, delete statefulset and requeue
			if err = updateCRs(r.Client, instance, statusFailedUsingPVC, custom, false, "", "", customValuesInuse); err != nil {
				r.Log.Info("Error updating operator/customization status. ", "Error: ", err)
			}
			if err = deleteRedisStatefulSet(r.Client); err != nil {
				r.Log.Info("Error deleting statefulset. ", "Error: ", err)
			}
			return ctrl.Result{RequeueAfter: 5 * time.Second}, fmt.Errorf(redisNotRunning)
		}
	} else {
		if !persistence && isStatefulSetAvailable(r.Client) && isPodRunning(r.Client, false, 1) &&
			persistenceStatus == statusUsingNodeEmptyDir {
			return ctrl.Result{}, nil
		}
		r.Log.Info("Using Empty dir Deployment")
		r.executeDeployment(r.Client, instance, false)
		if isPodRunning(r.Client, false, waitSecondsForPodChk) {
			//Write Status, if error - requeue
			err := updateCRs(r.Client, instance, statusUsingNodeEmptyDir, custom, false, "", "", customValuesInuse)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else {
			r.Log.Info("Unable to create Redisgraph Deployment")
			//Write Status, delete statefulset and requeue
			if err = updateCRs(r.Client, instance, statusFailedNoPersistence, custom, false, "", "", customValuesInuse); err != nil {
				r.Log.Info("Error updating operator/customization status. ", "Error: ", err)
			}
			if err = deleteRedisStatefulSet(r.Client); err != nil {
				r.Log.Info("Error deleting statefulset. ", "Error: ", err)
			}
			return ctrl.Result{RequeueAfter: 5 * time.Second}, fmt.Errorf(redisNotRunning)
		}

	}
	return ctrl.Result{}, nil
}

func (r *SearchOperatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	watchNamespace := os.Getenv("WATCH_NAMESPACE")
	pred := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return e.Meta.GetNamespace() == watchNamespace
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.MetaNew.GetNamespace() == watchNamespace &&
				e.MetaNew.GetGeneration() != e.MetaOld.GetGeneration() {
				return true
			}
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			if e.Meta.GetNamespace() == watchNamespace {
				return !e.DeleteStateUnknown
			}
			return false
		},
	}

	searchCustomizationFn := handler.ToRequestsFunc(
		func(a handler.MapObject) []reconcile.Request {
			return []reconcile.Request{
				{NamespacedName: types.NamespacedName{
					Name:      "searchcustomization",
					Namespace: watchNamespace,
				}},
			}
		})

	return ctrl.NewControllerManagedBy(mgr).
		For(&searchv1alpha1.SearchOperator{}).
		Owns(&appv1.StatefulSet{}).
		Owns(&corev1.Secret{}).
		Watches(&source.Kind{Type: &searchv1alpha1.SearchCustomization{}},
			&handler.EnqueueRequestsFromMapFunc{ToRequests: searchCustomizationFn}).
		WithEventFilter(pred).
		Complete(r)
}

func int32Ptr(i int32) *int32 { return &i }

func int64Ptr(i int64) *int64 { return &i }

func (r *SearchOperatorReconciler) getStatefulSet(cr *searchv1alpha1.SearchOperator, rdbVolumeSource v1.VolumeSource) *appv1.StatefulSet {
	bool := false
	sset := &appv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      statefulSetName,
			Namespace: cr.Namespace,
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
					SecurityContext: &v1.PodSecurityContext{
						FSGroup:   int64Ptr(redisUser),
						RunAsUser: int64Ptr(redisUser),
					},
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
										Port: intstr.FromInt(6380),
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
										Port: intstr.FromInt(6380),
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
	if err := ctrl.SetControllerReference(cr, sset, r.Scheme); err != nil {
		log.Info("Cannot set statefulSet OwnerReference", err.Error())
	}
	return sset
}
func updateCRs(kclient client.Client, operatorCR *searchv1alpha1.SearchOperator, status string,
	customizationCR *searchv1alpha1.SearchCustomization, persistence bool, storageClass string,
	storageSize string, customValuesInuse bool) error {
	var err error
	err = updateOperatorCR(kclient, operatorCR, status)
	if err != nil {
		return err
	}
	if customValuesInuse {
		err = updateCustomizationCR(kclient, customizationCR, persistence, storageClass, storageSize)
		if err != nil {
			return err
		}
	}
	return nil
}

func updateOperatorCR(kclient client.Client, cr *searchv1alpha1.SearchOperator, status string) error {
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

func updateCustomizationCR(kclient client.Client, cr *searchv1alpha1.SearchCustomization,
	persistence bool, storageClass string, storageSize string) error {
	found := &searchv1alpha1.SearchCustomization{}
	err := kclient.Get(context.TODO(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, found)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to get SearchCustomization %s/%s ", cr.Namespace, cr.Name))
		return err
	}
	cr.Status.Persistence = persistence
	cr.Status.StorageClass = storageClass
	cr.Status.StorageSize = storageSize
	err = kclient.Status().Update(context.TODO(), cr)
	if err != nil {
		if apierrors.IsConflict(err) {
			log.Info("Failed to update status Object has been modified")
		}
		log.Error(err, fmt.Sprintf("Failed to update %s/%s status ", cr.Namespace, cr.Name))
		return err
	} else {
		log.Info(fmt.Sprintf("Updated CR status with custom persistence %t ", cr.Status.Persistence))
	}
	return nil
}

func updateRedisStatefulSet(client client.Client, deployment *appv1.StatefulSet) {
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
func deleteRedisStatefulSet(client client.Client) error {
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

func deletePVC(client client.Client) error {
	pvc := &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: namespace,
		},
	}
	err := client.Delete(context.TODO(), pvc)
	if err != nil && !errors.IsNotFound(err) {
		log.Error(err, "Failed to delete search redisgraph PVC", "name", pvcName)
		return err
	}
	time.Sleep(1 * time.Second) //Sleep for a minute to avoid quick update of statefulset
	log.Info("PVC deleted", "name", pvcName)
	return nil
}

func getPVC() *v1.PersistentVolumeClaim {
	if storageClass != "" {
		return &v1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pvcName,
				Namespace: namespace,
			},
			Spec: v1.PersistentVolumeClaimSpec{
				AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceName(v1.ResourceStorage): resource.MustParse(storageSize),
					},
				},
				StorageClassName: &storageClass,
			},
		}
	}
	return &v1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: namespace,
		},
		Spec: v1.PersistentVolumeClaimSpec{
			AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceName(v1.ResourceStorage): resource.MustParse(storageSize),
				},
			},
		},
	}
}

//Remove PVC if you have one
func setupVolume(client client.Client) error {
	found := &v1.PersistentVolumeClaim{}
	pvc := getPVC()
	err := client.Get(context.TODO(), types.NamespacedName{Name: pvcName, Namespace: namespace}, found)
	logKeyPVCName := "PVC Name"
	if err != nil && errors.IsNotFound(err) {
		err = client.Create(context.TODO(), pvc)
		//Return True if sucessfully created pvc else return False
		if err != nil {
			log.Info("Error creating a new PVC ", logKeyPVCName, pvcName)
			log.Info(err.Error())
			return err
		} else {
			log.Info("Created a new PVC ", logKeyPVCName, pvcName)
			return nil
		}
	} else if err != nil {
		log.Info("Error finding PVC ", logKeyPVCName, pvcName)
		//return False and error if there is Error
		return err
	}
	log.Info("Using existing PVC")
	return nil
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

func isStatefulSetAvailable(kclient client.Client) bool {
	//check if statefulset is present if not we can assume the pod is not running
	found := &appv1.StatefulSet{}
	err := kclient.Get(context.TODO(), types.NamespacedName{Name: statefulSetName, Namespace: namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		return false
	}
	return true
}

func isPodRunning(kclient client.Client, withPVC bool, waitSeconds int) bool {
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

func (r *SearchOperatorReconciler) executeDeployment(client client.Client, cr *searchv1alpha1.SearchOperator, usePVC bool) *appv1.StatefulSet {
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
		statefulSet = r.getStatefulSet(cr, emptyDirVolume)
	} else {
		statefulSet = r.getStatefulSet(cr, pvcVolume)
	}
	updateRedisStatefulSet(client, statefulSet)
	return statefulSet
}

func (r *SearchOperatorReconciler) setupSecret(client client.Client, cr *searchv1alpha1.SearchOperator) error {
	// Define a new Secret object
	secret := newRedisSecret(cr, r.Scheme)
	// Check if this Secret already exists
	found := &corev1.Secret{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating a new Secret", "Secret.Namespace", secret.Namespace, "Secret.Name", secret.Name)
		err = client.Create(context.TODO(), secret)
		if err != nil {
			return err
		}
		// Secret created successfully - don't requeue
		return nil
	} else if err != nil {
		return err
	} else {
		// Secret already exists - don't requeue
		log.Info("Skip reconcile: Secret already exists", "Secret.Namespace", found.Namespace, "Secret.Name", found.Name)
	}
	return nil
}
