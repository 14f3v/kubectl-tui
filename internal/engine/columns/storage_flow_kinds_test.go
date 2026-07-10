package columns

import (
	"testing"
	"time"

	flowcontrolv1 "k8s.io/api/flowcontrol/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestFlowSchemasProject(t *testing.T) {
	proj := For("flowschemas")

	o := &flowcontrolv1.FlowSchema{
		ObjectMeta: meta("catch-all"),
		Spec: flowcontrolv1.FlowSchemaSpec{
			PriorityLevelConfiguration: flowcontrolv1.PriorityLevelConfigurationReference{Name: "workload-low"},
			MatchingPrecedence:         9000,
			DistinguisherMethod:        &flowcontrolv1.FlowDistinguisherMethod{Type: flowcontrolv1.FlowDistinguisherMethodByUserType},
		},
	}
	o.Namespace = ""
	row, ok := proj.Project(o, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if row.Namespace != "" {
		t.Fatalf("Namespace = %q, want empty (cluster-scoped)", row.Namespace)
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want neutral", row.Health)
	}
	if len(row.Cells) != 5 {
		t.Fatalf("cells = %d, want 5", len(row.Cells))
	}
	if got := find(t, proj, row, "NAME"); got.Text != "catch-all" || got.Role != RoleName {
		t.Fatalf("NAME = %q role=%v", got.Text, got.Role)
	}
	if got := find(t, proj, row, "PRIORITYLEVEL"); got.Text != "workload-low" {
		t.Fatalf("PRIORITYLEVEL = %q, want workload-low", got.Text)
	}
	if got := find(t, proj, row, "MATCHINGPRECEDENCE"); got.Text != "9000" {
		t.Fatalf("MATCHINGPRECEDENCE = %q, want 9000", got.Text)
	}
	if got := find(t, proj, row, "DISTINGUISHERMETHOD"); got.Text != "ByUser" {
		t.Fatalf("DISTINGUISHERMETHOD = %q, want ByUser", got.Text)
	}

	// Nil distinguisher method → dash.
	o2 := &flowcontrolv1.FlowSchema{ObjectMeta: meta("fs2")}
	row2, _ := proj.Project(o2, time.Now())
	if got := find(t, proj, row2, "DISTINGUISHERMETHOD"); got.Text != "—" {
		t.Fatalf("DISTINGUISHERMETHOD nil = %q, want dash", got.Text)
	}

	if _, ok := proj.Project(nil, time.Now()); ok {
		t.Fatal("expected nil projection to fail")
	}
}

func TestPriorityLevelConfigurationsProject(t *testing.T) {
	proj := For("prioritylevelconfigurations")

	shares := int32(30)
	o := &flowcontrolv1.PriorityLevelConfiguration{
		ObjectMeta: meta("workload-low"),
		Spec: flowcontrolv1.PriorityLevelConfigurationSpec{
			Type:    flowcontrolv1.PriorityLevelEnablementLimited,
			Limited: &flowcontrolv1.LimitedPriorityLevelConfiguration{NominalConcurrencyShares: &shares},
		},
	}
	o.Namespace = ""
	row, ok := proj.Project(o, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if row.Namespace != "" {
		t.Fatalf("Namespace = %q, want empty (cluster-scoped)", row.Namespace)
	}
	if len(row.Cells) != 4 {
		t.Fatalf("cells = %d, want 4", len(row.Cells))
	}
	if got := find(t, proj, row, "NAME"); got.Text != "workload-low" || got.Role != RoleName {
		t.Fatalf("NAME = %q role=%v", got.Text, got.Role)
	}
	if got := find(t, proj, row, "TYPE"); got.Text != "Limited" {
		t.Fatalf("TYPE = %q, want Limited", got.Text)
	}
	if got := find(t, proj, row, "NOMINALCONCURRENCYSHARES"); got.Text != "30" {
		t.Fatalf("NOMINALCONCURRENCYSHARES = %q, want 30", got.Text)
	}

	// Exempt type with nil Limited → dash for shares.
	o2 := &flowcontrolv1.PriorityLevelConfiguration{
		ObjectMeta: meta("exempt"),
		Spec:       flowcontrolv1.PriorityLevelConfigurationSpec{Type: flowcontrolv1.PriorityLevelEnablementExempt},
	}
	row2, _ := proj.Project(o2, time.Now())
	if got := find(t, proj, row2, "NOMINALCONCURRENCYSHARES"); got.Text != "—" {
		t.Fatalf("NOMINALCONCURRENCYSHARES nil = %q, want dash", got.Text)
	}

	if _, ok := proj.Project(&flowcontrolv1.PriorityLevelConfigurationList{}, time.Now()); ok {
		t.Fatal("expected wrong-type projection to fail")
	}
}

func TestCSIDriversProject(t *testing.T) {
	proj := For("csidrivers")

	attach := true
	podInfo := false
	o := &storagev1.CSIDriver{
		ObjectMeta: meta("ebs.csi.aws.com"),
		Spec: storagev1.CSIDriverSpec{
			AttachRequired:       &attach,
			PodInfoOnMount:       &podInfo,
			VolumeLifecycleModes: []storagev1.VolumeLifecycleMode{storagev1.VolumeLifecyclePersistent, storagev1.VolumeLifecycleEphemeral},
		},
	}
	o.Namespace = ""
	row, ok := proj.Project(o, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if row.Namespace != "" {
		t.Fatalf("Namespace = %q, want empty (cluster-scoped)", row.Namespace)
	}
	if len(row.Cells) != 5 {
		t.Fatalf("cells = %d, want 5", len(row.Cells))
	}
	if got := find(t, proj, row, "NAME"); got.Text != "ebs.csi.aws.com" || got.Role != RoleName {
		t.Fatalf("NAME = %q role=%v", got.Text, got.Role)
	}
	if got := find(t, proj, row, "ATTACHREQUIRED"); got.Text != "true" {
		t.Fatalf("ATTACHREQUIRED = %q, want true", got.Text)
	}
	if got := find(t, proj, row, "PODINFOONMOUNT"); got.Text != "false" {
		t.Fatalf("PODINFOONMOUNT = %q, want false", got.Text)
	}
	if got := find(t, proj, row, "MODES"); got.Text != "Persistent,Ephemeral" {
		t.Fatalf("MODES = %q, want Persistent,Ephemeral", got.Text)
	}

	// Nil pointers → dash.
	o2 := &storagev1.CSIDriver{ObjectMeta: meta("driver2")}
	row2, _ := proj.Project(o2, time.Now())
	if got := find(t, proj, row2, "ATTACHREQUIRED"); got.Text != "—" {
		t.Fatalf("ATTACHREQUIRED nil = %q, want dash", got.Text)
	}
	if got := find(t, proj, row2, "MODES"); got.Text != "—" {
		t.Fatalf("MODES empty = %q, want dash", got.Text)
	}

	if _, ok := proj.Project(&storagev1.CSIDriverList{}, time.Now()); ok {
		t.Fatal("expected wrong-type projection to fail")
	}
}

func TestCSINodesProject(t *testing.T) {
	proj := For("csinodes")
	o := &storagev1.CSINode{
		ObjectMeta: meta("node-1"),
		Spec: storagev1.CSINodeSpec{
			Drivers: []storagev1.CSINodeDriver{
				{Name: "ebs.csi.aws.com", NodeID: "i-123"},
				{Name: "efs.csi.aws.com", NodeID: "i-123"},
			},
		},
	}
	o.Namespace = ""
	row, ok := proj.Project(o, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if row.Namespace != "" {
		t.Fatalf("Namespace = %q, want empty (cluster-scoped)", row.Namespace)
	}
	if len(row.Cells) != 3 {
		t.Fatalf("cells = %d, want 3", len(row.Cells))
	}
	if got := find(t, proj, row, "NAME"); got.Text != "node-1" || got.Role != RoleName {
		t.Fatalf("NAME = %q role=%v", got.Text, got.Role)
	}
	if got := find(t, proj, row, "DRIVERS"); got.Text != "2" {
		t.Fatalf("DRIVERS = %q, want 2", got.Text)
	}

	if _, ok := proj.Project(&storagev1.CSINodeList{}, time.Now()); ok {
		t.Fatal("expected wrong-type projection to fail")
	}
}

func TestVolumeAttachmentsProject(t *testing.T) {
	proj := For("volumeattachments")

	pvName := "pv-xyz"
	o := &storagev1.VolumeAttachment{
		ObjectMeta: meta("csi-abc"),
		Spec: storagev1.VolumeAttachmentSpec{
			Attacher: "ebs.csi.aws.com",
			Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: &pvName},
			NodeName: "node-1",
		},
		Status: storagev1.VolumeAttachmentStatus{Attached: true},
	}
	o.Namespace = ""
	row, ok := proj.Project(o, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if row.Namespace != "" {
		t.Fatalf("Namespace = %q, want empty (cluster-scoped)", row.Namespace)
	}
	if len(row.Cells) != 6 {
		t.Fatalf("cells = %d, want 6", len(row.Cells))
	}
	if got := find(t, proj, row, "NAME"); got.Text != "csi-abc" || got.Role != RoleName {
		t.Fatalf("NAME = %q role=%v", got.Text, got.Role)
	}
	if got := find(t, proj, row, "ATTACHER"); got.Text != "ebs.csi.aws.com" {
		t.Fatalf("ATTACHER = %q", got.Text)
	}
	if got := find(t, proj, row, "PV"); got.Text != "pv-xyz" {
		t.Fatalf("PV = %q, want pv-xyz", got.Text)
	}
	if got := find(t, proj, row, "NODE"); got.Text != "node-1" {
		t.Fatalf("NODE = %q, want node-1", got.Text)
	}
	if got := find(t, proj, row, "ATTACHED"); got.Text != "true" {
		t.Fatalf("ATTACHED = %q, want true", got.Text)
	}

	// Nil PV name → dash; not-attached → false.
	o2 := &storagev1.VolumeAttachment{
		ObjectMeta: meta("csi-def"),
		Spec:       storagev1.VolumeAttachmentSpec{Attacher: "efs.csi.aws.com", NodeName: "node-2"},
	}
	row2, _ := proj.Project(o2, time.Now())
	if got := find(t, proj, row2, "PV"); got.Text != "—" {
		t.Fatalf("PV nil = %q, want dash", got.Text)
	}
	if got := find(t, proj, row2, "ATTACHED"); got.Text != "false" {
		t.Fatalf("ATTACHED = %q, want false", got.Text)
	}

	if _, ok := proj.Project(&storagev1.VolumeAttachmentList{}, time.Now()); ok {
		t.Fatal("expected wrong-type projection to fail")
	}
}

func TestCSIStorageCapacitiesProject(t *testing.T) {
	proj := For("csistoragecapacities")

	capacity := resource.MustParse("100Gi")
	o := &storagev1.CSIStorageCapacity{
		ObjectMeta:       meta("sc-cap"),
		StorageClassName: "gp3",
		Capacity:         &capacity,
	}
	row, ok := proj.Project(o, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	// Namespaced: metadata namespace preserved (meta() sets "default").
	if row.Namespace != "default" {
		t.Fatalf("Namespace = %q, want default (namespaced)", row.Namespace)
	}
	if len(row.Cells) != 4 {
		t.Fatalf("cells = %d, want 4", len(row.Cells))
	}
	if got := find(t, proj, row, "NAME"); got.Text != "sc-cap" || got.Role != RoleName {
		t.Fatalf("NAME = %q role=%v", got.Text, got.Role)
	}
	if got := find(t, proj, row, "STORAGECLASS"); got.Text != "gp3" {
		t.Fatalf("STORAGECLASS = %q, want gp3", got.Text)
	}
	if got := find(t, proj, row, "CAPACITY"); got.Text != "100Gi" {
		t.Fatalf("CAPACITY = %q, want 100Gi", got.Text)
	}

	// Nil capacity → dash.
	o2 := &storagev1.CSIStorageCapacity{ObjectMeta: meta("sc-cap2"), StorageClassName: "gp2"}
	row2, _ := proj.Project(o2, time.Now())
	if got := find(t, proj, row2, "CAPACITY"); got.Text != "—" {
		t.Fatalf("CAPACITY nil = %q, want dash", got.Text)
	}

	if _, ok := proj.Project(&storagev1.CSIStorageCapacityList{}, time.Now()); ok {
		t.Fatal("expected wrong-type projection to fail")
	}
}
