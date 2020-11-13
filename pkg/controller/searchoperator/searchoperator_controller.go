package searchoperator

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"reflect"
	"time"

	searchv1alpha1 "github.com/open-cluster-management/search-operator/pkg/apis/search/v1alpha1"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_searchoperator")

const pvcName = "redisgraph-pvc"

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new SearchOperator Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileSearchOperator{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("searchoperator-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource SearchOperator
	err = c.Watch(&source.Kind{Type: &searchv1alpha1.SearchOperator{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner SearchOperator
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &searchv1alpha1.SearchOperator{},
	})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileSearchOperator implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileSearchOperator{}

// ReconcileSearchOperator reconciles a SearchOperator object
type ReconcileSearchOperator struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a SearchOperator object and makes changes based on the state read
// and what is in the SearchOperator.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileSearchOperator) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling SearchOperator")

	// Fetch the SearchOperator instance
	instance := &searchv1alpha1.SearchOperator{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// Define a new Secret object
	secret := newRedisSecret(instance)

	// Set SearchService instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, secret, r.scheme); err != nil {
		return reconcile.Result{}, err
	}

	// Check if this Secret already exists
	found := &corev1.Secret{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new Secret", "Secret.Namespace", secret.Namespace, "Secret.Name", secret.Name)
		err = r.client.Create(context.TODO(), secret)
		if err != nil {
			return reconcile.Result{}, err
		}
		// Secret created successfully - don't requeue
		//return reconcile.Result{}, nil
	} else if err != nil {
		return reconcile.Result{}, err
	}

	// Secret already exists - don't requeue
	reqLogger.Info("Skip reconcile: Secret already exists", "Secret.Namespace", found.Namespace, "Secret.Name", found.Name)

	// Setup RedisGraph Deployment

	if !instance.Spec.Persistence {
		reqLogger.Info("Creating Empty dir Deployment")
		executeDeployment(r.client, instance, false, r.scheme)
		//Write Status
		updateCR(r.client, instance, "Node level persistence using EmptyDir")

	} else {
		setupVolume(r.client, instance)
		reqLogger.Info("Creating PVC Deployment with  ", instance.Spec.StorageClass, instance.Spec.StorageSize)
		executeDeployment(r.client, instance, true, r.scheme)
		//Write Status
		updateCR(r.client, instance, "Persistence using PersistenceVolumeClaim")
		//If Pod cannot be scheduled rollback to EmptyDir
		if !podScheduled(r.client, instance) {
			reqLogger.Info("Degrading to  Empty dir Deployment")
			executeDeployment(r.client, instance, false, r.scheme)
			//Write Status
			updateCR(r.client, instance, "Degraded mode using EmptyDir. Unable to use PersistenceVolumeClaim")
		}
	}

	return reconcile.Result{}, nil
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

func updateCR(kclient client.Client, cr *searchv1alpha1.SearchOperator, status string) {
	pvcLogger := log.WithValues("Request.Namespace", cr.Namespace)
	found := &searchv1alpha1.SearchOperator{}
	err := kclient.Get(context.TODO(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, found)
	if err != nil {
		pvcLogger.Error(err, fmt.Sprintf("Failed to get SearchOperator %s/%s ", cr.Namespace, cr.Name))
	}
	cr.Status.PersistenceStatus = status
	if status == "Degraded mode using EmptyDir. Unable to use PersistenceVolumeClaim" {
		cr.Spec.Persistence = false
		err := kclient.Update(context.TODO(), cr)
		if err != nil {
			pvcLogger.Error(err, fmt.Sprintf("Failed to update SearchOperator %s/%s  ", cr.Namespace, cr.Name))
			return
		}
	}
	err = kclient.Status().Update(context.TODO(), cr)
	if err != nil {
		if apierrors.IsConflict(err) {
			pvcLogger.Info("Failed to update status Object has been modified")
		}
		pvcLogger.Error(err, fmt.Sprintf("Failed to update %s/%s status ", cr.Namespace, cr.Name))

	}
	pvcLogger.Info(status, "set status")
	time.Sleep(5 * time.Second)
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
		if !reflect.DeepEqual(found.Spec.Template.Spec.Volumes, deployment.Spec.Template.Spec.Volumes) {
			deployment.ObjectMeta.ResourceVersion = found.ObjectMeta.ResourceVersion
			err = client.Update(context.TODO(), deployment)
			if err != nil {
				log.Error(err, "Failed to update  deployment")
				return
			}
			log.Info("Updated  redisgraph deployment ")
		}
	}
}

func getPVC(cr *searchv1alpha1.SearchOperator) *v1.PersistentVolumeClaim {
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

//Remove PVC if you have one
func setupVolume(client client.Client, cr *searchv1alpha1.SearchOperator) {
	pvcLogger := log.WithValues("Request.Namespace", cr.Namespace)
	found := &v1.PersistentVolumeClaim{}
	pvc := getPVC(cr)
	err := client.Get(context.TODO(), types.NamespacedName{Name: pvcName, Namespace: cr.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		pvcLogger.Info("Creating a new PVC", "PVC.Namespace", cr.Namespace, "PVC.Name", pvcName)
		err = client.Create(context.TODO(), pvc)
		//Return True if sucessfully created pvc else return False
		if err != nil {
			pvcLogger.Info("Error Creating a new PVC ", "PVC.Namespace", cr.Namespace, "PVC.Name", pvcName)
			pvcLogger.Info(err.Error())
			return
		} else {
			pvcLogger.Info("Created a new PVC ", "PVC.Namespace", cr.Namespace, "PVC.Name", pvcName)
			return
		}
	} else if err != nil {
		pvcLogger.Info("Error finding  PVC ", "PVC.Namespace", cr.Namespace, "PVC.Name", pvcName)
		//return False and error if there is Error
		return
	}
	pvcLogger.Info("Using existing PVC")
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

func podScheduled(kclient client.Client, cr *searchv1alpha1.SearchOperator) bool {
	pvcLogger := log.WithValues("Request.Namespace", cr.Namespace)
	podList := &corev1.PodList{}
	opts := []client.ListOption{client.MatchingLabels{"component": "redisgraph"}}
	//Check pod in 5 seconds
	time.Sleep(5 * time.Second)
	err := kclient.List(context.TODO(), podList, opts...)
	if err != nil {
		return false
	}
	//Keep checking status for 4 minutes , if its not running
	// We assume its not running
	count := 0
	for count < 240 {
		for _, item := range podList.Items {
			for _, status := range item.Status.Conditions {
				if status.Reason == "Unschedulable" {
					pvcLogger.Info("RedisGraph Pod UnScheduleable - likely PVC mount problem")
					return false
				}
			}
			for _, status := range item.Status.ContainerStatuses {
				if status.Ready {
					for _, name := range item.Spec.Volumes {
						if name.PersistentVolumeClaim != nil && name.PersistentVolumeClaim.ClaimName == pvcName {
							pvcLogger.Info("RedisGraph Pod Ready")
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

func executeDeployment(client client.Client, cr *searchv1alpha1.SearchOperator, usePVC bool,
	scheme *runtime.Scheme) *appv1.Deployment {
	pvcLogger := log.WithValues("Request.Namespace", cr.Namespace)
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
		pvcLogger.Info("Cannot set deployment OwnerReference", err.Error())
	}
	updateRedisDeployment(client, deployment, cr.Namespace)
	return deployment
}
