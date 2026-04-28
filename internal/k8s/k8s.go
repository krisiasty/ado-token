package k8s

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
)

type Credentials struct {
	TenantID     string
	ClientID     string
	ClientSecret string
}

func NewClient() (kubernetes.Interface, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		if !errors.Is(err, rest.ErrNotInCluster) {
			return nil, fmt.Errorf("in-cluster config failed: %w", err)
		}
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(),
			nil,
		).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("building kube client config: %w", err)
		}
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}
	return client, nil
}

func ReadCredentials(ctx context.Context, client kubernetes.Interface, namespace, name string) (*Credentials, error) {
	secret, err := client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting credentials secret %s/%s: %w", namespace, name, err)
	}

	get := func(key string) (string, error) {
		v, ok := secret.Data[key]
		if !ok {
			return "", fmt.Errorf("credentials secret %s/%s missing key %q", namespace, name, key)
		}
		trimmed := string(bytes.TrimSpace(v))
		if trimmed == "" {
			return "", fmt.Errorf("credentials secret %s/%s key %q is blank", namespace, name, key)
		}
		return trimmed, nil
	}

	tenantID, err := get("tenant_id")
	if err != nil {
		return nil, err
	}
	clientID, err := get("client_id")
	if err != nil {
		return nil, err
	}
	clientSecret, err := get("client_secret")
	if err != nil {
		return nil, err
	}

	return &Credentials{
		TenantID:     tenantID,
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}, nil
}

func UpdateSecret(ctx context.Context, client kubernetes.Interface, namespace, name, key, token string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("getting output secret %s/%s: %w", namespace, name, err)
		}

		updated := existing.DeepCopy()
		if updated.Data == nil {
			updated.Data = make(map[string][]byte)
		}
		updated.Data[key] = []byte(token)

		_, err = client.CoreV1().Secrets(namespace).Update(ctx, updated, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("updating output secret %s/%s: %w", namespace, name, err)
		}
		return nil
	})
}
