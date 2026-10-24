package opeco

import (
	"os"
	"strings"
	"testing"
)

func TestAllocateQRPortIsStablePerProjectAndDistinctAcrossProjects(t *testing.T) {
	home := t.TempDir()
	first, err := AllocateQRPort(home, "aaaa")
	if err != nil {
		t.Fatal(err)
	}
	if first != 61002 {
		t.Fatalf("first port = %d", first)
	}
	again, err := AllocateQRPort(home, "aaaa")
	if err != nil || again != first {
		t.Fatalf("second allocation for the same project = %d, %v", again, err)
	}
	second, err := AllocateQRPort(home, "bbbb")
	if err != nil || second != 61003 {
		t.Fatalf("port for another project = %d, %v", second, err)
	}

	loaded, err := LoadQRPort(home, "bbbb")
	if err != nil || loaded != second {
		t.Fatalf("loaded = %d, %v", loaded, err)
	}
	if err := ReleaseQRPort(home, "aaaa"); err != nil {
		t.Fatal(err)
	}
	reused, err := AllocateQRPort(home, "cccc")
	if err != nil || reused != 61002 {
		t.Fatalf("port after release = %d, %v", reused, err)
	}
	if _, err := LoadQRPort(home, "aaaa"); err == nil || !strings.Contains(err.Error(), "isotty up") {
		t.Fatalf("load after release: %v", err)
	}
	if err := ReleaseQRPort(home, "aaaa"); err != nil {
		t.Fatalf("second release must be a no-op: %v", err)
	}
}

func TestAllocateQRPortRejectsCorruptRecords(t *testing.T) {
	home := t.TempDir()
	if _, err := AllocateQRPort(home, "aaaa"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(RecordPath(home, "aaaa"), []byte("qr_port: 80\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AllocateQRPort(home, "bbbb"); err == nil {
		t.Fatal("a record outside the port range must fail the allocation")
	}
	if err := os.WriteFile(RecordPath(home, "aaaa"), []byte("port: 61002\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadQRPort(home, "aaaa"); err == nil {
		t.Fatal("an unknown field must fail the load")
	}
}
