// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package controllers

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-logr/logr"
	searchv1alpha1 "github.com/open-cluster-management/search-operator/api/v1alpha1"
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
	statusNoPersistence       = "Redisgraph pod running with persistence disabled"
	redisUser                 = int64(10001)
	defaultPvcName            = "search-redisgraph-pvc-0"
)

var (
	pvcName              = "search-redisgraph-pvc-0"
	waitSecondsForPodChk = 180 //Wait for 3 minutes
	log                  = logf.Log.WithName("searchoperator")
	persistence          = true
	allowdegrade         = true
	storageClass         = ""
	storageSize          = "10Gi"
	namespace            = os.Getenv("WATCH_NAMESPACE")
	//Keeping these here as the pod will restart everytime when ENV is updated and we will read the updated values
	deployRedisgraphPod, deployVarPresent = os.LookupEnv("DEPLOY_REDISGRAPH")
	deploy, deployVarErr                  = strconv.ParseBool(deployRedisgraphPod)
)
var startingSpec searchv1alpha1.SearchCustomizationSpec

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
			pvcName = defaultPvcName
			startingSpec = searchv1alpha1.SearchCustomizationSpec{}
		} else {
			return ctrl.Result{}, err
		}

	} else {
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
		if custom.Spec.StorageClass != "" {
			storageClass = custom.Spec.StorageClass
			pvcName = storageClass + "-search-redisgraph-0"
		}
		if custom.Spec.StorageSize != "" {
			storageSize = custom.Spec.StorageSize
		}
		//set the  user provided values
		customValuesInuse = true
		startingSpec = custom.Spec
		r.Log.Info(fmt.Sprintf("Storage %s", storageSize))
	}

	r.Log.Info("Checking if customization CR is created..", " Custom Values In use? ", customValuesInuse)
	r.Log.Info("Values in use: ", "persistence? ", persistence, " storageClass? ", storageClass,
		" storageSize? ", storageSize, " fallbackToEmptyDir? ", allowdegrade)

	// Create secret if not found
	err = r.setupSecret(r.Client, instance)
	if err != nil {
		// Error setting up secret - requeue the request.
		return ctrl.Result{}, err
	}

	//Read the searchoperator status
	persistenceStatus := instance.Status.PersistenceStatus

	// Setup RedisGraph Deployment
	r.Log.Info(fmt.Sprintf("Config in  Use Persistence/AllowDegrade %t/%t", persistence, allowdegrade))
	//if deploy env variable is false, don't deploy Redisgraph pod
	if deployVarPresent && deployVarErr == nil && !deploy {
		err := deleteRedisStatefulSet(r.Client) //if redisgraph pod is already deployed, delete it.
		if err != nil {
			return ctrl.Result{}, err
		}
		r.Log.Info(`Not deploying the database. This is not an error, it's a current limitation in this environment.
	The search feature is not operational.  More info: https://github.com/open-cluster-management/community/issues/34`)
		//Write Status
		err = updateCRs(r.Client, instance, redisNotRunning,
			custom, persistence, storageClass, storageSize, customValuesInuse)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	if persistence {
		//If running PVC deployment nothing to do
		if persistenceStatus == statusUsingPVC && isStatefulSetAvailable(r.Client) && r.isPodRunning(true, 1) {
			return ctrl.Result{}, nil
		}
		//If running degraded deployment AND AllowDegradeMode is set
		if allowdegrade && persistenceStatus == statusDegradedEmptyDir && isStatefulSetAvailable(r.Client) && r.isPodRunning(false, 1) {
			return ctrl.Result{}, nil
		}
		pvcError := setupVolume(r.Client)
		if pvcError != nil {
			return ctrl.Result{}, pvcError
		}
		r.executeDeployment(r.Client, instance, true, persistence)
		podReady := r.isPodRunning(true, waitSecondsForPodChk)
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
			r.executeDeployment(r.Client, instance, false, persistence)
			if r.isPodRunning(false, waitSecondsForPodChk) {
				//Write Status
				err := updateCRs(r.Client, instance, statusDegradedEmptyDir,
					custom, false, "", "", customValuesInuse)
				if err != nil {
					return ctrl.Result{}, err
				} else {
					return ctrl.Result{}, nil
				}
			} else {
				r.Log.Info("Unable to create Redisgraph Deployment in Degraded Mode")
				//Write Status, delete statefulset and requeue
				r.reconcileOnError(instance, statusFailedDegraded, custom, false, "", "", customValuesInuse)
				return ctrl.Result{RequeueAfter: 5 * time.Second}, fmt.Errorf(redisNotRunning)
			}
		}
		if !podReady && !allowdegrade {
			r.Log.Info("Unable to create Redisgraph Deployment using PVC ")
			//Write Status, delete statefulset and requeue
			r.reconcileOnError(instance, statusFailedUsingPVC, custom, false, "", "", customValuesInuse)
			return ctrl.Result{RequeueAfter: 5 * time.Second}, fmt.Errorf(redisNotRunning)
		}
	} else {
		if isStatefulSetAvailable(r.Client) && r.isPodRunning(false, 1) &&
			persistenceStatus == statusNoPersistence {
			return ctrl.Result{}, nil
		}
		r.Log.Info("Using Deployment with persistence disabled")
		r.executeDeployment(r.Client, instance, false, persistence)
		if r.isPodRunning(false, waitSecondsForPodChk) {
			//Write Status, if error - requeue
			err := updateCRs(r.Client, instance, statusNoPersistence, custom, false, "", "", customValuesInuse)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else {
			r.Log.Info("Unable to create Redisgraph Deployment with persistence disabled")
			//Write Status, delete statefulset and requeue
			r.reconcileOnError(instance, statusFailedNoPersistence, custom, false, "", "", customValuesInuse)
			return ctrl.Result{RequeueAfter: 5 * time.Second}, fmt.Errorf(redisNotRunning)
		}

	}
	return ctrl.Result{}, nil
}

func (r *SearchOperatorReconciler) reconcileOnError(instance *searchv1alpha1.SearchOperator, status string,
	custom *searchv1alpha1.SearchCustomization, persistence bool, storageClass string,
	storageSize string, customValuesInuse bool) {
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

func (r *SearchOperatorReconciler) getStatefulSet(cr *searchv1alpha1.SearchOperator,
	rdbVolumeSource v1.VolumeSource, saverdb string) *appv1.StatefulSet {
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
					Tolerations: []v1.Toleration{{
						Key:      "node-role.kubernetes.io/infra",
						Effect:   v1.TaintEffectNoSchedule,
						Operator: v1.TolerationOpExists,
					}},
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
	if cr.Spec.NodeSelector != nil {
		sset.Spec.Template.Spec.NodeSelector = cr.Spec.NodeSelector
		log.Info("Added Node Selector")
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
	if cr.Status.PreviousState == nil {
		cr.Status.PreviousState = &deploy
	} else if cr.Status.PreviousState != nil {
		if *cr.Status.PreviousState == deploy {
			updState := !deploy
			cr.Status.PreviousState = &updState
		}
	}
	err = kclient.Status().Update(context.TODO(), cr)
	if err != nil {
		if apierrors.IsConflict(err) {
			log.Info("Failed to update status Object has been modified")
		}
		log.Info(fmt.Sprintf("Failed to update %s/%s status. Error: %s", cr.Namespace, cr.Name, err.Error()))
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
		log.Info(fmt.Sprintf("Failed to update %s/%s status. Error:  %s", cr.Namespace, cr.Name, err.Error()))
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

func (r *SearchOperatorReconciler) isPodRunning(withPVC bool, waitSeconds int) bool {
	log.Info("Checking Redisgraph Pod Status...")
	//Keep checking status until waitSeconds
	// We assume its not running
	count := 0
	for count < waitSeconds {
		podList := &corev1.PodList{}
		opts := []client.ListOption{client.MatchingLabels{"app": appName, "component": "redisgraph"}}
		err := r.Client.List(context.TODO(), podList, opts...)
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
		// Fetch the SearchCustomization instance
		custom := &searchv1alpha1.SearchCustomization{}
		err = r.Client.Get(context.TODO(), types.NamespacedName{Name: "searchcustomization", Namespace: namespace}, custom)
		if err == nil && !reflect.DeepEqual(custom.Spec, startingSpec) {
			log.Info("SearchCustomization Spec updated , Reconciling ..")
			break
		}

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
			for _, env := range pod.Spec.Containers[0].Env {
				if !withPVC && env.Name == "SAVERDB" && env.Value == "false" {
					log.Info("RedisGraph Pod Running with Persistence disabled")
					return true
				}
			}
			for _, name := range pod.Spec.Volumes {
				if name.Name != "persist" {
					continue
				}
				if withPVC && name.PersistentVolumeClaim != nil && name.PersistentVolumeClaim.ClaimName == pvcName {
					log.Info("RedisGraph Pod with PVC Running")
					return true
				} else if !withPVC && name.PersistentVolumeClaim == nil {
					log.Info("RedisGraph Pod with EmptyDir Running")
					return true
				}
			}
		}
	}
	return false
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
