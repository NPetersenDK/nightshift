package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

type Node struct {
	ID                 int     `json:"id"`
	Type               string  `json:"type"`
	Title              string  `json:"title"`
	State              string  `json:"state"`
	Tags               string  `json:"tags,omitempty"`
	Description        string  `json:"description,omitempty"`
	AcceptanceCriteria string  `json:"acceptanceCriteria,omitempty"`
	Children           []*Node `json:"children,omitempty"`
}

var doneStates = []string{"Closed", "Done", "Removed", "Completed", "Resolved"}

// openWork fetches open Features (only those listed in config) with their User Stories and Tasks.
func openWork(d *DevOps) ([]*Node, error) {
	area, err := d.areaClause("[Source].[System.AreaPath]")
	if err != nil {
		return nil, err
	}
	quoted := make([]string, len(doneStates))
	for i, s := range doneStates {
		quoted[i] = "'" + s + "'"
	}
	wiql := fmt.Sprintf(`SELECT [System.Id] FROM WorkItemLinks
WHERE [Source].[System.TeamProject] = '%s'
  AND [Source].[System.WorkItemType] = 'Feature'
  AND [Source].[System.State] NOT IN (%s)
  %s
  AND [System.Links.LinkType] = 'System.LinkTypes.Hierarchy-Forward'
  AND [Target].[System.WorkItemType] <> 'Epic'
MODE (Recursive)`, d.t.Project.Name, strings.Join(quoted, ","), area)

	var q struct {
		Rels []struct {
			Source *struct{ ID int } `json:"source"`
			Target struct{ ID int }  `json:"target"`
		} `json:"workItemRelations"`
	}
	if err := d.do("POST", d.proj()+"/_apis/wit/wiql", map[string]string{"query": wiql}, "", "", &q); err != nil {
		return nil, err
	}
	idSet := map[int]bool{}
	for _, r := range q.Rels {
		if r.Source != nil {
			idSet[r.Source.ID] = true
		}
		idSet[r.Target.ID] = true
	}
	if len(idSet) == 0 {
		return nil, nil
	}
	ids := make([]int, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	const fields = "System.Id,System.WorkItemType,System.Title,System.State,System.Description,System.Tags,Microsoft.VSTS.Common.AcceptanceCriteria"
	nodes := map[int]*Node{}
	for i := 0; i < len(ids); i += 200 {
		end := min(i+200, len(ids))
		parts := make([]string, 0, end-i)
		for _, id := range ids[i:end] {
			parts = append(parts, fmt.Sprint(id))
		}
		var r struct {
			Value []WorkItem `json:"value"`
		}
		if err := d.do("GET", fmt.Sprintf("%s/_apis/wit/workitems?ids=%s&fields=%s", d.proj(), strings.Join(parts, ","), fields), nil, "", "", &r); err != nil {
			return nil, err
		}
		for i := range r.Value {
			w := &r.Value[i]
			nodes[w.ID] = &Node{
				ID: w.ID, Type: w.field("System.WorkItemType"), Title: w.field("System.Title"), State: w.field("System.State"),
				Tags: w.field("System.Tags"), Description: plainText(w.field("System.Description")),
				AcceptanceCriteria: plainText(w.field("Microsoft.VSTS.Common.AcceptanceCriteria")),
			}
		}
	}

	var roots []*Node
	for _, r := range q.Rels {
		t := nodes[r.Target.ID]
		if t == nil {
			continue
		}
		if r.Source != nil {
			if s := nodes[r.Source.ID]; s != nil {
				s.Children = append(s.Children, t)
			}
		} else if t.Type == "Feature" {
			roots = append(roots, t)
		}
	}
	var allowed []string
	for _, f := range d.t.Project.Features {
		allowed = append(allowed, f.Name)
	}
	if len(allowed) > 0 {
		roots = slices.DeleteFunc(roots, func(n *Node) bool { return !slices.Contains(allowed, n.Title) })
	}
	for _, r := range roots {
		dropDone(r)
	}
	return roots, nil
}

func dropDone(n *Node) {
	n.Children = slices.DeleteFunc(n.Children, func(c *Node) bool { return slices.Contains(doneStates, c.State) })
	for _, c := range n.Children {
		dropDone(c)
	}
}

var reWS = regexp.MustCompile(`\s*\n\s*`)

func nodeText(n *Node, depth int, sb *strings.Builder) {
	pad := strings.Repeat("  ", depth)
	tags := ""
	if n.Tags != "" {
		tags = " {" + n.Tags + "}"
	}
	fmt.Fprintf(sb, "%s- %s #%d %s [%s]%s\n", pad, n.Type, n.ID, n.Title, n.State, tags)
	if n.Description != "" {
		fmt.Fprintf(sb, "%s  desc: %s\n", pad, reWS.ReplaceAllString(n.Description, " / "))
	}
	if n.AcceptanceCriteria != "" {
		fmt.Fprintf(sb, "%s  ac: %s\n", pad, reWS.ReplaceAllString(n.AcceptanceCriteria, " / "))
	}
	for _, c := range n.Children {
		nodeText(c, depth+1, sb)
	}
}

func workText(roots []*Node) string {
	var sb strings.Builder
	for _, r := range roots {
		nodeText(r, 0, &sb)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func workJSON(roots []*Node) string {
	b, _ := json.Marshal(roots)
	return string(b)
}
