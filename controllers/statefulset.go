// Copyright (c) 2022 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package controllers

import (
	"context"
	"reflect"
	"time"

	searchv1alpha1 "github.com/stolostron/search-operator/api/v1alpha1"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	redisconfigmap = "redis-conf"
)

func (r *SearchOperatorReconciler) getStatefulSet(cr *searchv1alpha1.SearchOperator,
	rdbVolumeSource corev1.VolumeSource, saverdb string) *appv1.StatefulSet {
	sset := &appv1.StatefulSet{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: statefulSetName, Namespace: namespace}, sset)
	if err != nil {
		r.Log.Info("Error fetching Statefulset")
	}
	bool := false
	metadataLabels := map[string]string{}
	metadataLabels["release"] = releaseName
	metadataLabels["component"] = component
	metadataLabels["app"] = appName
	if !compareLabels(metadataLabels, sset.Labels) {
		sset.Labels = metadataLabels
	}
	sset.ObjectMeta.Name = statefulSetName
	sset.ObjectMeta.Namespace = cr.Namespace
	sset.Spec.Replicas = int32Ptr(1)
	sset.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: map[string]string{
			"component": component,
			"app":       appName,
		},
	}
	sset.Spec.Template.ObjectMeta.Labels = metadataLabels
	sset.Spec.Template.Spec.ServiceAccountName = "search-operator"
	tol := corev1.Toleration{
		Key:      "node-role.kubernetes.io/infra",
		Effect:   corev1.TaintEffectNoSchedule,
		Operator: corev1.TolerationOpExists,
	}
	sset.Spec.Template.Spec.Tolerations = []corev1.Toleration{tol}
	pullSecret := corev1.LocalObjectReference{
		Name: cr.Spec.PullSecret,
	}
	sset.Spec.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{pullSecret}
	if sset.Spec.Template.Spec.SecurityContext != nil {
		sset.Spec.Template.Spec.SecurityContext.FSGroup = int64Ptr(redisUser)
		sset.Spec.Template.Spec.SecurityContext.RunAsUser = int64Ptr(redisUser)
	} else {
		sset.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
			FSGroup:   int64Ptr(redisUser),
			RunAsUser: int64Ptr(redisUser),
		}
	}
	sset.Spec.Template.Spec.Containers = []corev1.Container{
		{
			Name:  "redisgraph",
			Image: cr.Spec.SearchImageOverrides.Redisgraph_TLS,
			Env: []corev1.EnvVar{
				{
					Name: "REDIS_PASSWORD",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
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
			LivenessProbe: &corev1.Probe{
				InitialDelaySeconds: 10,
				TimeoutSeconds:      1,
				PeriodSeconds:       15,
				SuccessThreshold:    1,
				FailureThreshold:    3,
				ProbeHandler: corev1.ProbeHandler{
					TCPSocket: &corev1.TCPSocketAction{
						Port: intstr.FromInt(6380),
					},
				},
			},
			ReadinessProbe: &corev1.Probe{
				InitialDelaySeconds: 5,
				TimeoutSeconds:      1,
				PeriodSeconds:       15,
				SuccessThreshold:    1,
				FailureThreshold:    3,
				ProbeHandler: corev1.ProbeHandler{
					TCPSocket: &corev1.TCPSocketAction{
						Port: intstr.FromInt(6380),
					},
				},
			},
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					"memory": resource.MustParse(cr.Spec.Redisgraph_Resource.LimitMemory),
				},
				Requests: corev1.ResourceList{
					"cpu":    resource.MustParse(cr.Spec.Redisgraph_Resource.RequestCPU),
					"memory": resource.MustParse(cr.Spec.Redisgraph_Resource.RequestMemory),
				},
			},
			TerminationMessagePolicy: "File",
			TerminationMessagePath:   "/dev/termination-log",
			ImagePullPolicy:          "Always",
			SecurityContext: &corev1.SecurityContext{
				Privileged:               &bool,
				AllowPrivilegeEscalation: &bool,
			},
			VolumeMounts: []corev1.VolumeMount{
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
	}
	sset.Spec.Template.Spec.Volumes = []corev1.Volume{
		{
			Name: "stunnel-pid",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		{
			Name: "redis-graph-certs",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: "search-redisgraph-certs",
					Items: []corev1.KeyToPath{
						{
							Key:  "tls.crt",
							Path: "server.crt",
						},
						{
							Key:  "tls.key",
							Path: "server.key",
						},
					},
					DefaultMode: int32Ptr(420),
				},
			},
		},
	}

	if (corev1.VolumeSource{}) != rdbVolumeSource {
		rdbVolume := corev1.Volume{
			Name:         "persist",
			VolumeSource: rdbVolumeSource,
		}
		sset.Spec.Template.Spec.Volumes = append(sset.Spec.Template.Spec.Volumes, rdbVolume)
		rdbVolumeMount := corev1.VolumeMount{
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
	if r.rgCustomization(cr) {
		addRGConfigmap(sset)
	}
	return sset
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

func isStatefulSetAvailable(kclient client.Client) bool {
	//check if statefulset is present if not we can assume the pod is not running
	found := &appv1.StatefulSet{}
	err := kclient.Get(context.TODO(), types.NamespacedName{Name: statefulSetName, Namespace: namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		return false
	}
	return true
}

func statefulSetNeedsUpdate(client client.Client, deployment *appv1.StatefulSet) bool {
	found := &appv1.StatefulSet{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: statefulSetName, Namespace: namespace}, found)
	if err != nil {
		return true
	} else {
		if !reflect.DeepEqual(found.Spec, deployment.Spec) ||
			!reflect.DeepEqual(found.GetObjectMeta(), deployment.GetObjectMeta()) {
			log.Info("Volume source and/or metadata needs to be updated for redisgraph Statefulset")
			return true
		} else {
			log.Info("No changes required for Statefulset")
			return false
		}
	}
}
func updateRedisStatefulSet(client client.Client, deployment *appv1.StatefulSet) {
	found := &appv1.StatefulSet{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: statefulSetName, Namespace: namespace}, found)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Statefulset not found. Creating Statefulset ...")
			err = client.Create(context.TODO(), deployment)
			if err != nil {
				log.Error(err, "Failed to create Statefulset")
				return
			}
			log.Info("Statefulset created successfully")
			return
		}
		log.Error(err, "Failed to fetch Statefulset")
		return
	} else {
		deployment.ObjectMeta.ResourceVersion = found.ObjectMeta.ResourceVersion
		err = client.Update(context.TODO(), deployment)
		if err != nil {
			log.Error(err, "Failed to update Statefulset")
			return
		}
		log.Info("Volume source and/or metadata updated for redisgraph Statefulset")
		return
	}

}

func (r *SearchOperatorReconciler) rgCustomization(cr *searchv1alpha1.SearchOperator) bool {
	found := &corev1.ConfigMap{}
	err := r.Get(context.TODO(), types.NamespacedName{
		Name:      redisconfigmap,
		Namespace: cr.Namespace,
	}, found)
	if err != nil && errors.IsNotFound(err) {
		return false
	}
	return true
}

func addRGConfigmap(sset *appv1.StatefulSet) {
	cmVolume := corev1.Volume{
		Name: redisconfigmap,
		VolumeSource: v1.VolumeSource{
			ConfigMap: &v1.ConfigMapVolumeSource{
				LocalObjectReference: v1.LocalObjectReference{
					Name: redisconfigmap,
				},
			},
		},
	}
	sset.Spec.Template.Spec.Volumes = append(sset.Spec.Template.Spec.Volumes, cmVolume)
	cmVolumeMount := corev1.VolumeMount{
		Name:      redisconfigmap,
		MountPath: "/redis-config",
	}
	for i, container := range sset.Spec.Template.Spec.Containers {
		if container.Name == "redisgraph" {
			sset.Spec.Template.Spec.Containers[i].VolumeMounts =
				append(sset.Spec.Template.Spec.Containers[i].VolumeMounts, cmVolumeMount)
			log.Info("Added redisgraph custom config in container: ", container.Name, cmVolumeMount.MountPath)
		}
	}
}
