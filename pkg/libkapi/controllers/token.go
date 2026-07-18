package controllers

import (
	"crypto/rsa"
	"fmt"
	"time"

	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/informers"
	v1 "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	controllersa "k8s.io/kubernetes/pkg/controller/serviceaccount"
	"k8s.io/kubernetes/pkg/serviceaccount"
)

// defaultSAResyncPeriod matches pkg/server's retryInterval (30s).
const defaultSAResyncPeriod = 30 * time.Second

// TokenControllerHookConfig holds the inputs needed to build the SA token
// controller post-start hook.
type TokenControllerHookConfig struct {
	// Issuer for SA tokens. If empty, defaults to "kubernetes/serviceaccount".
	Issuer string

	// SigningKey signs newly issued tokens.
	SigningKey *rsa.PrivateKey

	// RootCA is included in token secrets as ca.crt. May be nil.
	RootCA []byte

	// ResyncPeriod is the interval at which the controller re-lists
	// ServiceAccounts and Secrets. Default: 30s.
	ResyncPeriod time.Duration
}

// NewTokenControllerHook builds a PostStartHookFunc that starts the SA token
// controller, which issues tokens for ServiceAccount resources. The
// controller uses the SharedInformerFactory's SA and Secret informers.
//
// Used when the signing key is available during New (either provided by the
// caller or generated as ephemeral). When the key must be loaded from a
// persisted Secret after the server starts, use NewServiceAccountSetupHook
// instead.
func NewTokenControllerHook(
	hookCfg TokenControllerHookConfig,
	loopbackConfig *restclient.Config,
	informerFactory informers.SharedInformerFactory,
) (genericapiserver.PostStartHookFunc, error) {
	kubeClient, err := kubernetes.NewForConfig(loopbackConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client for tokens controller: %w", err)
	}

	saInformer := informerFactory.Core().V1().ServiceAccounts()
	secretInformer := informerFactory.Core().V1().Secrets()

	tokenController, err := buildTokenController(
		hookCfg.Issuer, hookCfg.SigningKey, hookCfg.RootCA,
		saInformer, secretInformer, kubeClient)
	if err != nil {
		return nil, err
	}

	return func(ctx genericapiserver.PostStartHookContext) error {
		informerFactory.Start(ctx.Done())

		tokenController.Run(ctx, 1)

		return nil
	}, nil
}

// buildTokenController creates the SA token controller with the given
// signing key and informers. The informers must be registered on the
// SharedInformerFactory before Start is called.
func buildTokenController(
	issuer string,
	signingKey *rsa.PrivateKey,
	rootCA []byte,
	saInformer v1.ServiceAccountInformer,
	secretInformer v1.SecretInformer,
	kubeClient kubernetes.Interface,
) (*controllersa.TokensController, error) {
	if issuer == "" {
		issuer = "kubernetes/serviceaccount"
	}

	tokenGenerator, err := serviceaccount.JWTTokenGenerator(issuer, signingKey)
	if err != nil {
		return nil, fmt.Errorf("failed to build token generator: %w", err)
	}

	tokenController, err := controllersa.NewTokensController(
		saInformer,
		secretInformer,
		kubeClient,
		controllersa.TokensControllerOptions{
			ServiceAccountResync: defaultSAResyncPeriod,
			SecretResync:         defaultSAResyncPeriod,
			TokenGenerator:       tokenGenerator,
			RootCA:               rootCA,
		})
	if err != nil {
		return nil, fmt.Errorf("failed to create tokens controller: %w", err)
	}

	return tokenController, nil
}
