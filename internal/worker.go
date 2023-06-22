package internal

import (
	"encoding/json"
	"errors"
	"fmt"
)

const (
	asgKey      = "asg_id"
	instanceKey = "instance_id"
)

type GroupID string
type InstanceID string

type Worker struct {
	ID        string `graphql:"id" json:"id"`
	Busy      bool   `graphql:"busy" json:"busy"`
	CreatedAt int32  `graphql:"createdAt" json:"createdAt"`
	Drained   bool   `graphql:"drained" json:"drained"`
	Metadata  string `graphql:"metadata" json:"metadata"`
}

func (w *Worker) InstanceIdentity() (GroupID, InstanceID, error) {
	groupID, groupErr := w.metadataValue(asgKey)
	instanceID, instanceErr := w.metadataValue(instanceKey)
	return GroupID(groupID), InstanceID(instanceID), errors.Join(groupErr, instanceErr)
}

func (w *Worker) metadata() (map[string]string, error) {
	out := make(map[string]string)

	if err := json.Unmarshal([]byte(w.Metadata), &out); err != nil {
		return nil, fmt.Errorf("invalid instance metadata: %w", err)
	}

	return out, nil
}

func (w *Worker) metadataValue(key string) (string, error) {
	metadata, err := w.metadata()
	if err != nil {
		return "", err
	}

	value, exists := metadata[key]
	if !exists {
		return "", fmt.Errorf("metadata %s not present", key)
	}

	return value, nil
}
