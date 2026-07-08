package view

func init() {
	Register("pods", []string{"po", "pod"}, func(d Deps) Page {
		return newResourcePage("pods", "pods", d)
	})
}
