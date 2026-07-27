package controllers

import (
	"crypto/rsa"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
)

// CreateKeyHookConfig holds the inputs needed to build the create-only
// key persistence post-start hook.
type CreateKeyHookConfig struct {
	// SigningKey is the in-memory signing key to persist.
	SigningKey *rsa.PrivateKey

	// Namespace is where the signing key Secret is stored.
	Namespace string

	// SecretName is the name of the signing key Secret.
	SecretName string
}

// NewCreateKeyHook builds a PostStartHookFunc that creates the signing key
// Secret if it doesn't already exist. Unlike the previous persistSigningKey
// behavior, this hook does NOT overwrite an existing Secret — the key is
// only persisted on the first run, so subsequent restarts don't rotate it.
//
// Used when the signing key was available during New (provided or ephemeral)
// and KeyPersistence is set. When the signing key must be loaded from the
// Secret after the server starts, use NewServiceAccountSetupHook instead.
func NewCreateKeyHook(
	hookCfg CreateKeyHookConfig,
	loopbackConfig *restclient.Config,
) (genericapiserver.PostStartHookFunc, error) {
	kubeClient, err := kubernetes.NewForConfig(loopbackConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client for key persistence: %w", err)
	}

	coreClient := kubeClient.CoreV1()

	return func(ctx genericapiserver.PostStartHookContext) error {
		err := auth.CreateSigningKeySecret(
			ctx, coreClient, hookCfg.SigningKey,
			hookCfg.Namespace, hookCfg.SecretName)
		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				// Secret already exists from a previous run — don't overwrite.
				return nil
			}

			return fmt.Errorf("failed to create signing key secret: %w", err)
		}

		return nil
	}, nil
}
