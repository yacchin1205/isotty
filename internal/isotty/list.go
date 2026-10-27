package isotty

import (
	"errors"
	"flag"
	"fmt"
	"slices"
	"sort"
	"text/tabwriter"

	vmcfg "github.com/yazawa/isotty/internal/isotty/vm"
)

type listRow struct {
	ID      string
	Status  string
	Project string
}

const (
	listStatusAbsent   = "ABSENT"
	listProjectNoState = "(no state on this host)"
)

func (a *App) runList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("list does not accept arguments")
	}

	states, err := ListStates()
	if err != nil {
		return err
	}
	defaultProjectID, err := resolveSetting("ISOTTY_GCP_PROJECT", []string{"gcloud", "config", "get-value", "project"})
	if err != nil {
		return fmt.Errorf("resolve GCP project: %w", err)
	}
	rows, err := listRows(states, defaultProjectID, vmcfg.ListGCPInstances)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tPROJECT")
	for _, row := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\n", row.ID, row.Status, row.Project)
	}
	return w.Flush()
}

// listRows joins the host's project states with the isotty VMs of their GCP
// projects and of the default one: a state whose VM is gone shows as ABSENT,
// and a VM no state here points at, brought up from another host or left
// behind by a lost state, shows without a project.
func listRows(states []State, defaultProjectID string, instances func(projectID string) ([]vmcfg.GCPInstance, error)) ([]listRow, error) {
	projectIDs := []string{defaultProjectID}
	for _, state := range states {
		if !slices.Contains(projectIDs, state.GCPProjectID) {
			projectIDs = append(projectIDs, state.GCPProjectID)
		}
	}
	sort.Strings(projectIDs)

	found := map[string]string{}
	var rows []listRow
	for _, projectID := range projectIDs {
		list, err := instances(projectID)
		if err != nil {
			return nil, fmt.Errorf("list instances of %s: %w", projectID, err)
		}
		for _, instance := range list {
			id := vmcfg.FormatGCPID(projectID, instance.Zone, instance.Name)
			found[id] = instance.Status
			rows = append(rows, listRow{ID: id, Status: instance.Status, Project: listProjectNoState})
		}
	}
	for _, state := range states {
		id := vmcfg.FormatGCPID(state.GCPProjectID, state.Zone, state.InstanceName)
		if _, ok := found[id]; ok {
			for i := range rows {
				if rows[i].ID == id {
					rows[i].Project = state.ProjectPath
				}
			}
			continue
		}
		rows = append(rows, listRow{ID: id, Status: listStatusAbsent, Project: state.ProjectPath})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}
