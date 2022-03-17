// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package controllers

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	searchv1alpha1 "github.com/stolostron/search-operator/api/v1alpha1"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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
	statusUpdateError         = "Error updating operator/customization status. "
	errorLogStr               = "Error: "
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
	releaseName          = os.Getenv("RELEASE_NAME")
	//Keeping these here as the pod will restart everytime when ENV is updated and we will read the updated values
	deployRedisgraphPod, deployVarPresent = os.LookupEnv("DEPLOY_REDISGRAPH")
	deploy, deployVarErr                  = strconv.ParseBool(deployRedisgraphPod)
)
var startingSpec searchv1alpha1.SearchCustomizationSpec
var deployStatus bool

func (r *SearchOperatorReconciler) Reconcile(con context.Context, req ctrl.Request) (ctrl.Result, error) {

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
		// Allowdegrade mode helps the user to set the controller from switching back to emptydir
		// and debug users configuration
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
		if err = updateCRs(r.Client, instance, redisNotRunning,
			custom, persistence, storageClass, storageSize, customValuesInuse); err != nil {
			r.Log.Info(statusUpdateError, errorLogStr, err)
		}
		return ctrl.Result{}, err
	}

	//Read the searchoperator status
	persistenceStatus := instance.Status.PersistenceStatus
	if instance.Status.DeployRedisgraph != nil {
		deployStatus = *instance.Status.DeployRedisgraph
	}
	// Setup RedisGraph Deployment
	r.Log.Info(fmt.Sprintf("Config in  Use Persistence/AllowDegrade %t/%t", persistence, allowdegrade))
	//if deploy env variable is false, don't deploy Redisgraph pod
	if deployVarPresent && deployVarErr == nil && !deploy {
		err := deleteRedisStatefulSet(r.Client) //if redisgraph pod is already deployed, delete it.
		if err != nil {
			if err = updateCRs(r.Client, instance, redisNotRunning,
				custom, persistence, storageClass, storageSize, customValuesInuse); err != nil {
				r.Log.Info(statusUpdateError, errorLogStr, err)
			}
			return ctrl.Result{}, err
		}
		r.Log.Info(`Not deploying the database. This is not an error, it's a current limitation in this environment.
	The search feature is not operational.  More info: https://github.com/open-cluster-management-io/community/issues/34`)
		//Write Status
		err = updateCRs(r.Client, instance, redisNotRunning,
			custom, persistence, storageClass, storageSize, customValuesInuse)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	if persistence {
		expectedSts := r.expectedStatefulSet(r.Client,
			instance, true, persistence)
		//If running PVC deployment nothing to do
		// Do nothing if status (persistence and deploy) in searchoperator is up-to-date with statusUsingPVC
		// and statefulset is available and up-to-date
		// and pod is running with PVC volume
		if persistenceStatus == statusUsingPVC && deployStatus == deploy && isStatefulSetAvailable(r.Client) &&
			!statefulSetNeedsUpdate(r.Client, expectedSts) && r.isPodRunning(true, 1) {
			r.Log.Info("Redisgraph Pod running successfully with PVC.")
			return ctrl.Result{}, nil
		}
		expectedSts = r.expectedStatefulSet(r.Client,
			instance, false, persistence)
		//If running degraded deployment AND AllowDegradeMode is set
		// Do nothing if status (persistence and deploy) in searchoperator is up-to-date with statusDegradedEmptyDir
		// and statefulset is available and up-to-date
		// and pod is running with emptyDir volume
		if allowdegrade && persistenceStatus == statusDegradedEmptyDir &&
			deployStatus == deploy && isStatefulSetAvailable(r.Client) &&
			!statefulSetNeedsUpdate(r.Client, expectedSts) && r.isPodRunning(false, 1) {
			r.Log.Info("Redisgraph Pod running successfully with EmptyDir.")
			return ctrl.Result{}, nil
		}
		//Restart search-collector pod while setting up Redisgraph pod
		if deployVarPresent && deployVarErr == nil && deploy {
			srchOp, err := fetchSrchOperator(r.Client, instance)
			//If Redisgraph was disabled, collector will be in a 10 minute timeout loop.
			//Restart collector and api pods while deploying Redisgraph.
			if err == nil && srchOp.Status.DeployRedisgraph != nil && *srchOp.Status.DeployRedisgraph == false {
				r.Log.Info("Restarting search-collector and search-api pods")
				//restart collector and api pods
				r.restartSearchComponents()
			}
		}
		pvcError := setupVolume(r.Client)
		if pvcError != nil {
			if err = updateCRs(r.Client, instance, redisNotRunning,
				custom, persistence, storageClass, storageSize, customValuesInuse); err != nil {
				r.Log.Info(statusUpdateError, errorLogStr, err)
			}
			return ctrl.Result{}, pvcError
		}
		r.Log.Info("PVC volume set up successfully")
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
				if err = updateCRs(r.Client, instance, redisNotRunning,
					custom, persistence, storageClass, storageSize, customValuesInuse); err != nil {
					r.Log.Info(statusUpdateError, errorLogStr, err)
				}
				return ctrl.Result{}, err
			}
			r.Log.Info("Deleted statefulset to move to emptyDir")
			err = deletePVC(r.Client)
			if err != nil {
				if err = updateCRs(r.Client, instance, redisNotRunning,
					custom, persistence, storageClass, storageSize, customValuesInuse); err != nil {
					r.Log.Info(statusUpdateError, errorLogStr, err)
				}
				return ctrl.Result{}, err
			}
			r.Log.Info("Deleted PVC to move to emptyDir")
			r.executeDeployment(r.Client, instance, false, persistence)
			if r.isPodRunning(false, waitSecondsForPodChk) {
				r.Log.Info("Pod set up and running successfully with emptyDir. Updating status...")
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
			persistenceStatus == statusNoPersistence && deployStatus == deploy {
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
		r.Log.Info(statusUpdateError, errorLogStr, err)
	}
	if err = deleteRedisStatefulSet(r.Client); err != nil {
		r.Log.Info("Error deleting statefulset. ", errorLogStr, err)
	}
}

// Restart search collector and api pods
func (r *SearchOperatorReconciler) restartSearchComponents() {
	allComponents := map[string]map[string]string{}
	allComponents["Search-collector"] = map[string]string{"app": "search-prod", "component": "search-collector"}
	allComponents["Search-api"] = map[string]string{"app": "search", "component": "search-api"}

	for compName, compLabels := range allComponents {
		opts := getOptions(compLabels)
		podList := &corev1.PodList{}
		err := r.Client.List(context.TODO(), podList, opts...)
		if err != nil || len(podList.Items) == 0 {
			if len(podList.Items) == 0 {
				r.Log.Info(fmt.Sprintf("Failed to find %s pods. %d pods found", compName, len(podList.Items)))
			}
			r.Log.Info(fmt.Sprintf("Error listing pods for component: %s", compName), "Err:", err)
			continue
		}

		for _, item := range podList.Items {
			compPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      item.Name,
					Namespace: item.Namespace,
				},
			}
			err := r.Client.Delete(context.TODO(), compPod)
			if err != nil && !errors.IsNotFound(err) {
				r.Log.Info("Failed to delete pods for ", "component", compName)
				r.Log.Info(err.Error())
				// Not needed to act on the error as restarting is to offset the timeout
				// search will continue to function
				continue
			}
			r.Log.Info(fmt.Sprintf("%s pod deleted. Namespace/Name: %s/%s", compName, item.Namespace, item.Name))
		}
	}
}

func (r *SearchOperatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	watchNamespace := os.Getenv("WATCH_NAMESPACE")
	pred := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return e.Object.GetNamespace() == watchNamespace
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.ObjectNew.GetNamespace() == watchNamespace &&
				e.ObjectNew.GetGeneration() != e.ObjectOld.GetGeneration() {
				return true
			}
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			if e.Object.GetNamespace() == watchNamespace {
				return !e.DeleteStateUnknown
			}
			return false
		},
	}

	searchCustomizationFn := handler.MapFunc(
		func(a client.Object) []reconcile.Request {
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
		Watches(&source.Kind{Type: &searchv1alpha1.SearchCustomization{}}, handler.EnqueueRequestsFromMapFunc(searchCustomizationFn)).
		WithEventFilter(pred).Complete(r)
}

func int32Ptr(i int32) *int32 { return &i }

func int64Ptr(i int64) *int64 { return &i }

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

func isReady(pod corev1.Pod, withPVC bool) bool {
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

func (r *SearchOperatorReconciler) expectedStatefulSet(client client.Client,
	cr *searchv1alpha1.SearchOperator, usePVC bool, saverdb bool) *appv1.StatefulSet {
	var statefulSet *appv1.StatefulSet
	emptyDirVolume := corev1.VolumeSource{
		EmptyDir: &corev1.EmptyDirVolumeSource{},
	}
	pvcVolume := corev1.VolumeSource{
		PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
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
		statefulSet = r.getStatefulSet(cr, corev1.VolumeSource{}, "false")
	}
	return statefulSet
}

func (r *SearchOperatorReconciler) executeDeployment(client client.Client,
	cr *searchv1alpha1.SearchOperator, usePVC bool, saverdb bool) *appv1.StatefulSet {
	statefulSet := r.expectedStatefulSet(client, cr, usePVC, saverdb)
	if statefulSetNeedsUpdate(client, statefulSet) {
		updateRedisStatefulSet(client, statefulSet)
	}
	log.Info("No updates required for Statefulset")
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
