package libkapi_test

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"

	"github.com/kommodity-io/kommodity/pkg/libkapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testResourceName names every resource this test creates.
const testResourceName = "libkapi-test"

// TestServerEndToEnd builds a real libkapi Server backed by an in-process
// Kine instance (sqlite, no external database needed), mounts a custom HTTP
// handler alongside it, and exercises it as a real client would: hitting the
// custom route, /healthz, and CRUD across one representative resource from
// each standard API group libkapi wires (core v1, apps/v1,
// rbac.authorization.k8s.io/v1), plus creating a CustomResourceDefinition to
// prove the CRD server is live.
func TestServerEndToEnd(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := libkapi.FreeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "libkapi.db")

	cfg := libkapi.Config{
		Addr:    addr,
		Storage: "sqlite://" + dbPath,
		Logger:  slog.Default(),
		Handlers: []libkapi.HTTPHandlerFactory{
			func(mux *http.ServeMux) error {
				mux.HandleFunc("GET /hello", func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte("hello"))
				})

				return nil
			},
		},
	}

	server, err := libkapi.New(ctx, cfg)
	require.NoError(t, err)

	go func() {
		_ = server.ListenAndServe(ctx)
	}()

	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		_ = server.Shutdown(shutdownCtx)
	})

	baseURL := "http://" + addr

	libkapi.WaitForHealthz(t, baseURL+"/healthz")

	assertHelloRoute(t, baseURL+"/hello")

	restConfig := &restclient.Config{Host: baseURL}

	kubeClient, err := kubernetes.NewForConfig(restConfig)
	require.NoError(t, err)

	assertCoreV1(ctx, t, kubeClient)
	assertAppsV1(ctx, t, kubeClient)
	assertRBACV1(ctx, t, kubeClient)
	assertCRDServerLive(ctx, t, restConfig)
}

// TestServerEndToEnd_WithAdminAuthorizer verifies that once an authorizer
// denies anonymous requests, libkapi's own internal loopback client (used by
// post-start hooks like bootstrap-default-namespace, and by the CRD/
// APIService informers) still authenticates as a privileged user, while an
// unauthenticated external caller is denied.
//
// Regression test: the loopback client used to carry no bearer token, so it
// authenticated as system:anonymous exactly like any other caller and was
// denied by AdminAuthorizer along with everyone else. A failing post-start
// hook makes k8s.io/apiserver call klog.Fatal (see
// k8s.io/apiserver@v0.32.6 pkg/server/hooks.go's runPostStartHook - "if the
// hook intentionally wants to kill server, let it"), so before the fix this
// test would crash the whole process instead of ever reaching the assertion
// below.
func TestServerEndToEnd_WithAdminAuthorizer(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr := libkapi.FreeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "libkapi-admin.db")

	cfg := libkapi.Config{
		Addr:    addr,
		Storage: "sqlite://" + dbPath,
		Logger:  slog.Default(),
	}

	server, err := libkapi.New(ctx, cfg, libkapi.WithAdminAuthorizer(libkapi.AdminAuthorizerConfig{
		AdminGroups: "test-admins",
	}))
	require.NoError(t, err)

	go func() {
		_ = server.ListenAndServe(ctx)
	}()

	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		_ = server.Shutdown(shutdownCtx)
	})

	baseURL := "http://" + addr

	libkapi.WaitForHealthz(t, baseURL+"/healthz")

	kubeClient, err := kubernetes.NewForConfig(&restclient.Config{Host: baseURL})
	require.NoError(t, err)

	_, err = kubeClient.CoreV1().Namespaces().Get(ctx, "default", metav1.GetOptions{})
	require.Error(t, err)
	assert.True(t, apierrors.IsForbidden(err), "expected anonymous request to be forbidden, got: %v", err)
}

func assertCoreV1(ctx context.Context, t *testing.T, kubeClient *kubernetes.Clientset) {
	t.Helper()

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testResourceName}}

	_, err := kubeClient.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	require.NoError(t, err)

	got, err := kubeClient.CoreV1().Namespaces().Get(ctx, testResourceName, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, testResourceName, got.Name)
}

func assertAppsV1(ctx context.Context, t *testing.T, kubeClient *kubernetes.Clientset) {
	t.Helper()

	replicas := int32(1)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: testResourceName, Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": testResourceName}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": testResourceName}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "test", Image: "test"}},
				},
			},
		},
	}

	_, err := kubeClient.AppsV1().Deployments("default").Create(ctx, deployment, metav1.CreateOptions{})
	require.NoError(t, err)

	got, err := kubeClient.AppsV1().Deployments("default").Get(ctx, testResourceName, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, testResourceName, got.Name)
}

func assertRBACV1(ctx context.Context, t *testing.T, kubeClient *kubernetes.Clientset) {
	t.Helper()

	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: testResourceName, Namespace: "default"},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
		},
	}

	_, err := kubeClient.RbacV1().Roles("default").Create(ctx, role, metav1.CreateOptions{})
	require.NoError(t, err)

	got, err := kubeClient.RbacV1().Roles("default").Get(ctx, testResourceName, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, testResourceName, got.Name)
}

func assertCRDServerLive(ctx context.Context, t *testing.T, restConfig *restclient.Config) {
	t.Helper()

	apiextensionsClient, err := apiextensionsclientset.NewForConfig(restConfig)
	require.NoError(t, err)

	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "widgets.libkapi.test"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "libkapi.test",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural:   "widgets",
				Singular: "widget",
				Kind:     "Widget",
				ListKind: "WidgetList",
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type:                   "object",
							XPreserveUnknownFields: new(true),
						},
					},
				},
			},
		},
	}

	_, err = apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions().Create(ctx, crd, metav1.CreateOptions{})
	require.NoError(t, err)

	err = wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 10*time.Second, true,
		func(ctx context.Context) (bool, error) {
			got, err := apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions().
				Get(ctx, "widgets.libkapi.test", metav1.GetOptions{})
			if err != nil {
				return false, nil //nolint:nilerr // keep polling on transient errors
			}

			for _, cond := range got.Status.Conditions {
				if cond.Type == apiextensionsv1.Established && cond.Status == apiextensionsv1.ConditionTrue {
					return true, nil
				}
			}

			return false, nil
		})
	require.NoError(t, err, "CRD never became established")
}

func assertHelloRoute(t *testing.T, url string) {
	t.Helper()

	resp, err := http.Get(url) //nolint:noctx,gosec // test helper, url is our own freshly-built server address
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
