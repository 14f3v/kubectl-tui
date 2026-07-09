package columns

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestConfigMapsProject(t *testing.T) {
	proj := For("configmaps")
	cm := &corev1.ConfigMap{
		ObjectMeta: meta("x"),
		Data: map[string]string{
			"app.conf": "a=1",
			"log.conf": "b=2",
		},
		BinaryData: map[string][]byte{
			"cert.der": []byte{0x01, 0x02},
		},
	}
	row, ok := proj.Project(cm, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "DATA").Text; got != "3" {
		t.Fatalf("DATA = %q, want %q", got, "3")
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want %v", row.Health, StatusNeutral)
	}
}

func TestPVCsProject_Bound(t *testing.T) {
	proj := For("persistentvolumeclaims")
	sc := "fast"
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: meta("x"),
		Spec: corev1.PersistentVolumeClaimSpec{
			VolumeName:       "pv-123",
			StorageClassName: &sc,
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase:       corev1.ClaimBound,
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Capacity:    corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
		},
	}
	row, ok := proj.Project(pvc, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "STATUS").Text; got != "Bound" {
		t.Fatalf("STATUS = %q, want %q", got, "Bound")
	}
	if got := find(t, proj, row, "CAPACITY").Text; got != "10Gi" {
		t.Fatalf("CAPACITY = %q, want %q", got, "10Gi")
	}
	if got := find(t, proj, row, "ACCESS-MODES").Text; got != "RWO" {
		t.Fatalf("ACCESS-MODES = %q, want %q", got, "RWO")
	}
	if got := find(t, proj, row, "VOLUME").Text; got != "pv-123" {
		t.Fatalf("VOLUME = %q, want %q", got, "pv-123")
	}
	if got := find(t, proj, row, "STORAGECLASS").Text; got != "fast" {
		t.Fatalf("STORAGECLASS = %q, want %q", got, "fast")
	}
	if row.Health != StatusOK {
		t.Fatalf("health = %v, want %v", row.Health, StatusOK)
	}
}

func TestPVCsProject_Pending(t *testing.T) {
	proj := For("persistentvolumeclaims")
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: meta("x"),
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
	row, ok := proj.Project(pvc, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "CAPACITY").Text; got != "—" {
		t.Fatalf("CAPACITY = %q, want dash", got)
	}
	if got := find(t, proj, row, "VOLUME").Text; got != "—" {
		t.Fatalf("VOLUME = %q, want dash", got)
	}
	if got := find(t, proj, row, "STORAGECLASS").Text; got != "—" {
		t.Fatalf("STORAGECLASS = %q, want dash", got)
	}
	if row.Health != StatusWarn {
		t.Fatalf("health = %v, want %v", row.Health, StatusWarn)
	}
}

func TestPVsProject_Bound(t *testing.T) {
	proj := For("persistentvolumes")
	pv := &corev1.PersistentVolume{
		ObjectMeta: meta("pv-x"),
		Spec: corev1.PersistentVolumeSpec{
			Capacity:                      corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")},
			AccessModes:                   []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany, corev1.ReadOnlyMany},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
			StorageClassName:              "slow",
			ClaimRef:                      &corev1.ObjectReference{Namespace: "default", Name: "myclaim"},
		},
		Status: corev1.PersistentVolumeStatus{Phase: corev1.VolumeBound},
	}
	row, ok := proj.Project(pv, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "CAPACITY").Text; got != "5Gi" {
		t.Fatalf("CAPACITY = %q, want %q", got, "5Gi")
	}
	if got := find(t, proj, row, "ACCESS-MODES").Text; got != "RWX,ROX" {
		t.Fatalf("ACCESS-MODES = %q, want %q", got, "RWX,ROX")
	}
	if got := find(t, proj, row, "RECLAIM").Text; got != "Retain" {
		t.Fatalf("RECLAIM = %q, want %q", got, "Retain")
	}
	if got := find(t, proj, row, "STATUS").Text; got != "Bound" {
		t.Fatalf("STATUS = %q, want %q", got, "Bound")
	}
	if got := find(t, proj, row, "CLAIM").Text; got != "default/myclaim" {
		t.Fatalf("CLAIM = %q, want %q", got, "default/myclaim")
	}
	if got := find(t, proj, row, "STORAGECLASS").Text; got != "slow" {
		t.Fatalf("STORAGECLASS = %q, want %q", got, "slow")
	}
	if row.Health != StatusOK {
		t.Fatalf("health = %v, want %v", row.Health, StatusOK)
	}
}

func TestPVsProject_Released(t *testing.T) {
	proj := For("persistentvolumes")
	pv := &corev1.PersistentVolume{
		ObjectMeta: meta("pv-y"),
		Status:     corev1.PersistentVolumeStatus{Phase: corev1.VolumeReleased},
	}
	row, ok := proj.Project(pv, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "CAPACITY").Text; got != "—" {
		t.Fatalf("CAPACITY = %q, want dash", got)
	}
	if got := find(t, proj, row, "CLAIM").Text; got != "—" {
		t.Fatalf("CLAIM = %q, want dash", got)
	}
	if got := find(t, proj, row, "STORAGECLASS").Text; got != "—" {
		t.Fatalf("STORAGECLASS = %q, want dash", got)
	}
	if row.Health != StatusWarn {
		t.Fatalf("health = %v, want %v", row.Health, StatusWarn)
	}
}

func TestStorageClassesProject_Default(t *testing.T) {
	proj := For("storageclasses")
	reclaim := corev1.PersistentVolumeReclaimDelete
	binding := storagev1.VolumeBindingWaitForFirstConsumer
	m := meta("standard")
	m.Annotations = map[string]string{"storageclass.kubernetes.io/is-default-class": "true"}
	sc := &storagev1.StorageClass{
		ObjectMeta:        m,
		Provisioner:       "rancher.io/local-path",
		ReclaimPolicy:     &reclaim,
		VolumeBindingMode: &binding,
	}
	row, ok := proj.Project(sc, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "NAME").Text; got != "standard (default)" {
		t.Fatalf("NAME = %q, want %q", got, "standard (default)")
	}
	if got := find(t, proj, row, "PROVISIONER").Text; got != "rancher.io/local-path" {
		t.Fatalf("PROVISIONER = %q, want %q", got, "rancher.io/local-path")
	}
	if got := find(t, proj, row, "RECLAIMPOLICY").Text; got != "Delete" {
		t.Fatalf("RECLAIMPOLICY = %q, want %q", got, "Delete")
	}
	if got := find(t, proj, row, "VOLUMEBINDINGMODE").Text; got != "WaitForFirstConsumer" {
		t.Fatalf("VOLUMEBINDINGMODE = %q, want %q", got, "WaitForFirstConsumer")
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want %v", row.Health, StatusNeutral)
	}
}

func TestStorageClassesProject_Nils(t *testing.T) {
	proj := For("storageclasses")
	sc := &storagev1.StorageClass{
		ObjectMeta:  meta("plain"),
		Provisioner: "example.com/prov",
	}
	row, ok := proj.Project(sc, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "NAME").Text; got != "plain" {
		t.Fatalf("NAME = %q, want %q", got, "plain")
	}
	if got := find(t, proj, row, "RECLAIMPOLICY").Text; got != "—" {
		t.Fatalf("RECLAIMPOLICY = %q, want dash", got)
	}
	if got := find(t, proj, row, "VOLUMEBINDINGMODE").Text; got != "—" {
		t.Fatalf("VOLUMEBINDINGMODE = %q, want dash", got)
	}
}
