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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SearchCustomizationSpec defines the desired state of SearchCustomization properties.
type SearchCustomizationSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// If specified this storageClass is used, otherwise the default storageClass
	// is used by Kubernetes. If storageClass is specified, persistence must be set to true.
	// +optional
	StorageClass string `json:"storageClass,omitempty"`

	// Size of the PVC which is used by search-redisgraph pod.
	// +optional
	StorageSize string `json:"storageSize,omitempty"`

	// If set to true, then a PVC is created on the storageClass that is provided.
	// If there is no storageClass specified, default storageClass is used to persist Redisgraph data.
	// +optional
	Persistence *bool `json:"persistence,omitempty"`
}

// SearchCustomizationStatus defines the observed state of SearchCustomization.
type SearchCustomizationStatus struct {
	StorageClass string `json:"storageClass"`

	StorageSize string `json:"storageSize"`

	Persistence bool `json:"persistence"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// SearchCustomization is the schema for the search customizations API.
type SearchCustomization struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SearchCustomizationSpec   `json:"spec,omitempty"`
	Status SearchCustomizationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SearchCustomizationList contains a list of SearchCustomization.
type SearchCustomizationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SearchCustomization `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SearchCustomization{}, &SearchCustomizationList{})
}
