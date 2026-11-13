package main

import (
	"fmt"
	"regexp"
	"strings"
)

var reKeepOpen = regexp.MustCompile(`(^|;\s*)keep-open(\s*;|$)`)

type CloseOpts struct {
	ID          int
	Comment     string
	State       string
	CommentOnly bool
	NoCascade   bool
	CloseAs     string // state category: Resolved or Completed (falls back to Completed when the type has no Resolved state)
	Webhook     Webhook
}

// closeItem comments and closes a work item, then closes parents whose children are all closed
// (never Features tagged keep-open). After the item itself closes it posts the webhook, if configured.
// Returns terse status lines.
func closeItem(d *DevOps, o CloseOpts) ([]string, error) {
	if o.CommentOnly {
		return []string{"ok"}, d.comment(o.ID, o.Comment)
	}
	get := func(id int) (*WorkItem, error) {
		var w WorkItem
		err := d.do("GET", fmt.Sprintf("%s/_apis/wit/workitems/%d?$expand=relations", d.proj(), id), nil, "", "", &w)
		return &w, err
	}
	isDone := func(id int) (bool, error) {
		s, err := d.itemState(id)
		return s.done(), err
	}
	categories := []string{"Completed"}
	if o.CloseAs != "Completed" {
		categories = []string{"Resolved", "Completed"}
	}
	closeOne := func(w *WorkItem, text, target string) (string, error) {
		typ := w.field("System.WorkItemType")
		for _, cat := range categories {
			if target != "" {
				break
			}
			t, err := d.stateName(typ, cat)
			if err != nil {
				return "", err
			}
			target = t
		}
		if target == "" {
			return "", fmt.Errorf("no %s state for %s, pass --state", strings.Join(categories, "/"), typ)
		}
		if err := d.comment(w.ID, text); err != nil {
			return "", err
		}
		if err := d.setState(w.ID, target); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s #%d", strings.ToLower(target), w.ID), nil
	}

	var out []string
	cur, err := get(o.ID)
	if err != nil {
		return out, err
	}
	line, err := closeOne(cur, o.Comment, o.State)
	if err != nil {
		return out, err
	}
	out = append(out, line)
	if o.Webhook.URL != "" {
		// the item is already closed; a webhook failure is reported, not returned
		if err := o.Webhook.send(cur.ID, cur.field("System.Title"), d.itemURL(cur.ID), prURLs(o.Comment)); err != nil {
			out = append(out, "webhook failed: "+err.Error())
		} else {
			out = append(out, "webhook sent")
		}
	}
	if o.NoCascade {
		return out, nil
	}
	for {
		parents := cur.relIDs("System.LinkTypes.Hierarchy-Reverse")
		if len(parents) == 0 {
			break
		}
		parent, err := get(parents[0])
		if err != nil {
			return out, err
		}
		if done, err := isDone(parent.ID); err != nil || done {
			return out, err
		}
		if reKeepOpen.MatchString(parent.field("System.Tags")) {
			break
		}
		var open []string
		for _, cid := range parent.relIDs("System.LinkTypes.Hierarchy-Forward") {
			if cid == cur.ID {
				continue
			}
			done, err := isDone(cid)
			if err != nil {
				return out, err
			}
			if !done {
				open = append(open, fmt.Sprintf("#%d", cid))
			}
		}
		if len(open) > 0 {
			out = append(out, "open "+strings.Join(open, ","))
			break
		}
		line, err := closeOne(parent, fmt.Sprintf("Alle børn lukket (#%d)", cur.ID), "")
		if err != nil {
			return out, err
		}
		out = append(out, line)
		cur = parent
	}
	return out, nil
}
