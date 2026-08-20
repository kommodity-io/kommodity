package controllers

import (
	"context"
	"crypto/rsa"
	"fmt"
	"log/slog"

	"k8s.io/apiserver/pkg/authentication/authenticator"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/informers"
	v1 "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
)

// ServiceAccountSetupHookConfig holds the inputs for the combined SA setup
// hook, used when the signing key must be loaded from a persisted Secret
// after the server starts.
type ServiceAccountSetupHookConfig struct {
	// SACfg is the ServiceAccount configuration. KeyPersistence must be non-nil.
	SACfg *auth.ServiceAccountConfig

	// OIDCAuth is the OIDC authenticator, or nil if OIDC was not configured.
	OIDCAuth authenticator.Request

	// SetAuthenticator is called to swap in the final union authenticator
	// after the SA authenticator is built. Typically DynamicAuthenticator.Set.
	SetAuthenticator func(authenticator.Request)

	// LoopbackConfig is the server's loopback client config.
	LoopbackConfig *restclient.Config

	// InformerFactory is the server's SharedInformerFactory.
	InformerFactory informers.SharedInformerFactory

	// SystemNamespace is the resolved namespace for the signing key Secret
	// and SA-token-rotation lister, used as the fallback when
	// KeyPersistence.Namespace/TokenSecretsNamespace aren't set.
	SystemNamespace string

	// Logger receives log output from the hook.
	Logger *slog.Logger
}

// NewServiceAccountSetupHook builds a PostStartHookFunc that:
//  1. Resolves the signing key by loading it from the persisted Secret, or
//     generating a new key and creating the Secret if it doesn't exist.
//  2. Builds the SA authenticator with the resolved key.
//  3. Swaps in the final union authenticator (OIDC + SA + anonymous) via
//     SetAuthenticator.
//  4. Starts the SA token controller.
//  5. Sets up signing key rotation watch (if KeyPersistence is set).
//
// This hook is used when SigningKey is nil and KeyPersistence is set,
// so the key can be loaded from the persisted Secret after the server
// starts listening. The server briefly runs with a placeholder authenticator
// (OIDC + anonymous) until this hook completes.
func NewServiceAccountSetupHook(
	hookCfg ServiceAccountSetupHookConfig,
) (genericapiserver.PostStartHookFunc, error) {
	kubeClient, err := kubernetes.NewForConfig(hookCfg.LoopbackConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client for SA setup: %w", err)
	}

	saInformer := hookCfg.InformerFactory.Core().V1().ServiceAccounts()
	secretInformer := hookCfg.InformerFactory.Core().V1().Secrets()

	return func(ctx genericapiserver.PostStartHookContext) error {
		signingKey, err := auth.LoadOrCreateSigningKey(
			ctx, kubeClient.CoreV1(), hookCfg.SACfg.KeyPersistence, hookCfg.SystemNamespace)
		if err != nil {
			return fmt.Errorf("failed to resolve service account signing key: %w", err)
		}

		hookCfg.Logger.Info("Service account signing key resolved")

		saAuth, err := auth.BuildSAAuthenticator(
			hookCfg.SACfg, signingKey, hookCfg.LoopbackConfig)
		if err != nil {
			return fmt.Errorf("failed to build service account authenticator: %w", err)
		}

		hookCfg.SetAuthenticator(auth.BuildUnionAuthenticator(hookCfg.OIDCAuth, saAuth))

		hookCfg.InformerFactory.Start(ctx.Done())

		err = runTokenController(ctx, hookCfg.SACfg, signingKey,
			saInformer, secretInformer, kubeClient)
		if err != nil {
			return fmt.Errorf("failed to start token controller: %w", err)
		}

		if hookCfg.SACfg.KeyPersistence != nil {
			SetupSigningKeyRotation(
				secretInformer.Informer(),
				hookCfg.SACfg,
				signingKey,
				kubeClient.CoreV1(),
				hookCfg.SystemNamespace,
				hookCfg.Logger,
			)
		}

		return nil
	}, nil
}

// runTokenController builds and runs the SA token controller. The informers
// must already be registered on the InformerFactory before Start is called.
func runTokenController(
	ctx genericapiserver.PostStartHookContext,
	saCfg *auth.ServiceAccountConfig,
	signingKey *rsa.PrivateKey,
	saInformer v1.ServiceAccountInformer,
	secretInformer v1.SecretInformer,
	kubeClient kubernetes.Interface,
) error {
	issuer := saCfg.Issuer
	if issuer == "" {
		issuer = "kubernetes/serviceaccount"
	}

	tokenController, err := buildTokenController(
		issuer, signingKey, saCfg.RootCA, saInformer, secretInformer, kubeClient)
	if err != nil {
		return err
	}

	go func() {
		runCtx, cancel := context.WithCancelCause(ctx)
		defer cancel(nil)

		tokenController.Run(runCtx, 1)
	}()

	return nil
}
