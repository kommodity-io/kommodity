package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apiserver/pkg/authorization/union"
	rbaclisters "k8s.io/client-go/listers/rbac/v1"
	upstreamrbac "k8s.io/kubernetes/plugin/pkg/auth/authorizer/rbac"
)

// errRBACListersNotReady is returned by RBACListerSource's Getter/Lister
// methods before SetListers has been called — see RBACListerSource's doc.
// In practice this should never be observed: buildServer calls SetListers
// synchronously during New, before the server ever starts serving, so no
// Authorize call can reach RBACListerSource before it's ready.
var errRBACListersNotReady = errors.New("rbac authorizer: informer listers not ready")

// RBACAuthorizerConfig configures the RBAC authorizer.
type RBACAuthorizerConfig struct {
	// AdminGroups is a comma-delimited list of groups allowed full access,
	// used as WithRBACAuthorizer's deny-by-default fallback whenever no
	// RBAC rule grants access — see WithRBACAuthorizer. Same format as
	// AdminAuthorizerConfig.AdminGroups. At least one group is required.
	AdminGroups string
}

// WithRBACAuthorizer sets an authorizer that evaluates real RBAC rules —
// Role, RoleBinding, ClusterRole, and ClusterRoleBinding objects, via
// upstream Kubernetes's own RBAC authorizer — tried first, falling back to
// the same behavior WithAdminAuthorizer provides (system:masters,
// AdminGroups, system:serviceaccounts, health paths) whenever no RBAC rule
// matches.
//
// This ordering is safe because upstream's RBAC authorizer only ever
// returns Allow or NoOpinion for a request, never a terminal Deny — RBAC
// is purely additive — so every bit of the admin-group authorizer's
// deny-by-default behavior is preserved exactly, while a real
// ClusterRoleBinding/RoleBinding also grants access through actual rule
// evaluation, so `kubectl auth can-i` reflects reality instead of a grant
// that exists only as an authorizer's in-memory group check.
//
// The RBAC half reads Role/RoleBinding/ClusterRole/ClusterRoleBinding
// objects through informer-backed listers, not a live per-call API
// request: a direct client would make Authorize itself issue a new
// request against this same server to decide whether to authorize that
// very request (to check "is this ClusterRoleBinding list allowed," it
// would need to list ClusterRoleBindings, which needs authorizing, which
// needs to list ClusterRoleBindings...) — client-go listers instead read
// a local, continuously-updated cache that never blocks and never makes a
// new outbound call from inside Authorize, exactly how upstream
// kube-apiserver's own RBAC authorizer avoids the same trap.
//
// Those listers can't be built until the server's SharedInformerFactory
// exists, which doesn't happen at Option-application time (it's produced
// by apiserver.SetupAPIServerConfig, itself called with the resolved
// authorizer as an argument — the server's privileged identity and its
// authorizer are resolved in the same step, each needing the other).
// WithRBACAuthorizer breaks that cycle by installing an RBACListerSource
// with no listers yet; buildServer calls SetListers on it once the
// SharedInformerFactory is available (see server.go's
// finishRBACAuthorizer), synchronously within New, entirely before
// ListenAndServe is ever called — so there is no window where a real
// request could reach this authorizer before it's ready. Until the
// factory's own post-start hook actually starts syncing (registered by
// finishRBACAuthorizer too), the listers simply read an empty cache — no
// blocking, no error, just "no matching rule yet," which is exactly the
// same outcome as no RBAC objects existing at all: NoOpinion, falling
// through to the admin-group fallback.
func WithRBACAuthorizer(rbacCfg RBACAuthorizerConfig) Option {
	return func(_ context.Context, cfg *config) error {
		groups := parseAdminGroups(rbacCfg.AdminGroups)
		if len(groups) == 0 {
			return ErrAdminGroupRequired
		}

		source := &RBACListerSource{}
		rbacAuthorizer := upstreamrbac.New(source, source, source, source)
		adminAuthorizer := &AdminAuthorizer{Groups: groups}

		cfg.authorizer = union.New(rbacAuthorizer, adminAuthorizer)
		cfg.rbacListerSource = source

		return nil
	}
}

// RBACListerSource adapts client-go's rbac/v1 listers, installed later via
// SetListers, to the RoleGetter/RoleBindingLister/ClusterRoleGetter/
// ClusterRoleBindingLister interfaces
// k8s.io/kubernetes/plugin/pkg/auth/authorizer/rbac.New expects
// (k8s.io/kubernetes/pkg/registry/rbac/validation's own interfaces) — see
// WithRBACAuthorizer's doc for why listers, not a direct client.
//
// Every method locks around the same lister fields, so SetListers is safe
// to call concurrently with Get/List calls arriving from in-flight
// Authorize checks — though in practice buildServer populates it
// synchronously during New, before the server ever starts serving, so no
// such call ever actually races it.
type RBACListerSource struct {
	mu                       sync.RWMutex
	roleLister               rbaclisters.RoleLister
	roleBindingLister        rbaclisters.RoleBindingLister
	clusterRoleLister        rbaclisters.ClusterRoleLister
	clusterRoleBindingLister rbaclisters.ClusterRoleBindingLister
}

// SetListers installs the four listers, unblocking every Getter/Lister
// method.
func (s *RBACListerSource) SetListers(
	roleLister rbaclisters.RoleLister,
	roleBindingLister rbaclisters.RoleBindingLister,
	clusterRoleLister rbaclisters.ClusterRoleLister,
	clusterRoleBindingLister rbaclisters.ClusterRoleBindingLister,
) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.roleLister = roleLister
	s.roleBindingLister = roleBindingLister
	s.clusterRoleLister = clusterRoleLister
	s.clusterRoleBindingLister = clusterRoleBindingLister
}

// GetRole implements
// k8s.io/kubernetes/pkg/registry/rbac/validation.RoleGetter.
func (s *RBACListerSource) GetRole(_ context.Context, namespace, name string) (*rbacv1.Role, error) {
	s.mu.RLock()
	lister := s.roleLister
	s.mu.RUnlock()

	if lister == nil {
		return nil, errRBACListersNotReady
	}

	role, err := lister.Roles(namespace).Get(name)
	if err != nil {
		return nil, fmt.Errorf("failed to get role %q: %w", name, err)
	}

	return role, nil
}

// ListRoleBindings implements
// k8s.io/kubernetes/pkg/registry/rbac/validation.RoleBindingLister.
func (s *RBACListerSource) ListRoleBindings(_ context.Context, namespace string) ([]*rbacv1.RoleBinding, error) {
	s.mu.RLock()
	lister := s.roleBindingLister
	s.mu.RUnlock()

	if lister == nil {
		return nil, errRBACListersNotReady
	}

	bindings, err := lister.RoleBindings(namespace).List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("failed to list role bindings in %q: %w", namespace, err)
	}

	return bindings, nil
}

// GetClusterRole implements
// k8s.io/kubernetes/pkg/registry/rbac/validation.ClusterRoleGetter.
func (s *RBACListerSource) GetClusterRole(_ context.Context, name string) (*rbacv1.ClusterRole, error) {
	s.mu.RLock()
	lister := s.clusterRoleLister
	s.mu.RUnlock()

	if lister == nil {
		return nil, errRBACListersNotReady
	}

	role, err := lister.Get(name)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster role %q: %w", name, err)
	}

	return role, nil
}

// ListClusterRoleBindings implements
// k8s.io/kubernetes/pkg/registry/rbac/validation.ClusterRoleBindingLister.
func (s *RBACListerSource) ListClusterRoleBindings(_ context.Context) ([]*rbacv1.ClusterRoleBinding, error) {
	s.mu.RLock()
	lister := s.clusterRoleBindingLister
	s.mu.RUnlock()

	if lister == nil {
		return nil, errRBACListersNotReady
	}

	bindings, err := lister.List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster role bindings: %w", err)
	}

	return bindings, nil
}
