package state

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// Namespace is the Kubernetes namespace where nstack stores its state.
	Namespace = "nstack-system"
	// configMapBase is the base name for the state ConfigMap.
	configMapBase = "nstack-state"
	// DataKey is the key within the ConfigMap's data map that holds the JSON state.
	DataKey = "state"
)

// Store provides ConfigMap-backed persistence for nstack deployment state.
type Store struct {
	clientset     kubernetes.Interface
	configMapName string
}

// NewStore creates a new Store backed by the given Kubernetes clientset.
// If siteName is non-empty, the ConfigMap is named "nstack-state-<siteName>"
// to isolate state per site; otherwise it defaults to "nstack-state".
func NewStore(clientset kubernetes.Interface, siteName string) *Store {
	name := configMapBase
	if siteName != "" {
		name = configMapBase + "-" + siteName
	}
	return &Store{clientset: clientset, configMapName: name}
}

// ConfigMapName returns the name of the ConfigMap used by this store.
func (s *Store) ConfigMapName() string {
	return s.configMapName
}

// Load retrieves the nstack state from the ConfigMap. If the ConfigMap does
// not exist, it returns an empty State with an initialized Stages map.
func (s *Store) Load(ctx context.Context) (*State, error) {
	cm, err := s.clientset.CoreV1().ConfigMaps(Namespace).Get(ctx, s.configMapName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return &State{Stages: make(map[int]*StageState)}, nil
		}
		return nil, fmt.Errorf("getting configmap %s/%s: %w", Namespace, s.configMapName, err)
	}

	data, ok := cm.Data[DataKey]
	if !ok {
		return &State{Stages: make(map[int]*StageState)}, nil
	}

	var st State
	if err := json.Unmarshal([]byte(data), &st); err != nil {
		return nil, fmt.Errorf("unmarshaling state: %w", err)
	}

	// Ensure Stages is never nil even if the JSON had "stages": null.
	if st.Stages == nil {
		st.Stages = make(map[int]*StageState)
	}

	return &st, nil
}

// Save persists the given state to the ConfigMap, creating or updating as needed.
func (s *Store) Save(ctx context.Context, state *State) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}

	cmClient := s.clientset.CoreV1().ConfigMaps(Namespace)

	existing, err := cmClient.Get(ctx, s.configMapName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("getting configmap %s/%s: %w", Namespace, s.configMapName, err)
		}

		// ConfigMap does not exist — create it.
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      s.configMapName,
				Namespace: Namespace,
			},
			Data: map[string]string{
				DataKey: string(raw),
			},
		}
		if _, err := cmClient.Create(ctx, cm, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("creating configmap %s/%s: %w", Namespace, s.configMapName, err)
		}
		return nil
	}

	// ConfigMap exists — update it.
	if existing.Data == nil {
		existing.Data = make(map[string]string)
	}
	existing.Data[DataKey] = string(raw)

	if _, err := cmClient.Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("updating configmap %s/%s: %w", Namespace, s.configMapName, err)
	}
	return nil
}

// EnsureNamespace creates the nstack-system namespace if it does not already exist.
func (s *Store) EnsureNamespace(ctx context.Context) error {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: Namespace,
		},
	}

	_, err := s.clientset.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating namespace %s: %w", Namespace, err)
	}
	return nil
}
