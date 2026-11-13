package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Webhook is posted when a task is closed. Format:
//   - "json" (default): {"text", "taskId", "taskTitle", "taskUrl", "prs"}; "text" works for Slack and Mattermost
//   - "discord": {"content"}
//   - "teams": Adaptive Card for a Teams Workflows ("When a Teams webhook request is received") URL
type Webhook struct {
	URL    string `json:"url"`
	Format string `json:"format"`
}

var rePR = regexp.MustCompile(`https?://[^\s"'<>]+/(?:pull|pullrequest|merge_requests)/\d+`)

// prURLs returns the distinct PR links in text, in order.
func prURLs(text string) []string {
	var out []string
	for _, u := range rePR.FindAllString(text, -1) {
		if !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	return out
}

func (d *DevOps) itemURL(id int) string {
	return fmt.Sprintf("https://dev.azure.com/%s/%s/_workitems/edit/%d", url.PathEscape(d.t.Org.Name), d.proj(), id)
}

func webhookText(id int, title, itemURL string, prs []string) string {
	pr := "none"
	if len(prs) > 0 {
		pr = strings.Join(prs, " ")
	}
	return fmt.Sprintf("A task has been completed: #%d %s\nAzure DevOps: %s\nPR: %s", id, title, itemURL, pr)
}

func (w Webhook) payload(id int, title, itemURL string, prs []string) any {
	text := webhookText(id, title, itemURL, prs)
	switch strings.ToLower(w.Format) {
	case "discord":
		return map[string]any{"content": text}
	case "teams":
		actions := []map[string]any{{"type": "Action.OpenUrl", "title": "Azure DevOps", "url": itemURL}}
		for i, p := range prs {
			label := "PR"
			if len(prs) > 1 {
				label = fmt.Sprintf("PR %d", i+1)
			}
			actions = append(actions, map[string]any{"type": "Action.OpenUrl", "title": label, "url": p})
		}
		return map[string]any{
			"type": "message",
			"attachments": []map[string]any{{
				"contentType": "application/vnd.microsoft.card.adaptive",
				"content": map[string]any{
					"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
					"type":    "AdaptiveCard",
					"version": "1.4",
					"body": []map[string]any{
						{"type": "TextBlock", "text": "A task has been completed", "weight": "Bolder"},
						{"type": "TextBlock", "text": fmt.Sprintf("#%d %s", id, title), "wrap": true},
					},
					"actions": actions,
				},
			}},
		}
	default:
		if prs == nil {
			prs = []string{}
		}
		return map[string]any{"text": text, "taskId": id, "taskTitle": title, "taskUrl": itemURL, "prs": prs}
	}
}

func (w Webhook) send(id int, title, itemURL string, prs []string) error {
	b, err := json.Marshal(w.payload(id, title, itemURL, prs))
	if err != nil {
		return err
	}
	c := &http.Client{Timeout: 20 * time.Second}
	resp, err := c.Post(w.URL, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("%d %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
