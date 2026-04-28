package k8s

import (
	"context"
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
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(),
			nil,
		).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("building kube client config: %w", err)
		}
	}
	return kubernetes.NewForConfig(cfg)
}

func ReadCredentials(ctx context.Context, client kubernetes.Interface, namespace, name string) (*Credentials, error) {
	secret, err := client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting credentials secret %s/%s: %w", namespace, name, err)
	}

	get := func(key string) (string, error) {
		v, ok := secret.Data[key]
		if !ok || len(v) == 0 {
			return "", fmt.Errorf("credentials secret %s/%s missing key %q", namespace, name, key)
		}
		return string(v), nil
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
	op := "updating"
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		op = "getting"
		existing, err := client.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		updated := existing.DeepCopy()
		if updated.Data == nil {
			updated.Data = make(map[string][]byte)
		}
		updated.Data[key] = []byte(token)

		op = "updating"
		_, err = client.CoreV1().Secrets(namespace).Update(ctx, updated, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		return fmt.Errorf("%s output secret %s/%s: %w", op, namespace, name, err)
	}
	return nil
}
