package columns

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func meta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name: name, Namespace: "default", UID: types.UID("uid-" + name), ResourceVersion: "1",
		CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Hour)),
	}
}

func find(t *testing.T, proj Projector, row Row, title string) Cell {
	t.Helper()
	for i, c := range proj.Columns() {
		if c.Title == title {
			return row.Cells[i]
		}
	}
	t.Fatalf("no column %q", title)
	return Cell{}
}

func TestDeploymentsProject(t *testing.T) {
	proj := For("deployments")
	three := int32(3)
	cases := []struct {
		name          string
		desired       int32
		ready         int32
		wantReadyText string
		wantHealth    StatusClass
	}{
		{"healthy", 3, 3, "3/3", StatusOK},
		{"degraded", 3, 1, "1/3", StatusWarn},
		{"down", 3, 0, "0/3", StatusError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &appsv1.Deployment{
				ObjectMeta: meta(tc.name),
				Spec:       appsv1.DeploymentSpec{Replicas: &three},
				Status:     appsv1.DeploymentStatus{ReadyReplicas: tc.ready, AvailableReplicas: tc.ready, UpdatedReplicas: tc.ready},
			}
			d.Spec.Replicas = &tc.desired
			row, ok := proj.Project(d, time.Now())
			if !ok {
				t.Fatal("projection failed")
			}
			if got := find(t, proj, row, "READY"); got.Text != tc.wantReadyText {
				t.Fatalf("READY = %q, want %q", got.Text, tc.wantReadyText)
			}
			if row.Health != tc.wantHealth {
				t.Fatalf("health = %v, want %v", row.Health, tc.wantHealth)
			}
		})
	}
}

func TestServicesProject_Ports(t *testing.T) {
	proj := For("services")
	svc := &corev1.Service{
		ObjectMeta: meta("web"),
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeNodePort,
			ClusterIP: "10.96.0.1",
			Ports: []corev1.ServicePort{
				{Port: 80, NodePort: 30080, Protocol: corev1.ProtocolTCP},
				{Port: 443, Protocol: corev1.ProtocolTCP},
			},
		},
	}
	row, _ := proj.Project(svc, time.Now())
	if got := find(t, proj, row, "PORTS"); got.Text != "80:30080/TCP,443/TCP" {
		t.Fatalf("PORTS = %q", got.Text)
	}
	if got := find(t, proj, row, "TYPE"); got.Text != "NodePort" {
		t.Fatalf("TYPE = %q", got.Text)
	}
}

func TestNodesProject_Status(t *testing.T) {
	proj := For("nodes")
	mkNode := func(ready bool, cordoned bool) *corev1.Node {
		st := corev1.ConditionFalse
		if ready {
			st = corev1.ConditionTrue
		}
		return &corev1.Node{
			ObjectMeta: meta("n1"),
			Spec:       corev1.NodeSpec{Unschedulable: cordoned},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: st}},
				NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: "v1.29.4"},
			},
		}
	}
	row, _ := proj.Project(mkNode(true, false), time.Now())
	if got := find(t, proj, row, "STATUS"); got.Text != "Ready" || row.Health != StatusOK {
		t.Fatalf("ready node STATUS=%q health=%v", got.Text, row.Health)
	}
	row, _ = proj.Project(mkNode(true, true), time.Now())
	if got := find(t, proj, row, "STATUS"); got.Text != "Ready,SchedulingDisabled" || row.Health != StatusWarn {
		t.Fatalf("cordoned node STATUS=%q health=%v", got.Text, row.Health)
	}
	row, _ = proj.Project(mkNode(false, false), time.Now())
	if row.Health != StatusError {
		t.Fatalf("notready node health=%v, want error", row.Health)
	}
}

func TestNamespacesProject(t *testing.T) {
	proj := For("namespaces")
	active := &corev1.Namespace{ObjectMeta: meta("prod"), Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive}}
	row, _ := proj.Project(active, time.Now())
	if row.Health != StatusOK {
		t.Fatalf("active ns health = %v, want ok", row.Health)
	}
	term := &corev1.Namespace{ObjectMeta: meta("gone"), Status: corev1.NamespaceStatus{Phase: corev1.NamespaceTerminating}}
	row, _ = proj.Project(term, time.Now())
	if row.Health != StatusWarn {
		t.Fatalf("terminating ns health = %v, want warn", row.Health)
	}
}

func TestEventsProject(t *testing.T) {
	proj := For("events")
	e := &corev1.Event{
		ObjectMeta:     meta("evt"),
		Type:           corev1.EventTypeWarning,
		Reason:         "BackOff",
		Count:          5,
		Message:        "Back-off\nrestarting failed container",
		LastTimestamp:  metav1.NewTime(time.Now().Add(-1 * time.Minute)),
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "web-0"},
	}
	row, _ := proj.Project(e, time.Now())
	if row.Health != StatusWarn {
		t.Fatalf("warning event health = %v, want warn", row.Health)
	}
	if got := find(t, proj, row, "OBJECT"); got.Text != "Pod/web-0" {
		t.Fatalf("OBJECT = %q", got.Text)
	}
	// Message must be single-lined.
	if got := find(t, proj, row, "MESSAGE"); got.Text != "Back-off restarting failed container" {
		t.Fatalf("MESSAGE = %q (newline not collapsed)", got.Text)
	}
}
