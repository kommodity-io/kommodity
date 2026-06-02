{{/*
Cloud-controller-manager manifests for the Kubevirt provider, applied to the
workload cluster via ClusterResourceSet. All resources land in kube-system.

Source: github.com/kubevirt/cloud-provider-kubevirt @ v0.6.0
        (37201f6ce1da7d03fa5322885cb1b6991c673d56)
Composed from config/{manager,rbac}/*.yaml with namespace overrides to
kube-system and the downstream kubeconfig Secret renamed to
kubevirt-cloud-controller-manager.
*/}}
{{- define "kommodity.ccm.manifest.Kubevirt" -}}
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
  name: kccm
rules:
  - apiGroups:
      - ""
    resources:
      - nodes
    verbs:
      - get
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kccm
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kccm
subjects:
  - kind: ServiceAccount
    name: cloud-controller-manager
    namespace: kube-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: kccm
  namespace: kube-system
rules:
  - apiGroups:
      - kubevirt.io
    resources:
      - virtualmachines
    verbs:
      - get
      - watch
      - list
  - apiGroups:
      - kubevirt.io
    resources:
      - virtualmachineinstances
    verbs:
      - get
      - watch
      - list
      - update
  - apiGroups:
      - ""
    resources:
      - pods
    verbs:
      - get
      - list
      - watch
  - apiGroups:
      - ""
    resources:
      - services
    verbs:
      - "*"
  - apiGroups:
      - ""
    resources:
      - nodes
    verbs:
      - get
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: kccm-sa
  namespace: kube-system
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: kccm
subjects:
  - kind: ServiceAccount
    name: cloud-controller-manager
    namespace: kube-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: kccm-extension-apiserver-authentication-reader
  namespace: kube-system
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: extension-apiserver-authentication-reader
subjects:
  - kind: ServiceAccount
    name: cloud-controller-manager
    namespace: kube-system
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: cloud-config
  namespace: kube-system
data:
  cloud-config: |
    namespace: {{ .Values.kommodity.provider.config.infraClusterNamespace }}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kubevirt-cloud-controller-manager
  namespace: kube-system
  labels:
    k8s-app: kubevirt-cloud-controller-manager
spec:
  replicas: 1
  selector:
    matchLabels:
      k8s-app: kubevirt-cloud-controller-manager
  template:
    metadata:
      labels:
        k8s-app: kubevirt-cloud-controller-manager
    spec:
      serviceAccountName: cloud-controller-manager
      nodeSelector:
        node-role.kubernetes.io/control-plane: ""
      tolerations:
        - key: node.cloudprovider.kubernetes.io/uninitialized
          value: "true"
          effect: NoSchedule
        - key: node-role.kubernetes.io/control-plane
          effect: NoSchedule
      containers:
        - name: kubevirt-cloud-controller-manager
          image: {{ .Values.kommodity.provider.cloudControllerManager.image.repository }}:{{ .Values.kommodity.provider.cloudControllerManager.image.tag }}
          imagePullPolicy: IfNotPresent
          command:
            - /bin/kubevirt-cloud-controller-manager
          args:
            - --cloud-provider=kubevirt
            - --cloud-config=/etc/cloud/cloud-config
            - --kubeconfig=/etc/kubernetes/kubeconfig/value
          securityContext:
            privileged: true
          resources:
            requests:
              cpu: 100m
          volumeMounts:
            - mountPath: /etc/kubernetes/kubeconfig
              name: kubeconfig
              readOnly: true
            - mountPath: /etc/cloud
              name: cloud-config
              readOnly: true
      volumes:
        - name: cloud-config
          configMap:
            name: cloud-config
        - name: kubeconfig
          secret:
            secretName: kubevirt-cloud-controller-manager
            items:
              - key: kubeconfig
                path: value
{{- end -}}
