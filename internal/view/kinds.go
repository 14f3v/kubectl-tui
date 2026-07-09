package view

// Registration of the core resource pages. Each maps command aliases to a
// generic resource page over the kind's column projector. Adding a kind is a
// projector file plus one Register line here plus an engine factory.
func init() {
	Register("deployments", []string{"deploy", "deployments", "dp"}, "deployments in the cluster", func(d Deps) Page {
		return newResourcePage("deployments", "deployments", d)
	})
	Register("services", []string{"svc", "service", "services"}, "services in the cluster", func(d Deps) Page {
		return newResourcePage("services", "services", d)
	})
	Register("nodes", []string{"no", "node", "nodes"}, "cluster nodes", func(d Deps) Page {
		return newResourcePage("nodes", "nodes", d)
	})
	Register("namespaces", []string{"ns", "namespace", "namespaces"}, "namespaces", func(d Deps) Page {
		return newResourcePage("namespaces", "namespaces", d)
	})
	Register("events", []string{"ev", "event", "events"}, "recent cluster events", func(d Deps) Page {
		// Events open newest-first (LAST SEEN is column 0, sorted descending).
		return newResourcePage("events", "events", d, WithInitialSort(0, true))
	})

	// Workloads (#3).
	registerKind("statefulsets", []string{"sts", "statefulset", "statefulsets"}, "stateful sets")
	registerKind("daemonsets", []string{"ds", "daemonset", "daemonsets"}, "daemon sets")
	registerKind("replicasets", []string{"rs", "replicaset", "replicasets"}, "replica sets")
	registerKind("jobs", []string{"job", "jobs"}, "batch jobs")
	registerKind("cronjobs", []string{"cj", "cronjob", "cronjobs"}, "cron jobs")

	// Config & storage (#4) and secrets (#2).
	registerKind("configmaps", []string{"cm", "configmap", "configmaps"}, "config maps")
	registerKind("secrets", []string{"secret", "secrets"}, "secrets (values hidden; enter to reveal)")
	registerKind("persistentvolumeclaims", []string{"pvc", "persistentvolumeclaim", "persistentvolumeclaims"}, "persistent volume claims")
	registerKind("persistentvolumes", []string{"pv", "persistentvolume", "persistentvolumes"}, "persistent volumes")
	registerKind("storageclasses", []string{"sc", "storageclass", "storageclasses"}, "storage classes")

	// Networking (#5).
	registerKind("ingresses", []string{"ing", "ingress", "ingresses"}, "ingresses")
	registerKind("networkpolicies", []string{"netpol", "networkpolicy", "networkpolicies"}, "network policies")
	registerKind("endpointslices", []string{"eps", "endpointslice", "endpointslices"}, "endpoint slices")

	// RBAC (#6).
	registerKind("serviceaccounts", []string{"sa", "serviceaccount", "serviceaccounts"}, "service accounts")
	registerKind("roles", []string{"role", "roles"}, "RBAC roles")
	registerKind("rolebindings", []string{"rolebinding", "rolebindings"}, "RBAC role bindings")
	registerKind("clusterroles", []string{"clusterrole", "clusterroles"}, "RBAC cluster roles")
	registerKind("clusterrolebindings", []string{"crb", "clusterrolebinding", "clusterrolebindings"}, "RBAC cluster role bindings")

	// Autoscaling & policy (#7).
	registerKind("horizontalpodautoscalers", []string{"hpa", "horizontalpodautoscaler", "horizontalpodautoscalers"}, "horizontal pod autoscalers")
	registerKind("poddisruptionbudgets", []string{"pdb", "poddisruptionbudget", "poddisruptionbudgets"}, "pod disruption budgets")
	registerKind("resourcequotas", []string{"quota", "resourcequota", "resourcequotas"}, "resource quotas")
	registerKind("limitranges", []string{"limits", "limitrange", "limitranges"}, "limit ranges")

	// Certificate signing requests (#25) — approve/deny via a/x on the CSR view.
	registerKind("certificatesigningrequests", []string{"csr", "csrs", "certificatesigningrequest", "certificatesigningrequests"}, "certificate signing requests (a approve / x deny)")
}

// registerKind wires a generic resource page (title == kind) for kinds whose
// only customization is their column projector. Kinds needing options (initial
// sort, drill-ins beyond the shared enter handler) call Register directly.
func registerKind(kind string, aliases []string, desc string) {
	Register(kind, aliases, desc, func(d Deps) Page {
		return newResourcePage(kind, kind, d)
	})
}
