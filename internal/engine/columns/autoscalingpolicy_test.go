package columns

import (
	"testing"
	"time"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestHPAProject(t *testing.T) {
	proj := For("horizontalpodautoscalers")
	min := int32(2)
	util := int32(80)
	cases := []struct {
		name        string
		current     int32
		max         int32
		wantTargets string
		wantHealth  StatusClass
	}{
		{"steady", 3, 10, "cpu:80%", StatusOK},
		{"maxed", 10, 10, "cpu:80%", StatusWarn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hpa := &autoscalingv2.HorizontalPodAutoscaler{
				ObjectMeta: meta(tc.name),
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{Kind: "Deployment", Name: "web"},
					MinReplicas:    &min,
					MaxReplicas:    tc.max,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceCPU,
								Target: autoscalingv2.MetricTarget{
									AverageUtilization: &util,
								},
							},
						},
					},
				},
				Status: autoscalingv2.HorizontalPodAutoscalerStatus{CurrentReplicas: tc.current},
			}
			row, ok := proj.Project(hpa, time.Now())
			if !ok {
				t.Fatal("projection failed")
			}
			if got := find(t, proj, row, "REFERENCE"); got.Text != "Deployment/web" {
				t.Fatalf("REFERENCE = %q", got.Text)
			}
			if got := find(t, proj, row, "TARGETS"); got.Text != tc.wantTargets {
				t.Fatalf("TARGETS = %q, want %q", got.Text, tc.wantTargets)
			}
			if got := find(t, proj, row, "MINPODS"); got.Text != "2" {
				t.Fatalf("MINPODS = %q", got.Text)
			}
			if row.Health != tc.wantHealth {
				t.Fatalf("health = %v, want %v", row.Health, tc.wantHealth)
			}
		})
	}
}

func TestHPAProject_NoMetrics(t *testing.T) {
	proj := For("horizontalpodautoscalers")
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: meta("nometrics"),
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{Kind: "Deployment", Name: "api"},
			MaxReplicas:    5,
		},
		Status: autoscalingv2.HorizontalPodAutoscalerStatus{CurrentReplicas: 1},
	}
	row, ok := proj.Project(hpa, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "TARGETS"); got.Text != "<none>" {
		t.Fatalf("TARGETS = %q, want <none>", got.Text)
	}
	if got := find(t, proj, row, "MINPODS"); got.Text != "1" {
		t.Fatalf("MINPODS = %q, want 1 (nil MinReplicas)", got.Text)
	}
}

func TestPDBProject(t *testing.T) {
	proj := For("poddisruptionbudgets")
	minAvail := intstr.FromInt32(2)
	cases := []struct {
		name        string
		allowed     int32
		wantAllowed string
		wantHealth  StatusClass
	}{
		{"ok", 3, "3", StatusOK},
		{"blocked", 0, "0", StatusWarn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pdb := &policyv1.PodDisruptionBudget{
				ObjectMeta: meta(tc.name),
				Spec:       policyv1.PodDisruptionBudgetSpec{MinAvailable: &minAvail},
				Status:     policyv1.PodDisruptionBudgetStatus{DisruptionsAllowed: tc.allowed},
			}
			row, ok := proj.Project(pdb, time.Now())
			if !ok {
				t.Fatal("projection failed")
			}
			if got := find(t, proj, row, "MIN-AVAILABLE"); got.Text != "2" {
				t.Fatalf("MIN-AVAILABLE = %q", got.Text)
			}
			if got := find(t, proj, row, "MAX-UNAVAILABLE"); got.Text != "—" {
				t.Fatalf("MAX-UNAVAILABLE = %q, want dash", got.Text)
			}
			if got := find(t, proj, row, "ALLOWED-DISRUPTIONS"); got.Text != tc.wantAllowed {
				t.Fatalf("ALLOWED-DISRUPTIONS = %q, want %q", got.Text, tc.wantAllowed)
			}
			if row.Health != tc.wantHealth {
				t.Fatalf("health = %v, want %v", row.Health, tc.wantHealth)
			}
		})
	}
}

func TestResourceQuotasProject(t *testing.T) {
	proj := For("resourcequotas")
	rq := &corev1.ResourceQuota{
		ObjectMeta: meta("rq"),
		Spec: corev1.ResourceQuotaSpec{
			Hard: corev1.ResourceList{
				corev1.ResourceRequestsMemory: resource.MustParse("8Gi"),
			},
		},
		Status: corev1.ResourceQuotaStatus{
			Hard: corev1.ResourceList{
				corev1.ResourceRequestsCPU: resource.MustParse("4"),
			},
		},
	}
	row, ok := proj.Project(rq, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "REQUEST-CPU"); got.Text != "4" {
		t.Fatalf("REQUEST-CPU = %q, want 4 (from status.hard)", got.Text)
	}
	if got := find(t, proj, row, "REQUEST-MEM"); got.Text != "8Gi" {
		t.Fatalf("REQUEST-MEM = %q, want 8Gi (fallback to spec.hard)", got.Text)
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want neutral", row.Health)
	}
}

func TestResourceQuotasProject_Absent(t *testing.T) {
	proj := For("resourcequotas")
	rq := &corev1.ResourceQuota{ObjectMeta: meta("empty")}
	row, ok := proj.Project(rq, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "REQUEST-CPU"); got.Text != "—" {
		t.Fatalf("REQUEST-CPU = %q, want dash", got.Text)
	}
}

func TestLimitRangesProject(t *testing.T) {
	proj := For("limitranges")
	lr := &corev1.LimitRange{
		ObjectMeta: meta("lr"),
		Spec: corev1.LimitRangeSpec{
			Limits: []corev1.LimitRangeItem{
				{Type: corev1.LimitTypeContainer},
				{Type: corev1.LimitTypePod},
				{Type: corev1.LimitTypeContainer},
			},
		},
	}
	row, ok := proj.Project(lr, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "TYPES"); got.Text != "Container,Pod" {
		t.Fatalf("TYPES = %q, want Container,Pod (distinct)", got.Text)
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want neutral", row.Health)
	}
}

func TestLimitRangesProject_Empty(t *testing.T) {
	proj := For("limitranges")
	lr := &corev1.LimitRange{ObjectMeta: meta("empty-lr")}
	row, ok := proj.Project(lr, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "TYPES"); got.Text != "—" {
		t.Fatalf("TYPES = %q, want dash", got.Text)
	}
}
