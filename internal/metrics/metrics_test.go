package metrics

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

func TestFormatCPU(t *testing.T) {
	cases := map[int64]string{0: "0", 5: "5m", 142: "142m", 999: "999m", 1000: "1.0", 1500: "1.5"}
	for in, want := range cases {
		if got := FormatCPU(in); got != want {
			t.Errorf("FormatCPU(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatMem(t *testing.T) {
	mi := int64(1024 * 1024)
	cases := map[int64]string{318 * mi: "318Mi", 1536 * mi: "1.5Gi"}
	for in, want := range cases {
		if got := FormatMem(in); got != want {
			t.Errorf("FormatMem(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestPodUsageSumsContainers(t *testing.T) {
	pm := &metricsv1beta1.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Containers: []metricsv1beta1.ContainerMetrics{
			{Name: "app", Usage: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("200Mi"),
			}},
			{Name: "sidecar", Usage: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("42m"),
				corev1.ResourceMemory: resource.MustParse("118Mi"),
			}},
		},
	}
	u := podUsage(pm)
	if u.CPUMillis != 142 {
		t.Fatalf("CPU = %dm, want 142m", u.CPUMillis)
	}
	if u.MemBytes != 318*1024*1024 {
		t.Fatalf("MEM = %d, want %d", u.MemBytes, 318*1024*1024)
	}
}

func TestPct(t *testing.T) {
	if got := pct(2000, 4000); got < 49.9 || got > 50.1 {
		t.Fatalf("pct(2000,4000) = %.2f, want 50", got)
	}
	if got := pct(10, 0); got != 0 {
		t.Fatalf("pct with zero total = %.2f, want 0", got)
	}
}
