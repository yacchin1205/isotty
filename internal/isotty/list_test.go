package isotty

import (
	"errors"
	"reflect"
	"testing"

	vmcfg "github.com/yazawa/isotty/internal/isotty/vm"
)

func TestListRowsJoinsStatesWithInstances(t *testing.T) {
	states := []State{
		{ProjectPath: "/work/a", GCPProjectID: "demo-project", Zone: "us-central1-f", InstanceName: "isotty-aaa"},
		{ProjectPath: "/work/b", GCPProjectID: "other-project", Zone: "us-central1-f", InstanceName: "isotty-bbb"},
	}
	instances := map[string][]vmcfg.GCPInstance{
		"demo-project": {
			{Name: "isotty-aaa", Zone: "us-central1-f", Status: "RUNNING"},
			{Name: "isotty-ccc", Zone: "asia-northeast1-b", Status: "TERMINATED"},
		},
		"other-project": nil,
	}
	var asked []string
	rows, err := listRows(states, "demo-project", func(projectID string) ([]vmcfg.GCPInstance, error) {
		asked = append(asked, projectID)
		list, ok := instances[projectID]
		if !ok {
			return nil, errors.New("unexpected project " + projectID)
		}
		return list, nil
	})
	if err != nil {
		t.Fatalf("listRows() error = %v", err)
	}
	if !reflect.DeepEqual(asked, []string{"demo-project", "other-project"}) {
		t.Fatalf("asked = %v, want each project once", asked)
	}
	want := []listRow{
		{ID: "gcp:demo-project:asia-northeast1-b:isotty-ccc", Status: "TERMINATED", Project: "(no state on this host)"},
		{ID: "gcp:demo-project:us-central1-f:isotty-aaa", Status: "RUNNING", Project: "/work/a"},
		{ID: "gcp:other-project:us-central1-f:isotty-bbb", Status: "ABSENT", Project: "/work/b"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %#v, want %#v", rows, want)
	}
}

func TestListRowsAsksTheDefaultProjectWithoutStates(t *testing.T) {
	rows, err := listRows(nil, "demo-project", func(projectID string) ([]vmcfg.GCPInstance, error) {
		return []vmcfg.GCPInstance{{Name: "isotty-aaa", Zone: "us-central1-f", Status: "RUNNING"}}, nil
	})
	if err != nil {
		t.Fatalf("listRows() error = %v", err)
	}
	if len(rows) != 1 || rows[0].Project != "(no state on this host)" {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestListRowsFailsWhenGcloudFails(t *testing.T) {
	_, err := listRows(nil, "demo-project", func(projectID string) ([]vmcfg.GCPInstance, error) {
		return nil, errors.New("permission denied")
	})
	if err == nil {
		t.Fatal("listRows() error = nil, want the gcloud failure")
	}
}
