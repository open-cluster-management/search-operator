// Copyright (c) 2020 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package controllers

import (
	"context"
	"fmt"

	searchv1alpha1 "github.com/stolostron/search-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// compareLabels compares two map[string]string structs
// Returns false if all key-value pairs in first map is not in second map
// Else returns true
// Used to check if all the expected labels are present in the current running statefulset
func compareLabels(metadataLabels, ssetLabels map[string]string) bool {
	allLabelsPresent := true
	for label, value := range metadataLabels {
		ssetVal, labelPresent := ssetLabels[label]
		if labelPresent && (ssetVal == value) {
			continue
		} else {
			allLabelsPresent = false
			log.Info("Not all labels present in statefulset. Label: ", label, "not found in metadata")
			break
		}
	}
	return allLabelsPresent
}

func getOptions(opts map[string]string) []client.ListOption {
	listOptions := []client.ListOption{}
	listOption := client.ListOptions{
		LabelSelector: labels.SelectorFromSet(opts),
		Namespace:     namespace,
		Limit:         2, //setting this to 2 as a safety net while deleting pods
	}
	listOptions = append(listOptions, &listOption)
	return listOptions
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
	log.Info("Updated status in CRs successfully.")
	return nil
}

func fetchSrchOperator(kclient client.Client, cr *searchv1alpha1.SearchOperator) (
	*searchv1alpha1.SearchOperator, error) {
	found := &searchv1alpha1.SearchOperator{}
	err := kclient.Get(context.TODO(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, found)
	return found, err
}
func fetchSrchCustomization(kclient client.Client, cr *searchv1alpha1.SearchCustomization) (
	*searchv1alpha1.SearchCustomization, error) {
	found := &searchv1alpha1.SearchCustomization{}
	err := kclient.Get(context.TODO(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, found)
	return found, err
}
func updateOperatorCR(kclient client.Client, cr *searchv1alpha1.SearchOperator, status string) error {
	cr, err := fetchSrchOperator(kclient, cr)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to get SearchOperator %s/%s ", cr.Namespace, cr.Name))
		return err
	}
	cr.Status.PersistenceStatus = status
	if deployVarPresent && deployVarErr == nil {
		cr.Status.DeployRedisgraph = &deploy
	}
	err = kclient.Status().Update(context.TODO(), cr)
	if err != nil {
		if errors.IsConflict(err) {
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
	cr, err := fetchSrchCustomization(kclient, cr)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to get SearchCustomization %s/%s ", cr.Namespace, cr.Name))
		return err
	}
	cr.Status.Persistence = persistence
	cr.Status.StorageClass = storageClass
	cr.Status.StorageSize = storageSize
	err = kclient.Status().Update(context.TODO(), cr)
	if err != nil {
		if errors.IsConflict(err) {
			log.Info("Failed to update status Object has been modified")
		}
		log.Info(fmt.Sprintf("Failed to update %s/%s status. Error:  %s", cr.Namespace, cr.Name, err.Error()))
		return err
	} else {
		log.Info(fmt.Sprintf("Updated CR status with custom persistence %t ", cr.Status.Persistence))
	}
	return nil
}
