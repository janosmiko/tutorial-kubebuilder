/*
Copyright 2023.

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
	"crypto/sha256"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"
)

type DomainResolverStatusPhase string

const (
	DomainResolverStatusPhasePending     DomainResolverStatusPhase = "Pending"
	DomainResolverStatusPhaseCreating    DomainResolverStatusPhase = "Creating"
	DomainResolverStatusPhaseCreated     DomainResolverStatusPhase = "Created"
	DomainResolverStatusPhaseTerminating DomainResolverStatusPhase = "Terminating"
	DomainResolverStatusPhaseDeleted     DomainResolverStatusPhase = "Deleted"
	DomainResolverStatusPhaseError       DomainResolverStatusPhase = "Error"
)

// DomainResolverSpec defines the desired state of DomainResolver
type DomainResolverSpec struct {
	//+kubebuilder:validation:Required
	//+kubebuilder:validation:MinLength=1
	//+kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	Id string `json:"id"`

	//+kubebuilder:validation:Required
	//+kubebuilder:validation:MinLength=1
	Domain string `json:"domain"`
}

// DomainResolverStatus defines the observed state of DomainResolver
type DomainResolverStatus struct {
	Ready             bool               `json:"ready"`
	Failed            int                `json:"failed,omitempty"`
	Phase             metav1.Condition   `json:"phase,omitempty"`
	Conditions        []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
	SpecHash          string             `json:"specHash,omitempty"`
	IPAddress         string             `json:"ipAddress,omitempty"`
	ControllerImageID string             `json:"controllerImageID,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
// Allow the DomainResolver to be approved by the API server
//+kubebuilder:metadata:annotations="api-approved.kubernetes.io=https://janosmiko.com"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="The age of the object"
//+kubebuilder:printcolumn:name="Domain",type="string",JSONPath=".spec.domain",description="The domain name assigned to the object"
//+kubebuilder:printcolumn:name="IP Address",type="string",JSONPath=".status.ipAddress",description="The IP address assigned to the object"
//+kubebuilder:printcolumn:name="Image ID",type="string",JSONPath=".status.controllerImageID",description="The Image ID of the kube-rbac-proxy"

// DomainResolver is the Schema for the domainresolvers API
type DomainResolver struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DomainResolverSpec   `json:"spec,omitempty"`
	Status DomainResolverStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// DomainResolverList contains a list of DomainResolver
type DomainResolverList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DomainResolver `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DomainResolver{}, &DomainResolverList{})
}

func (r *DomainResolver) CreateSpecHash() (string, error) {
	hashSrc, err := json.Marshal(r.Spec)
	if err != nil {
		return "", err
	}

	toHash := []interface{}{hashSrc}

	hash, err := GenerateHashFromInterfaces(toHash)
	if err != nil {
		return "", err
	}

	return hash.String(), nil
}

type InterfaceHash []byte

// String returns the string value of an interface hash.
func (hash *InterfaceHash) String() string {
	return fmt.Sprintf("%x", *hash)
}

// Short returns the first 8 characters of an interface hash.
func (hash *InterfaceHash) Short() string {
	return hash.String()[:8]
}

// GenerateHashFromInterfaces returns a hash sum based on a slice of given interfaces.
func GenerateHashFromInterfaces(interfaces []interface{}) (InterfaceHash, error) {
	hashSrc := make([]byte, len(interfaces))

	for _, in := range interfaces {
		chainElem, err := json.Marshal(in)
		if err != nil {
			return InterfaceHash{}, err
		}

		hashSrc = append(hashSrc, chainElem...)
	}

	hash := sha256.New()

	_, err := hash.Write(hashSrc)
	if err != nil {
		return nil, err
	}

	return hash.Sum(nil), nil
}
