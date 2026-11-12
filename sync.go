package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// syncFeatures ensures the Features in config exist, are open, and carry keep-open + repo:<alias> tags
// (and the team's default area path when a team is set).
func syncFeatures(d *DevOps) ([]string, error) {
	defs := d.t.Project.Features
	if len(defs) == 0 {
		return nil, nil
	}
	area := ""
	if d.t.Project.Team != "" {
		ta, err := d.teamAreas()
		if err != nil {
			return nil, err
		}
		area = ta.Default
	}
	var q struct {
		WorkItems []struct{ ID int } `json:"workItems"`
	}
	wiql := fmt.Sprintf("SELECT [System.Id] FROM WorkItems WHERE [System.TeamProject] = '%s' AND [System.WorkItemType] = 'Feature' AND [System.State] NOT IN ('Closed','Done','Removed','Completed')", d.t.Project.Name)
	if err := d.do("POST", d.proj()+"/_apis/wit/wiql", map[string]string{"query": wiql}, "", "", &q); err != nil {
		return nil, err
	}
	existing := map[string]*WorkItem{}
	if len(q.WorkItems) > 0 {
		ids := make([]string, 0, len(q.WorkItems))
		for _, w := range q.WorkItems[:min(len(q.WorkItems), 200)] {
			ids = append(ids, fmt.Sprint(w.ID))
		}
		var r struct {
			Value []WorkItem `json:"value"`
		}
		if err := d.do("GET", fmt.Sprintf("%s/_apis/wit/workitems?ids=%s&fields=System.Title,System.Tags,System.AreaPath", d.proj(), strings.Join(ids, ",")), nil, "", "", &r); err != nil {
			return nil, err
		}
		for i := range r.Value {
			existing[r.Value[i].field("System.Title")] = &r.Value[i]
		}
	}

	var out []string
	for _, def := range defs {
		want := []string{"keep-open"}
		for _, r := range def.Repos {
			want = append(want, "repo:"+r)
		}
		w := existing[def.Name]
		if w == nil {
			ops := []map[string]any{
				{"op": "add", "path": "/fields/System.Title", "value": def.Name},
				{"op": "add", "path": "/fields/System.Tags", "value": strings.Join(want, "; ")},
			}
			if area != "" {
				ops = append(ops, map[string]any{"op": "add", "path": "/fields/System.AreaPath", "value": area})
			}
			var created WorkItem
			j, _ := json.Marshal(ops)
			if err := d.do("POST", d.proj()+"/_apis/wit/workitems/$Feature", j, "application/json-patch+json", "", &created); err != nil {
				return out, err
			}
			out = append(out, fmt.Sprintf("created #%d %s", created.ID, def.Name))
			continue
		}
		var have []string
		for _, t := range strings.Split(w.field("System.Tags"), ";") {
			if t = strings.TrimSpace(t); t != "" {
				have = append(have, t)
			}
		}
		var missing []string
		for _, t := range want {
			if !slices.Contains(have, t) {
				missing = append(missing, t)
			}
		}
		var ops []map[string]any
		if len(missing) > 0 {
			ops = append(ops, map[string]any{"op": "add", "path": "/fields/System.Tags", "value": strings.Join(append(have, missing...), "; ")})
		}
		if area != "" && w.field("System.AreaPath") != area {
			ops = append(ops, map[string]any{"op": "add", "path": "/fields/System.AreaPath", "value": area})
		}
		if len(ops) > 0 {
			if err := d.patch(w.ID, ops); err != nil {
				return out, err
			}
			out = append(out, fmt.Sprintf("updated #%d %s", w.ID, def.Name))
		}
	}
	return out, nil
}
