package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type AgentInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Spec              AgentInstanceSpec   `json:"spec"`
	Status            AgentInstanceStatus `json:"status"`
}

type AgentInstanceSpec struct {
	UserID       string   `json:"userID"`
	DisplayName  string   `json:"displayName"`
	Model        string   `json:"model"`
	MCPServerIDs []string `json:"mcpServerIDs,omitempty"`
	Suspended    bool     `json:"suspended"`
}

type AgentInstanceStatus struct {
	State              string `json:"state,omitempty"`
	Error              string `json:"error,omitempty"`
	ObservedGeneration int64  `json:"observedGeneration,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type AgentInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []AgentInstance `json:"items"`
}

func (in *AgentInstance) Has(field string) bool { return field == "spec.userID" }

func (in *AgentInstance) Get(field string) string {
	if field == "spec.userID" {
		return in.Spec.UserID
	}
	return ""
}

func (in *AgentInstance) FieldNames() []string { return []string{"spec.userID"} }
