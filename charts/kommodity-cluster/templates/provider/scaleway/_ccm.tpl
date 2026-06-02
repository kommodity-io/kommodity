{{/*
Cloud-controller-manager manifests for the Scaleway provider, applied to the
workload cluster via ClusterResourceSet. All resources land in kube-system.

Source: github.com/scaleway/scaleway-cloud-controller-manager
        @ 944bc4afe666eb6556e7939247ed590bba5329ed
        examples/k8s-scaleway-ccm-latest.yml
*/}}
{{- define "kommodity.ccm.manifest.Scaleway" -}}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: scaleway-cloud-controller-manager
  namespace: kube-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: scaleway-cloud-controller-manager
  revisionHistoryLimit: 2
  template:
    metadata:
      labels:
        app: scaleway-cloud-controller-manager
      annotations:
        scheduler.alpha.kubernetes.io/critical-pod: ''
    spec:
      dnsPolicy: Default
      hostNetwork: true
      serviceAccountName: cloud-controller-manager
      tolerations:
        - key: "node.cloudprovider.kubernetes.io/uninitialized"
          value: "true"
          effect: "NoSchedule"
        - key: "CriticalAddonsOnly"
          operator: "Exists"
        - key: "node-role.kubernetes.io/master"
          effect: NoSchedule
      containers:
        - name: scaleway-cloud-controller-manager
          image: {{ .Values.kommodity.provider.cloudControllerManager.image.repository }}:{{ .Values.kommodity.provider.cloudControllerManager.image.tag }}
          imagePullPolicy: Always
          args:
            - --cloud-provider=scaleway
            - --leader-elect=true
            - --allow-untagged-cloud
          resources:
            requests:
              cpu: 100m
              memory: 50Mi
          envFrom:
            - secretRef:
                name: scaleway-secret
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: cloud-controller-manager
  namespace: kube-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  annotations:
    rbac.authorization.kubernetes.io/autoupdate: "true"
  name: system:cloud-controller-manager
rules:
  - apiGroups:
      - coordination.k8s.io
    resources:
      - leases
    verbs:
      - get
      - create
      - update
  - apiGroups:
      - ""
    resources:
      - events
    verbs:
      - create
      - patch
      - update
  - apiGroups:
      - ""
    resources:
      - nodes
    verbs:
      - '*'
  - apiGroups:
      - ""
    resources:
      - nodes/status
    verbs:
      - patch
  - apiGroups:
      - ""
    resources:
      - services
    verbs:
      - list
      - patch
      - update
      - watch
  - apiGroups:
      - ""
    resources:
      - services/status
    verbs:
      - list
      - patch
      - update
      - watch
  - apiGroups:
      - ""
    resources:
      - serviceaccounts
    verbs:
      - create
  - apiGroups:
      - ""
    resources:
      - persistentvolumes
    verbs:
      - get
      - list
      - update
      - watch
  - apiGroups:
      - ""
    resources:
      - endpoints
    verbs:
      - create
      - get
      - list
      - watch
      - update
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: system:cloud-controller-manager
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:cloud-controller-manager
subjects:
  - kind: ServiceAccount
    name: cloud-controller-manager
    namespace: kube-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: system:cloud-controller-manager
  namespace: kube-system
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: extension-apiserver-authentication-reader
subjects:
  - kind: ServiceAccount
    name: cloud-controller-manager
    namespace: kube-system
{{- end -}}
