package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SearchOperatorSpec defines the desired state of SearchOperator
type SearchOperatorSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html

	// +optional
	StorageClass string `json:"storageclass,omitempty"`
	// +optional
	StorageSize string `json:"storagesize,omitempty"`
	// +optional
	Persistence bool `json:"persistence,omitempty"`
	// Image to use in deployment
	RedisgraphImage string `json:"redisgraph_image"`
	// Request memory
	RequestMemory string `json:"request_memory"`
	// Request CPU
	RequestCPU string `json:"request_cpu"`
	// Limit Memory
	LimitMemory string `json:"limit_memory"`
	// Limit CPU
	LimitCPU string `json:"limit_cpu"`

	PullPolicy string `json:"pullpolicy"`

	PullSecret string `json:"pullsecret"`

	NodeSelector        bool   `json:"nodeselector,omitempty"`
	NodeSelectorOs      string `json:"nodeselectoros,omitempty"`
	CustomLabelSelector string `json:"customlabelselector,omitempty"`
	CustomLabelValue    string `json:"customlabelvalue,omitempty"`
}

// SearchOperatorStatus defines the observed state of SearchOperator
type SearchOperatorStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html

	PersistenceStatus string `json:"persistence"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SearchOperator is the Schema for the searchoperators API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=searchoperators,scope=Namespaced
type SearchOperator struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SearchOperatorSpec   `json:"spec,omitempty"`
	Status SearchOperatorStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SearchOperatorList contains a list of SearchOperator
type SearchOperatorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SearchOperator `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SearchOperator{}, &SearchOperatorList{})
}
