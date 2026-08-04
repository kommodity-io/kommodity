package auth_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/client-go/informers"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	rbaclisters "k8s.io/client-go/listers/rbac/v1"

	"github.com/kommodity-io/kommodity/pkg/libkapi/auth"
)

// TestWithRBACAuthorizer_MissingAdminGroup_ReturnsError verifies that
// WithRBACAuthorizer returns ErrAdminGroupRequired when AdminGroups is
// empty — the RBAC half is purely additive, so an empty fallback would
// mean no deny-by-default backstop at all.
func TestWithRBACAuthorizer_MissingAdminGroup_ReturnsError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	_, err := auth.Resolve(ctx, []auth.Option{
		auth.WithRBACAuthorizer(auth.RBACAuthorizerConfig{AdminGroups: ""}),
	}, slog.Default())

	require.ErrorIs(t, err, auth.ErrAdminGroupRequired)
}

// TestWithRBACAuthorizer_SetsAuthorizerAndListerSource verifies that
// WithRBACAuthorizer sets both a non-nil authorizer and a non-nil
// RBACListerSource on the resolved config — buildServer relies on the
// latter being present to know whether to call SetListers.
func TestWithRBACAuthorizer_SetsAuthorizerAndListerSource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	resolved, err := auth.Resolve(ctx, []auth.Option{
		auth.WithRBACAuthorizer(auth.RBACAuthorizerConfig{AdminGroups: "my-admins"}),
	}, slog.Default())
	require.NoError(t, err)

	assert.NotNil(t, resolved.Authorizer)
	assert.NotNil(t, resolved.RBACListerSource)
}

// TestRBACListerSource_BeforeSetListers_ReturnsError verifies every
// Getter/Lister method fails before SetListers has been called.
func TestRBACListerSource_BeforeSetListers_ReturnsError(t *testing.T) {
	t.Parallel()

	source := &auth.RBACListerSource{}

	_, err := source.GetRole(context.Background(), "default", "reader")
	require.Error(t, err)

	_, err = source.ListRoleBindings(context.Background(), "default")
	require.Error(t, err)

	_, err = source.GetClusterRole(context.Background(), "reader")
	require.Error(t, err)

	_, err = source.ListClusterRoleBindings(context.Background())
	require.Error(t, err)
}

// newSyncedRBACListers builds client-go rbac/v1 listers backed by a fake
// clientset, seeded with objects and synced before returning — mirroring
// what finishRBACAuthorizer installs from a real SharedInformerFactory,
// but already caught up rather than racing a background Start.
func newSyncedRBACListers(t *testing.T, objects ...runtime.Object) (
	rbaclisters.RoleLister, rbaclisters.RoleBindingLister,
	rbaclisters.ClusterRoleLister, rbaclisters.ClusterRoleBindingLister,
) {
	t.Helper()

	fakeClient := fakeclientset.NewClientset(objects...)
	factory := informers.NewSharedInformerFactory(fakeClient, 0)
	rbacInformers := factory.Rbac().V1()

	roleLister := rbacInformers.Roles().Lister()
	roleBindingLister := rbacInformers.RoleBindings().Lister()
	clusterRoleLister := rbacInformers.ClusterRoles().Lister()
	clusterRoleBindingLister := rbacInformers.ClusterRoleBindings().Lister()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	factory.Start(ctx.Done())

	synced := factory.WaitForCacheSync(ctx.Done())
	for typ, ok := range synced {
		require.Truef(t, ok, "cache for %v never synced", typ)
	}

	return roleLister, roleBindingLister, clusterRoleLister, clusterRoleBindingLister
}

// TestRBACListerSource_AfterSetListers_ServesObjects verifies every
// Getter/Lister method reads through to the installed listers once
// SetListers has been called.
func TestRBACListerSource_AfterSetListers_ServesObjects(t *testing.T) {
	t.Parallel()

	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "reader", Namespace: "default"}}
	roleBinding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "readers", Namespace: "default"}}
	clusterRole := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "cluster-reader"}}
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "cluster-readers"}}

	roleLister, roleBindingLister, clusterRoleLister, clusterRoleBindingLister :=
		newSyncedRBACListers(t, role, roleBinding, clusterRole, clusterRoleBinding)

	source := &auth.RBACListerSource{}
	source.SetListers(roleLister, roleBindingLister, clusterRoleLister, clusterRoleBindingLister)

	gotRole, err := source.GetRole(context.Background(), "default", "reader")
	require.NoError(t, err)
	assert.Equal(t, "reader", gotRole.Name)

	roleBindings, err := source.ListRoleBindings(context.Background(), "default")
	require.NoError(t, err)
	require.Len(t, roleBindings, 1)
	assert.Equal(t, "readers", roleBindings[0].Name)

	gotClusterRole, err := source.GetClusterRole(context.Background(), "cluster-reader")
	require.NoError(t, err)
	assert.Equal(t, "cluster-reader", gotClusterRole.Name)

	clusterRoleBindings, err := source.ListClusterRoleBindings(context.Background())
	require.NoError(t, err)
	require.Len(t, clusterRoleBindings, 1)
	assert.Equal(t, "cluster-readers", clusterRoleBindings[0].Name)
}

// TestRBACAuthorizer_FallsBackToAdminGroupBeforeListersReady verifies that
// the admin-group fallback still works while the RBAC half's listers
// aren't installed yet — the window WithRBACAuthorizer's doc describes as
// safe because buildServer always closes it before the server ever serves.
func TestRBACAuthorizer_FallsBackToAdminGroupBeforeListersReady(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	resolved, err := auth.Resolve(ctx, []auth.Option{
		auth.WithRBACAuthorizer(auth.RBACAuthorizerConfig{AdminGroups: "platform-admins"}),
	}, slog.Default())
	require.NoError(t, err)

	loopbackUser := &user.DefaultInfo{Name: "loopback", Groups: []string{"system:masters"}}
	attrs := &fakeAttributes{user: loopbackUser, isResource: true, verb: "list", resource: "kontinuums"}

	decision, _, err := resolved.Authorizer.Authorize(ctx, attrs)
	require.NoError(t, err)
	assert.Equal(t, authorizer.DecisionAllow, decision,
		"the admin-group fallback must still work while the RBAC listers aren't ready yet")
}

// TestRBACAuthorizer_GrantsAccessViaRealClusterRoleBinding proves the RBAC
// half is actually consulted, not just present: a group with no entry in
// AdminGroups gets access purely because a ClusterRoleBinding/ClusterRole
// pair grants it, and loses access to anything that pair doesn't cover
// (the admin-group fallback doesn't recognize the group either).
func TestRBACAuthorizer_GrantsAccessViaRealClusterRoleBinding(t *testing.T) {
	t.Parallel()

	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "widget-reader"},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{"widgets.example.com"}, Resources: []string{"widgets"}, Verbs: []string{"get", "list"}},
		},
	}
	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "widget-readers"},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "widget-reader"},
		Subjects:   []rbacv1.Subject{{Kind: rbacv1.GroupKind, APIGroup: rbacv1.GroupName, Name: "widget-readers"}},
	}

	roleLister, roleBindingLister, clusterRoleLister, clusterRoleBindingLister :=
		newSyncedRBACListers(t, clusterRole, clusterRoleBinding)

	ctx := context.Background()

	resolved, err := auth.Resolve(ctx, []auth.Option{
		auth.WithRBACAuthorizer(auth.RBACAuthorizerConfig{AdminGroups: "platform-admins"}),
	}, slog.Default())
	require.NoError(t, err)

	resolved.RBACListerSource.SetListers(roleLister, roleBindingLister, clusterRoleLister, clusterRoleBindingLister)

	reader := &user.DefaultInfo{Name: "bob", Groups: []string{"widget-readers"}}

	allowed := &fakeAttributes{
		user: reader, isResource: true, verb: "get", apiGroup: "widgets.example.com", resource: "widgets",
	}

	decision, _, err := resolved.Authorizer.Authorize(ctx, allowed)
	require.NoError(t, err)
	assert.Equal(t, authorizer.DecisionAllow, decision, "the ClusterRoleBinding's rule should grant this")

	notAllowed := &fakeAttributes{
		user: reader, isResource: true, verb: "delete", apiGroup: "widgets.example.com", resource: "widgets",
	}

	decision, _, err = resolved.Authorizer.Authorize(ctx, notAllowed)
	require.NoError(t, err)
	assert.Equal(t, authorizer.DecisionDeny, decision,
		"delete isn't in the bound ClusterRole's rules and the user isn't in any admin group, "+
			"so the admin-group fallback must deny it too")
}

