package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"

	"github.com/kommodity-io/kommodity/pkg/combinedserver"
	"github.com/kommodity-io/kommodity/pkg/config"
	"github.com/kommodity-io/kommodity/pkg/logging"
	"go.uber.org/zap"
	"k8s.io/client-go/rest"
)

// NewHTTPMuxFactory creates a new HTTP mux proxy factory for the API server.
func NewHTTPMuxFactory(ctx context.Context, cfg *config.KommodityConfig) combinedserver.HTTPMuxFactory {
	return func(mux *http.ServeMux) error {
		server, err := New(ctx, cfg)
		if err != nil {
			return fmt.Errorf("failed to create server: %w", err)
		}

		go func() {
			logger := logging.FromContext(ctx)

			runCtx, cancel := context.WithCancelCause(ctx)
			defer cancel(nil)

			preparedGenericServer, err := server.PrepareRun()
			if err != nil {
				errorMsg := "failed to prepare generic server:"
				logger.Error(errorMsg, zap.Error(err))
				cancel(fmt.Errorf("%s %w", errorMsg, err))
			}

			err = preparedGenericServer.Run(runCtx)
			if err != nil {
				errorMsg := "failed to run generic server:"
				logger.Error(errorMsg, zap.Error(err))
				cancel(fmt.Errorf("%s %w", errorMsg, err))
			}
		}()

		proxy, err := setupProxy(ctx, cfg,
			server.GenericAPIServer.LoopbackClientConfig.TLSClientConfig)
		if err != nil {
			return fmt.Errorf("failed to setup proxy: %w", err)
		}

		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			proxy.ServeHTTP(w, r)
		})

		return nil
	}
}

func setupProxy(ctx context.Context, cfg *config.KommodityConfig, tlsClient rest.TLSClientConfig) (*httputil.ReverseProxy, error) {
	// Target backend URL (where the proxy will forward requests)
	target, err := url.Parse("https://localhost:" + strconv.Itoa(cfg.APIServerPort))
	if err != nil {
		return nil, fmt.Errorf("failed to parse target URL: %w", err)
	}

	// Create the reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(target)

	tlsConfig, err := tlsConfigFromREST(tlsClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS config: %w", err)
	}

	proxy.Transport = &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, e error) {
		logger := logging.FromContext(ctx)
		logger.Error("Reverse proxy error", zap.Error(e))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}

	return proxy, nil
}

func tlsConfigFromREST(restTLS rest.TLSClientConfig) (*tls.Config, error) {
	rootCAs := x509.NewCertPool()
	if len(restTLS.CAData) > 0 {
		if ok := rootCAs.AppendCertsFromPEM(restTLS.CAData); !ok {
			return nil, ErrFailedToAppendCAData
		}
	}

	tlsConfig := &tls.Config{
		RootCAs:    rootCAs,
		MinVersion: tls.VersionTLS12,
	}

	if restTLS.Insecure {
		tlsConfig.InsecureSkipVerify = true
	}

	return tlsConfig, nil
}
