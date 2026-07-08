package view

// Registration of the core resource pages. Each maps command aliases to a
// generic resource page over the kind's column projector. Adding a kind is a
// projector file plus one Register line here plus an engine factory.
func init() {
	Register("deployments", []string{"deploy", "deployments", "dp"}, func(d Deps) Page {
		return newResourcePage("deployments", "deployments", d)
	})
	Register("services", []string{"svc", "service", "services"}, func(d Deps) Page {
		return newResourcePage("services", "services", d)
	})
	Register("nodes", []string{"no", "node", "nodes"}, func(d Deps) Page {
		return newResourcePage("nodes", "nodes", d)
	})
	Register("namespaces", []string{"ns", "namespace", "namespaces"}, func(d Deps) Page {
		return newResourcePage("namespaces", "namespaces", d)
	})
	Register("events", []string{"ev", "event", "events"}, func(d Deps) Page {
		// Events open newest-first (LAST SEEN is column 0, sorted descending).
		return newResourcePage("events", "events", d, WithInitialSort(0, true))
	})
}
