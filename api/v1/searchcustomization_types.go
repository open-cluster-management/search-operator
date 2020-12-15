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

// SearchCustomizationSpec defines the desired state of SearchCustomization
type SearchCustomizationSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// If specified this storage class is used, else default storage class will be
	//used by kubernetes. If storageclass is specified, persistence must be set to true.
	// +optional
	StorageClass string `json:"storageClass,omitempty"`

	// Size of the PVC which will be used by search-redisgraph for persistence
	// +optional
	StorageSize string `json:"storageSize,omitempty"`

	// If true, then a PVC is created on the stroageclass provided (if none specified ,
	//default storageclass)is used to persist redis data .
	// +optional
	Persistence *bool `json:"persistence,omitempty"`

	// If true, then whenever PVC cannot be Bound to search-redisgraph
	//pod then controller automatically sets up EmptyDir volume for search-redisgraph
	// +optional
	FallbackToEmptyDir *bool `json:"fallbackToEmptyDir,omitempty"`
}

// SearchCustomizationStatus defines the observed state of SearchCustomization
type SearchCustomizationStatus struct {
	StorageClass string `json:"storageClass"`

	StorageSize string `json:"storageSize"`

	Persistence bool `json:"persistence"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// SearchCustomization is the Schema for the searchcustomizations API
type SearchCustomization struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SearchCustomizationSpec   `json:"spec,omitempty"`
	Status SearchCustomizationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SearchCustomizationList contains a list of SearchCustomization
type SearchCustomizationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SearchCustomization `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SearchCustomization{}, &SearchCustomizationList{})
}
