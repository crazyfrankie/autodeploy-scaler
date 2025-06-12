/*
Copyright 2025.

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

const (
	SCALED   = "Scaled"
	FAILED   = "Failed"
	PENDING  = "Pending"
	RESTORED = "Restored"
)

type DeploymentInfo struct {
	Replicas  int32  `json:"replicas,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

// ScalerSpec defines the desired state of Scaler.
type ScalerSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:Maximum=23
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Required
	StartTime int `json:"startTime,omitempty"`
	// +kubebuilder:validation:Maximum=24
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Required
	EndTime    int              `json:"endTime,omitempty"`
	Replicas   int32            `json:"replicas,omitempty"`
	Deployment []DeploymentSpec `json:"deployment,omitempty"`
}

type DeploymentSpec struct {
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

// ScalerStatus defines the observed state of Scaler.
type ScalerStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Status string `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Scaler is the Schema for the scalers API.
type Scaler struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ScalerSpec   `json:"spec,omitempty"`
	Status ScalerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ScalerList contains a list of Scaler.
type ScalerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Scaler `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Scaler{}, &ScalerList{})
}
