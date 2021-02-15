// Copyright (c) 2020 Red Hat, Inc.

package controllers

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"strings"
	"time"

	newerr "errors"

	"github.com/go-logr/logr"
	searchv1alpha1 "github.com/open-cluster-management/search-operator/api/v1alpha1"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
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

type StartingSpec struct {
	allowdegrade bool
	withPVC      bool
	custSpec     searchv1alpha1.SearchCustomizationSpec
}
type specList struct {
	desiredSpec  searchv1alpha1.SearchCustomizationSpec
	startingSpec StartingSpec
}

const (
	appName         = "search"
	component       = "redisgraph"
	statefulSetName = "search-redisgraph"
	podName         = "search-redisgraph-0"

	redisNotRunning           = "Redisgraph Pod not running"
	statusUsingPVC            = "Redisgraph is using PersistenceVolumeClaim"
	statusDegradedEmptyDir    = "Degraded mode using EmptyDir. Unable to use PersistenceVolumeClaim"
	statusUsingNodeEmptyDir   = "Node level persistence using EmptyDir"
	statusFailedDegraded      = "Unable to create Redisgraph Deployment in Degraded Mode"
	statusFailedUsingPVC      = "Unable to create Redisgraph Deployment using PVC"
	statusFailedNoPersistence = "Unable to create Redisgraph Deployment"
	statusNoPersistence       = "Redisgraph pod running with persistence disabled"
	redisUser                 = int64(10001)
	defaultPvcName            = "search-redisgraph-pvc-0"
	noMatchSpec               = "Spec of the current running redisgraph Pod  doesn't match desired Spec"
)

var (
	reconcileLoopCount      = 0
	pvcName                 = "search-redisgraph-pvc-0"
	waitSecondsForPodChk    = 180 //Wait for 3 minutes
	log                     = logf.Log.WithName("searchoperator")
	persistence             = true
	allowdegrade            = true
	storageClass            = ""
	storageSize             = "10Gi"
	namespace               = os.Getenv("WATCH_NAMESPACE")
	redisgraphMemoryRequest = "128Mi"
	redisgraphMemoryLimit   = "4Gi"
)

func (r *SearchOperatorReconciler) doNotRequeue() (ctrl.Result, error) {
	return ctrl.Result{}, nil
}
func (r *SearchOperatorReconciler) requeueOnErr(err error) (ctrl.Result, error) {
	return ctrl.Result{}, err
}
func (r *SearchOperatorReconciler) requeueAfter(duration time.Duration, err error) (ctrl.Result, error) {
	return ctrl.Result{RequeueAfter: duration}, err
}

func setDefaultValues() {
	persistence = true
	allowdegrade = true
	storageClass = ""
	storageSize = "10Gi"
	pvcName = defaultPvcName
	redisgraphMemoryRequest = "128Mi"
	redisgraphMemoryLimit = "4Gi"
}

func setCustomValues(custom *searchv1alpha1.SearchCustomization) {
	if custom.Spec.Persistence != nil && *custom.Spec.Persistence == false {
		persistence = false
	} else {
		persistence = true
	}
	// Allowdegrade mode helps the user to set the controller from switching back to emptydir - and debug users configuration
	allowdegrade = false
	storageClass = ""
	storageSize = "10Gi"
	pvcName = defaultPvcName
	redisgraphMemoryRequest = "128Mi"
	redisgraphMemoryLimit = "4Gi"
	if custom.Spec.StorageClass != "" {
		storageClass = custom.Spec.StorageClass
		pvcName = storageClass + "-search-redisgraph-0"
	}
	if custom.Spec.StorageSize != "" {
		storageSize = custom.Spec.StorageSize
	}
	if custom.Spec.RedisgraphMemoryLimit != "" {
		redisgraphMemoryLimit = custom.Spec.RedisgraphMemoryLimit
	}
	if custom.Spec.RedisgraphMemoryRequest != "" {
		redisgraphMemoryRequest = custom.Spec.RedisgraphMemoryRequest
	}

	//if persistence is false, don't use the storageClass and storagesize provided
	if !persistence {
		storageClass = ""
		storageSize = ""
	}
}

func (r *SearchOperatorReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {

	_ = context.Background()
	_ = r.Log.WithValues("searchoperator", req.NamespacedName)
	//TODO: Reset after value reaches max value
	reconcileLoopCount++
	fmt.Println("******************************RECONCILE LOOP COUNT: ", reconcileLoopCount, " ******************************")
	// Fetch the SearchOperator instance
	instance := &searchv1alpha1.SearchOperator{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: "searchoperator", Namespace: req.Namespace}, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return r.doNotRequeue()
		}
		// Error reading the object - requeue the request.
		return r.requeueOnErr(err)
	}

	// Fetch the SearchCustomization instance
	custom := &searchv1alpha1.SearchCustomization{}
	customValuesInuse := false
	desiredSpec := searchv1alpha1.SearchCustomizationSpec{}
	var startingSpec StartingSpec

	custom, err = fetchSrchC(r.Client, custom, req.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Set the values to defult
			setDefaultValues()

			// startingCustSpec := searchv1alpha1.SearchCustomizationSpec{Persistence: &persistence, StorageClass: storageClass,
			// 	RedisgraphMemoryLimit: redisgraphMemoryLimit, RedisgraphMemoryRequest: redisgraphMemoryRequest}
			// startingSpec1 = StartingSpec{custSpec: startingCustSpec, allowdegrade: allowdegrade}
			fmt.Printf("startingSpec without CustCR: Persistence: %+v, Spec: %+v\n", *&startingSpec.custSpec.Persistence, startingSpec)
		} else {
			return r.requeueOnErr(err)
		}

	} else {
		setCustomValues(custom)
		//set the  user provided values
		customValuesInuse = true
		desiredSpec = custom.Spec
		// startingCustSpec := searchv1alpha1.SearchCustomizationSpec{Persistence: &persistence, StorageClass: storageClass,
		// 	RedisgraphMemoryLimit: redisgraphMemoryLimit, RedisgraphMemoryRequest: redisgraphMemoryRequest}
		// startingSpec1 = StartingSpec{custSpec: startingCustSpec, allowdegrade: allowdegrade}

		// fmt.Println("startingSpec with CustCR: ", startingSpec1)
	}
	startingCustSpec := searchv1alpha1.SearchCustomizationSpec{Persistence: &persistence, StorageClass: storageClass,
		RedisgraphMemoryLimit: redisgraphMemoryLimit, RedisgraphMemoryRequest: redisgraphMemoryRequest}
	startingSpec = StartingSpec{custSpec: startingCustSpec, allowdegrade: allowdegrade}
	fmt.Printf("startingSpec: Persistence: %+v, Spec: %+v\n", *&startingSpec.custSpec.Persistence, startingSpec)

	r.Log.Info("Checking if customization CR is created..", " Custom Values In use? ", customValuesInuse)
	r.Log.Info("Values in use: ", "persistence? ", persistence, " storageClass? ", storageClass,
		" storageSize? ", storageSize, " fallbackToEmptyDir? ", allowdegrade)
	specList := specList{desiredSpec: desiredSpec, startingSpec: startingSpec}
	// Create secret if not found
	err = r.setupSecret(r.Client, instance)
	if err != nil {
		// Error setting up secret - requeue the request.
		return r.requeueOnErr(err)
	}
	//TODO: Check if default storage class present - move to a function
	//Read the searchoperator status
	persistenceStatus := instance.Status.PersistenceStatus

	scPresent := storageClassPresent(r.Client, storageClass)

	r.Log.Info(fmt.Sprintf("Config in  Use Persistence/AllowDegrade %t/%t", persistence, allowdegrade))

	if persistence {
		flag := scPresent || customValuesInuse
		switch flag {
		case true:
			fmt.Println("Try setting persistence")
			specList.startingSpec.withPVC = true
			// err.Error() == noMatchSpec
			podRunning, podRunningErr := r.isPodRunning(true, 1, specList)
			if persistenceStatus == statusUsingPVC && isStatefulSetAvailable(r.Client) && (podRunning && podRunningErr == nil) {
				fmt.Println("Returning from CASE TRUE: ", flag)

				return r.doNotRequeue()
			}
			pvcError := setupVolume(r.Client)
			if pvcError != nil {
				return r.requeueOnErr(pvcError)
			}
			r.executeDeployment(r.Client, instance, true, persistence)
			podReady, err := r.isPodRunning(true, waitSecondsForPodChk, specList)
			// if err != nil && err.Error() == noMatchSpec {
			// 	sts, err := fetchSts(r.Client, namespace)
			// 	if err == nil {
			// 		fmt.Print("sts found\n", sts.Name)
			// 	} else {
			// 		fmt.Println("error fetching sts")
			// 	}
			// }
			if podReady && err == nil {
				//Write Status
				err := updateCRs(r.Client, instance, statusUsingPVC,
					custom, persistence, storageClass, storageSize, customValuesInuse)
				if err != nil {
					return r.requeueOnErr(err)
				}
				r.doNotRequeue()
			}
			if !podReady && !allowdegrade || err != nil {
				r.Log.Info("Unable to create Redisgraph Deployment using PVC ")
				//Write Status, delete statefulset and requeue
				r.reconcileOnError(instance, statusFailedUsingPVC, custom, false, "", "", customValuesInuse)
				return r.requeueAfter(5*time.Second, newerr.New(redisNotRunning))
				// return ctrl.Result{RequeueAfter: 5 * time.Second}, fmt.Errorf(redisNotRunning)
			}
			fmt.Println("NOT Handled in CASE TRUE: ", flag)

		case false:
			fmt.Println("Degrade to emptyDir")
			specList.startingSpec.withPVC = false
			podRunning, podRunningErr := r.isPodRunning(false, 1, specList)

			if allowdegrade && persistenceStatus == statusDegradedEmptyDir && isStatefulSetAvailable(r.Client) && (podRunning && podRunningErr == nil) {
				fmt.Println("Returning from CASE FALSE: ", flag)
				return r.doNotRequeue()
			}
			r.Log.Info("Degrading Redisgraph deployment to use empty dir.")
			//TODO: Check if deletion is necessary
			err := deleteRedisStatefulSet(r.Client)
			if err != nil {
				return r.requeueOnErr(err)
			}
			err = deletePVC(r.Client)
			if err != nil {
				return r.requeueOnErr(err)
			}
			r.executeDeployment(r.Client, instance, false, persistence)
			podReady, err := r.isPodRunning(false, waitSecondsForPodChk, specList)

			if podReady && err == nil {
				//Write Status
				err := updateCRs(r.Client, instance, statusDegradedEmptyDir,
					custom, false, "", "", customValuesInuse)
				if err != nil {
					return r.requeueOnErr(err)
				}
				return r.doNotRequeue()

			}
			r.Log.Info("Unable to create Redisgraph Deployment in Degraded Mode")
			//Write Status, delete statefulset and requeue
			r.reconcileOnError(instance, statusFailedDegraded, custom, false, "", "", customValuesInuse)
			// return ctrl.Result{RequeueAfter: 5 * time.Second}, fmt.Errorf(redisNotRunning)
			return r.requeueAfter(5*time.Second, newerr.New(redisNotRunning))

			// fmt.Println("NOT Handled in CASE FALSE: ", flag)

		}
	} else {
		fmt.Println("Try setting no persistence")

		podRunning, podRunningErr := r.isPodRunning(false, 1, specList)
		if isStatefulSetAvailable(r.Client) && (podRunning && podRunningErr == nil) &&
			persistenceStatus == statusNoPersistence {
			return ctrl.Result{}, nil
		}
		r.Log.Info("Using Deployment with persistence disabled")
		r.executeDeployment(r.Client, instance, false, persistence)
		podReady, _ := r.isPodRunning(false, waitSecondsForPodChk, specList)
		if podReady && err == nil {
			//Write Status, if error - requeue
			err := updateCRs(r.Client, instance, statusNoPersistence, custom, false, "", "", customValuesInuse)
			if err != nil {
				return r.requeueOnErr(err)
			}
		} else {
			r.Log.Info("Unable to create Redisgraph Deployment with persistence disabled")
			//Write Status, delete statefulset and requeue
			r.reconcileOnError(instance, statusFailedNoPersistence, custom, false, "", "", customValuesInuse)
			// return ctrl.Result{RequeueAfter: 5 * time.Second}, fmt.Errorf(redisNotRunning)
			return r.requeueAfter(5*time.Second, newerr.New(redisNotRunning))
		}
	}
	return r.doNotRequeue()

	// if persistence {
	// 	//If running PVC deployment nothing to do
	// 	// if persistenceStatus == statusUsingPVC && isStatefulSetAvailable(r.Client) && r.isPodRunning(true, 1, specList) {
	// 	// 	return ctrl.Result{}, nil
	// 	// }
	// 	// //If running degraded deployment AND AllowDegradeMode is set
	// 	// if allowdegrade && persistenceStatus == statusDegradedEmptyDir && isStatefulSetAvailable(r.Client) && r.isPodRunning(false, 1, specList) {
	// 	// 	return ctrl.Result{}, nil
	// 	// }
	// 	// pvcError := setupVolume(r.Client)
	// 	// if pvcError != nil {
	// 	// 	return ctrl.Result{}, pvcError
	// 	// }
	// 	// r.executeDeployment(r.Client, instance, true, persistence)
	// 	// podReady := r.isPodRunning(true, waitSecondsForPodChk, specList)
	// 	// if podReady {
	// 	// 	//Write Status
	// 	// 	err := updateCRs(r.Client, instance, statusUsingPVC,
	// 	// 		custom, persistence, storageClass, storageSize, customValuesInuse)
	// 	// 	if err != nil {
	// 	// 		return ctrl.Result{}, err
	// 	// 	}
	// 	// }
	// 	//If Pod cannot be scheduled rollback to EmptyDir if AllowDegradeMode is set
	// 	// if !podReady && allowdegrade {
	// 	// r.Log.Info("Degrading Redisgraph deployment to use empty dir.")
	// 	// err := deleteRedisStatefulSet(r.Client)
	// 	// if err != nil {
	// 	// 	return ctrl.Result{}, err
	// 	// }
	// 	// err = deletePVC(r.Client)
	// 	// if err != nil {
	// 	// 	return ctrl.Result{}, err
	// 	// }
	// 	// r.executeDeployment(r.Client, instance, false, persistence)
	// 	// if r.isPodRunning(false, waitSecondsForPodChk, specList) {
	// 	// 	//Write Status
	// 	// 	err := updateCRs(r.Client, instance, statusDegradedEmptyDir,
	// 	// 		custom, false, "", "", customValuesInuse)
	// 	// 	if err != nil {
	// 	// 		return ctrl.Result{}, err
	// 	// 	} else {
	// 	// 		return ctrl.Result{}, nil
	// 	// 	}
	// 	// } else {
	// 	// 	r.Log.Info("Unable to create Redisgraph Deployment in Degraded Mode")
	// 	// 	//Write Status, delete statefulset and requeue
	// 	// 	r.reconcileOnError(instance, statusFailedDegraded, custom, false, "", "", customValuesInuse)
	// 	// 	// return ctrl.Result{RequeueAfter: 5 * time.Second}, fmt.Errorf(redisNotRunning)
	// 	// 	return r.requeueAfter(5*time.Second, newerr.New(redisNotRunning))

	// 	// }
	// 	// }
	// 	// if !podReady && !allowdegrade {
	// 	// 	r.Log.Info("Unable to create Redisgraph Deployment using PVC ")
	// 	// 	//Write Status, delete statefulset and requeue
	// 	// 	r.reconcileOnError(instance, statusFailedUsingPVC, custom, false, "", "", customValuesInuse)
	// 	// 	return r.requeueAfter(5*time.Second, newerr.New(redisNotRunning))
	// 	// 	// return ctrl.Result{RequeueAfter: 5 * time.Second}, fmt.Errorf(redisNotRunning)
	// 	// }
	// } else {
	// 	if isStatefulSetAvailable(r.Client) && r.isPodRunning(false, 1, specList) &&
	// 		persistenceStatus == statusNoPersistence {
	// 		return ctrl.Result{}, nil
	// 	}
	// 	r.Log.Info("Using Deployment with persistence disabled")
	// 	r.executeDeployment(r.Client, instance, false, persistence)
	// 	if r.isPodRunning(false, waitSecondsForPodChk, specList) {
	// 		//Write Status, if error - requeue
	// 		err := updateCRs(r.Client, instance, statusNoPersistence, custom, false, "", "", customValuesInuse)
	// 		if err != nil {
	// 			return ctrl.Result{}, err
	// 		}
	// 	} else {
	// 		r.Log.Info("Unable to create Redisgraph Deployment with persistence disabled")
	// 		//Write Status, delete statefulset and requeue
	// 		r.reconcileOnError(instance, statusFailedNoPersistence, custom, false, "", "", customValuesInuse)
	// 		// return ctrl.Result{RequeueAfter: 5 * time.Second}, fmt.Errorf(redisNotRunning)
	// 		return r.requeueAfter(5*time.Second, newerr.New(redisNotRunning))

	// 	}

	// }
}

func (r *SearchOperatorReconciler) reconcileOnError(instance *searchv1alpha1.SearchOperator, status string,
	custom *searchv1alpha1.SearchCustomization, persistence bool, storageClass string,
	storageSize string, customValuesInuse bool) {
	fmt.Println("In reconcileOnError: going to update CRs and deleteRedisStatefulSet")
	var err error
	if err = updateCRs(r.Client, instance, status, custom, false,
		storageClass, storageSize, customValuesInuse); err != nil {
		r.Log.Info("Error updating operator/customization status. ", "Error: ", err)
	}
	if err = deleteRedisStatefulSet(r.Client); err != nil {
		r.Log.Info("Error deleting statefulset. ", "Error: ", err)
	}
}

func (r *SearchOperatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	watchNamespace := os.Getenv("WATCH_NAMESPACE")
	fmt.Println("In SetupWithManager with watchNamespace: ", watchNamespace)
	r.Log.Info("In SetupWithManagerr")
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

func storageClassPresent(c client.Client, storageClassName string) bool {
	scList := &storagev1.StorageClassList{}
	//opts := []client.ListOption{client.MatchingFields{"metadata.annotations['storageclass.kubernetes.io/is-default-class']": "true"}}
	opts := []client.ListOption{}
	err := c.List(context.TODO(), scList, opts...)
	fmt.Println("err listing sc: ", err)
	if len(scList.Items) > 0 {
		fmt.Println("YYYY storageclass defined: ")
	} else {
		fmt.Println("XXXXX No storageclass defined")
	}
	defSCPresent := false
	for _, sclass := range scList.Items {
		if storageClassName == "" {
			if sclass.GetObjectMeta().GetAnnotations()["storageclass.kubernetes.io/is-default-class"] == "true" {
				fmt.Println("YYYY Default storageclass defined: ", sclass.Name)
				defSCPresent = true
				break
			}
		} else {
			if sclass.Name == storageClassName {
				defSCPresent = true
				fmt.Println("YYYY Expected storageclass defined: ", sclass.Name)

				break
			}
		}
	}
	if !defSCPresent {
		fmt.Println("XXXXXXX Default/Expected storageclass NOT defined")
	}
	return defSCPresent
}

func (r *SearchOperatorReconciler) getStatefulSet(cr *searchv1alpha1.SearchOperator,
	rdbVolumeSource v1.VolumeSource, saverdb string) *appv1.StatefulSet {
	resources := r.memorySettings(cr)
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
								{
									Name:  "SAVERDB",
									Value: saverdb,
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
							Resources:                resources,
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

	if (v1.VolumeSource{}) != rdbVolumeSource {
		rdbVolume := v1.Volume{
			Name:         "persist",
			VolumeSource: rdbVolumeSource,
		}
		sset.Spec.Template.Spec.Volumes = append(sset.Spec.Template.Spec.Volumes, rdbVolume)
		rdbVolumeMount := v1.VolumeMount{
			Name:      "persist",
			MountPath: "/redis-data",
		}
		for i, container := range sset.Spec.Template.Spec.Containers {
			if container.Name == "redisgraph" {
				sset.Spec.Template.Spec.Containers[i].VolumeMounts =
					append(sset.Spec.Template.Spec.Containers[i].VolumeMounts, rdbVolumeMount)
				log.Info("Added rdbVolumeMount in container: ", container.Name, rdbVolumeMount.MountPath)
			}
		}
	}

	if err := ctrl.SetControllerReference(cr, sset, r.Scheme); err != nil {
		log.Info("Cannot set statefulSet OwnerReference", err.Error())
	}
	return sset
}
func updateCRs(kclient client.Client, operatorCR *searchv1alpha1.SearchOperator, status string,
	customizationCR *searchv1alpha1.SearchCustomization, persistence bool, storageClass string,
	storageSize string, customValuesInuse bool) error {
	log.Info("------------Updating CRs------------might trigger a reconcile loop")
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
		log.Info(fmt.Sprintf("Failed to get SearchOperator %s/%s ", cr.Namespace, cr.Name), "Error: ", err.Error())
		return err
	}
	cr.Status.PersistenceStatus = status
	err = kclient.Status().Update(context.TODO(), cr)
	if err != nil {
		if apierrors.IsConflict(err) {
			log.Info("Failed to update status Object has been modified")
		}
		// log.Error(err, fmt.Sprintf("Failed to update %s/%s status ", cr.Namespace, cr.Name))
		log.Info(fmt.Sprintf("Failed to update SearchOperator %s/%s status", cr.Namespace, cr.Name), "Error: ", err.Error())

		return err
	}
	log.Info(fmt.Sprintf("Updated CR status with persistence %s  ", cr.Status.PersistenceStatus))

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

	podList, err := ftchPodList(kclient)
	for _, pod := range podList.Items {
		if pod.Name == podName {
			for _, container := range pod.Spec.Containers {
				if container.Name == "redisgraph" {
					cr.Status.RedisgraphMemoryLimit = container.Resources.Limits.Memory().String()
					cr.Status.RedisgraphMemoryRequest = container.Resources.Requests.Memory().String()
					break
				}
			}
		}
	}
	err = kclient.Status().Update(context.TODO(), cr)
	if err != nil {
		if apierrors.IsConflict(err) {
			log.Info("Failed to update status. Object has been modified")
		}
		log.Error(err, fmt.Sprintf("Failed to update %s/%s status ", cr.Namespace, cr.Name))
		return err
	}
	log.Info(fmt.Sprintf("Updated CR status with custom persistence %t ", cr.Status.Persistence))

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
			// deployment.ObjectMeta.ResourceVersion = found.ObjectMeta.ResourceVersion
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
		}
		log.Info("Created a new PVC ", logKeyPVCName, pvcName)
		return nil

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

func runningPodSpec(kclient client.Client, pod v1.Pod) StartingSpec {
	log.Info("Checking Redisgraph Pod Status...")
	//Keep checking status until waitSeconds
	// We assume its not running
	// podName := "search-redisgraph-0"
	persistence := true
	runningSpec := StartingSpec{}
	custom := &searchv1alpha1.SearchCustomization{}
	_, err := fetchSrchC(kclient, custom, namespace)
	if errors.IsNotFound(err) {
		runningSpec.allowdegrade = true
	}
	if err == nil {
		runningSpec.allowdegrade = false
	}

	// err := client.Get(context.TODO(), types.NamespacedName{Name: podName, Namespace: namespace}, pod)
	// fmt.Println("Error fetching redis pod: ", err)

	// runningCustSpec := searchv1alpha1.SearchCustomizationSpec{} //desiredSpec{}
	for _, vol := range pod.Spec.Volumes {
		if vol.Name == "persist" {
			persistence = true
			runningSpec.custSpec.Persistence = &persistence
			fmt.Println("Setting runningSpec.custSpec.Persistence to ", persistence)
			if vol.PersistentVolumeClaim != nil {
				runningSpec.withPVC = true
				if vol.PersistentVolumeClaim.ClaimName != defaultPvcName {
					runningSpec.custSpec.StorageClass = strings.TrimSuffix(vol.PersistentVolumeClaim.ClaimName, "-search-redisgraph-0")
				}
				if vol.PersistentVolumeClaim != nil && vol.PersistentVolumeClaim.ClaimName == pvcName {
					log.Info("***** RedisGraph Pod with PVC Running *****")
				}
			} else if vol.EmptyDir != nil { //TODO: Check if this is needed
				log.Info("***** RedisGraph Pod with EmptyDir Running *****")
				runningSpec.withPVC = false
				runningSpec.custSpec.StorageClass = ""
			}
			break
		}

		persistence = false
		runningSpec.custSpec.Persistence = &persistence
	}
	for _, container := range pod.Spec.Containers {
		if container.Name == "redisgraph" {
			runningSpec.custSpec.RedisgraphMemoryLimit = container.Resources.Limits.Memory().String()
			runningSpec.custSpec.RedisgraphMemoryRequest = container.Resources.Requests.Memory().String()
			break
		}
	}

	// log.Info("Redisgraph Pod not Running...")
	return runningSpec
}

func ftchPodList(kclient client.Client) (*corev1.PodList, error) {
	fmt.Println("In ftchPodList fn. Fetching redisgraph pod fresh")
	podList := &corev1.PodList{}
	opts := []client.ListOption{client.MatchingLabels{"app": appName, "component": "redisgraph"}}
	err := kclient.List(context.TODO(), podList, opts...)
	return podList, err
}
func (r *SearchOperatorReconciler) isPodRunning(withPVC bool, waitSeconds int, specList specList) (bool, error) {
	fmt.Println("^^^^^^^^^^^^^^^^waitSeconds: ", waitSeconds, " ^^^^^^^^^^^^^^^^^^")
	var err error
	log.Info("Checking Redisgraph Pod Status...")
	//Keep checking status until waitSeconds
	// We assume its not running
	count := 0
	for count < waitSeconds {
		podList, err := ftchPodList(r.Client)
		if err != nil {
			log.Info("Error listing redisgraph pods. ", err)
			return false, err
		}
		for _, item := range podList.Items {
			ready, err := r.isReady(item, withPVC, specList.startingSpec)
			if ready && (err != nil && err.Error() == noMatchSpec) {
				log.Info("In isPodRunning: Error is noMatchSpec.. Returning true")
				if count == waitSeconds {
					return false, err
				}
			}
			if ready && err == nil {
				log.Info("Redisgraph Pod Running...")
				return true, nil
			}
			// if err != nil && err.
		}
		count++
		fmt.Println("Sleeping for a second")
		time.Sleep(1 * time.Second)
		// Fetch the SearchCustomization instance
		custom := &searchv1alpha1.SearchCustomization{}
		custom, err = fetchSrchC(r.Client, custom, namespace)
		if err == nil && !reflect.DeepEqual(custom.Spec, specList.desiredSpec) {
			log.Info("******************************* SearchCustomization Spec updated , Reconciling .. *****************")
			break
		}

	}
	log.Info("Redisgraph Pod not Running...")
	return false, err
}

func (r *SearchOperatorReconciler) isReady(pod v1.Pod, withPVC bool, startingSpec StartingSpec) (bool, error) {
	fmt.Println("??????????? In isReady() function")
	for _, status := range pod.Status.Conditions {
		if status.Reason == "Unschedulable" {
			log.Info("RedisGraph Pod UnScheduleable - likely PVC mount problem")
			return false, nil
		}
	}
	fmt.Println("withPVC: ", withPVC, " startingSpec.withPVC: ", startingSpec.withPVC)

	runningSpec := StartingSpec{}
	for _, status := range pod.Status.ContainerStatuses {
		if status.Ready {

			runningSpec = runningPodSpec(r.Client, pod)
			if (*runningSpec.custSpec.Persistence == *startingSpec.custSpec.Persistence) &&
				(runningSpec.custSpec.StorageClass == startingSpec.custSpec.StorageClass) &&
				(runningSpec.custSpec.RedisgraphMemoryLimit == startingSpec.custSpec.RedisgraphMemoryLimit) &&
				(runningSpec.custSpec.RedisgraphMemoryRequest == startingSpec.custSpec.RedisgraphMemoryRequest) &&
				(runningSpec.allowdegrade == startingSpec.allowdegrade) &&
				(runningSpec.withPVC == startingSpec.withPVC) {

				fmt.Printf("Running Spec %+v: %+v:\n", *runningSpec.custSpec.Persistence, runningSpec)
				fmt.Printf("Starting Spec %+v: %+v:\n", *startingSpec.custSpec.Persistence, startingSpec)
				for _, env := range pod.Spec.Containers[0].Env {
					if !withPVC && env.Name == "SAVERDB" && env.Value == "false" {
						log.Info("PAY ATTENTION***** RedisGraph Pod Running with Persistence disabled *****")
						// return true
					}
				}
				log.Info("***** RedisGraph Pod Running Spec = startingSpec ***** - RETURNING TRUE here")
				return true, nil
			}
			log.Info("***** RedisGraph Pod Running Spec <> startingSpec *****")

			fmt.Printf("<> Running Spec %+v: %+v:\n", *runningSpec.custSpec.Persistence, runningSpec)
			fmt.Printf("<> Starting Spec %+v: %+v:\n", *startingSpec.custSpec.Persistence, startingSpec)
			log.Info(noMatchSpec)
			log.Info("***** RedisGraph Pod Running Spec <> startingSpec ***** - RETURNING FALSE here")

			return false, fmt.Errorf("%s", noMatchSpec)

		}
		fmt.Printf("Current UNREADY pod status: %+v \n", status)

	}
	if runningSpec.custSpec.Persistence != nil {
		fmt.Printf("<< Running Spec %+v: %+v:\n", *runningSpec.custSpec.Persistence, runningSpec)
		fmt.Printf("<< Starting Spec %+v: %+v:\n", *startingSpec.custSpec.Persistence, startingSpec)
	}
	return false, nil
}
func (r *SearchOperatorReconciler) memorySettings(cr *searchv1alpha1.SearchOperator) v1.ResourceRequirements {
	var memRequest resource.Quantity = resource.MustParse(cr.Spec.Redisgraph_Resource.RequestMemory)
	var memLimit resource.Quantity = resource.MustParse(cr.Spec.Redisgraph_Resource.LimitMemory)

	//Check if custCR is present
	custom := &searchv1alpha1.SearchCustomization{}
	custom, err := fetchSrchC(r.Client, custom, cr.Namespace)
	if err == nil {
		if custom.Spec.RedisgraphMemoryRequest != "" {
			memRequest = resource.MustParse(custom.Spec.RedisgraphMemoryRequest)
		}
		if custom.Spec.RedisgraphMemoryLimit != "" {
			memLimit = resource.MustParse(custom.Spec.RedisgraphMemoryLimit)
		}
	}
	return v1.ResourceRequirements{
		Limits: v1.ResourceList{
			"memory": memLimit,
		},
		Requests: v1.ResourceList{
			"cpu":    resource.MustParse(cr.Spec.Redisgraph_Resource.RequestCPU),
			"memory": memRequest,
		},
	}
}

func (r *SearchOperatorReconciler) executeDeployment(client client.Client,
	cr *searchv1alpha1.SearchOperator, usePVC bool, saverdb bool) *appv1.StatefulSet {
	var statefulSet *appv1.StatefulSet
	emptyDirVolume := v1.VolumeSource{
		EmptyDir: &v1.EmptyDirVolumeSource{},
	}
	pvcVolume := v1.VolumeSource{
		PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
			ClaimName: pvcName,
		},
	}
	if saverdb {
		if !usePVC {
			statefulSet = r.getStatefulSet(cr, emptyDirVolume, "true")
		} else {
			statefulSet = r.getStatefulSet(cr, pvcVolume, "true")
		}
	} else {
		statefulSet = r.getStatefulSet(cr, v1.VolumeSource{}, "false")
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

func fetchSrchC(kclient client.Client, custom *searchv1alpha1.SearchCustomization, namespace string) (*searchv1alpha1.SearchCustomization, error) {
	err := kclient.Get(context.TODO(), types.NamespacedName{Name: "searchcustomization", Namespace: namespace}, custom)
	return custom, err
}

func fetchSts(kclient client.Client, namespace string) (*appv1.StatefulSet, error) {

	found := &appv1.StatefulSet{}
	err := kclient.Get(context.TODO(), types.NamespacedName{Name: statefulSetName, Namespace: namespace}, found)
	return found, err
}
