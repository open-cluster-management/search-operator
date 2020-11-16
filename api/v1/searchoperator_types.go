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

// SearchOperatorSpec defines the desired state of SearchOperator
type SearchOperatorSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +optional
	StorageClass string `json:"storageclass,omitempty"`
	// +optional
	StorageSize string `json:"storagesize,omitempty"`

	Persistence bool `json:"persistence"`

	// +optional
	Degraded bool `json:"degraded,omitempty"`
	// Image to use in deployment
	RedisgraphImage string `json:"redisgraph_image,omitempty"`
	// Request memory
	RequestMemory string `json:"request_memory,omitempty"`
	// Request CPU
	RequestCPU string `json:"request_cpu,omitempty"`
	// Limit Memory
	LimitMemory string `json:"limit_memory,omitempty"`
	// Limit CPU
	LimitCPU string `json:"limit_cpu,omitempty"`

	PullPolicy string `json:"pullpolicy,omitempty"`

	PullSecret string `json:"pullsecret,omitempty"`

	NodeSelector        bool   `json:"nodeselector,omitempty"`
	NodeSelectorOs      string `json:"nodeselectoros,omitempty"`
	CustomLabelSelector string `json:"customlabelselector,omitempty"`
	CustomLabelValue    string `json:"customlabelvalue,omitempty"`
}

// SearchOperatorStatus defines the observed state of SearchOperator
type SearchOperatorStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	PersistenceStatus string `json:"persistence"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// SearchOperator is the Schema for the searchoperators API
type SearchOperator struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SearchOperatorSpec   `json:"spec,omitempty"`
	Status SearchOperatorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SearchOperatorList contains a list of SearchOperator
type SearchOperatorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SearchOperator `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SearchOperator{}, &SearchOperatorList{})
}
