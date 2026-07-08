package view

func init() {
	Register("pods", []string{"po", "pod"}, "pods in the cluster", func(d Deps) Page {
		return newResourcePage("pods", "pods", d)
	})
}
