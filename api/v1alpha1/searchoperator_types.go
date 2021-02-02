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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SearchOperatorSpec defines the desired state of SearchOperator
type SearchOperatorSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Image to use in deployment
	SearchImageOverrides ImageOverrides `json:"searchimageoverrides"`

	Redisgraph_Resource PodResource `json:"redisgraph_resource"`

	PullPolicy string `json:"pullpolicy,omitempty"`

	PullSecret string `json:"pullsecret,omitempty"`

	// NodeSelector causes all components to be scheduled on nodes with matching labels.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
}

// SearchOperatorStatus defines the observed state of SearchOperator
type SearchOperatorStatus struct {
	// Reflects the current status of the RedisGraph pod using a Persistence mode (PVC/EmptyDir/Degraded)
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

type PodResource struct {
	// Request memory
	RequestMemory string `json:"request_memory"`
	// Request CPU
	RequestCPU string `json:"request_cpu"`
	// Limit Memory
	LimitMemory string `json:"limit_memory"`
	// Limit CPU
	LimitCPU string `json:"limit_cpu,omitempty"`
}

type ImageOverrides struct {
	Redisgraph_TLS    string `json:"redisgraph_tls"`
	Search_Aggregator string `json:"search_aggregator,omitempty"`
	Search_API        string `json:"search_api,omitempty"`
	Search_Collector  string `json:"search_collector,omitempty"`
}

func init() {
	SchemeBuilder.Register(&SearchOperator{}, &SearchOperatorList{})
}
