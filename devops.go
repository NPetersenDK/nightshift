package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type DevOps struct {
	t    Target
	http *http.Client
}

func newDevOps(t Target) *DevOps {
	return &DevOps{t: t, http: &http.Client{Timeout: 60 * time.Second}}
}

func (d *DevOps) proj() string { return url.PathEscape(d.t.Project.Name) }

// do sends a request. body may be a string/[]byte (sent raw) or any JSON-marshalable value.
func (d *DevOps) do(method, path string, body any, ctype, version string, out any) error {
	if version == "" {
		version = "7.1"
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	u := fmt.Sprintf("https://dev.azure.com/%s/%s%sapi-version=%s", url.PathEscape(d.t.Org.Name), path, sep, version)
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		j, err := json.Marshal(b)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(j)
	}
	req, err := http.NewRequest(method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(":"+d.t.pat())))
	if ctype == "" {
		ctype = "application/json"
	}
	if rd != nil {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		msg := string(data)
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("devops %s %s: %d %s", method, path, resp.StatusCode, msg)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

type WorkItem struct {
	ID        int            `json:"id"`
	Fields    map[string]any `json:"fields"`
	Relations []struct {
		Rel string `json:"rel"`
		URL string `json:"url"`
	} `json:"relations"`
}

func (w *WorkItem) field(name string) string {
	if v, ok := w.Fields[name]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

func (w *WorkItem) relIDs(rel string) []int {
	var ids []int
	for _, r := range w.Relations {
		if r.Rel == rel {
			var id int
			fmt.Sscanf(r.URL[strings.LastIndex(r.URL, "/")+1:], "%d", &id)
			ids = append(ids, id)
		}
	}
	return ids
}

type StateDef struct {
	Name     string `json:"name"`
	Category string `json:"category"`
}

func (d *DevOps) states(typ string) ([]StateDef, error) {
	var r struct {
		Value []StateDef `json:"value"`
	}
	err := d.do("GET", fmt.Sprintf("%s/_apis/wit/workitemtypes/%s/states", d.proj(), url.PathEscape(typ)), nil, "", "", &r)
	return r.Value, err
}

// stateName returns the first state of a category (Proposed/InProgress/Completed).
func (d *DevOps) stateName(typ, category string) (string, error) {
	ss, err := d.states(typ)
	if err != nil {
		return "", err
	}
	for _, s := range ss {
		if s.Category == category {
			return s.Name, nil
		}
	}
	return "", nil
}

type ItemState struct{ Type, State, Category string }

// done reports whether nightshift treats the item as finished (Resolved counts, see closeAs).
func (s ItemState) done() bool {
	return s.Category == "Resolved" || s.Category == "Completed" || s.Category == "Removed"
}

func (d *DevOps) itemState(id int) (ItemState, error) {
	var w WorkItem
	if err := d.do("GET", fmt.Sprintf("%s/_apis/wit/workitems/%d?fields=System.WorkItemType,System.State", d.proj(), id), nil, "", "", &w); err != nil {
		return ItemState{}, err
	}
	s := ItemState{Type: w.field("System.WorkItemType"), State: w.field("System.State")}
	ss, err := d.states(s.Type)
	if err != nil {
		return s, err
	}
	for _, x := range ss {
		if x.Name == s.State {
			s.Category = x.Category
		}
	}
	return s, nil
}

func (d *DevOps) setState(id int, state string) error {
	return d.patch(id, []map[string]any{{"op": "add", "path": "/fields/System.State", "value": state}})
}

func (d *DevOps) patch(id int, ops []map[string]any) error {
	return d.do("PATCH", fmt.Sprintf("%s/_apis/wit/workitems/%d", d.proj(), id), ops, "application/json-patch+json", "", nil)
}

func (d *DevOps) comment(id int, text string) error {
	h := strings.ReplaceAll(html.EscapeString(text), "\n", "<br>")
	return d.do("POST", fmt.Sprintf("%s/_apis/wit/workItems/%d/comments", d.proj(), id), map[string]string{"text": h}, "", "7.1-preview.4", nil)
}

type TeamAreas struct {
	Default string
	Values  []struct {
		Path            string
		IncludeChildren bool
	}
}

func (d *DevOps) teamAreas() (TeamAreas, error) {
	var r struct {
		DefaultValue string `json:"defaultValue"`
		Values       []struct {
			Value           string `json:"value"`
			IncludeChildren bool   `json:"includeChildren"`
		} `json:"values"`
	}
	err := d.do("GET", fmt.Sprintf("%s/%s/_apis/work/teamsettings/teamfieldvalues", d.proj(), url.PathEscape(d.t.Project.Team)), nil, "", "", &r)
	ta := TeamAreas{Default: r.DefaultValue}
	for _, v := range r.Values {
		ta.Values = append(ta.Values, struct {
			Path            string
			IncludeChildren bool
		}{v.Value, v.IncludeChildren})
	}
	return ta, err
}

// areaClause returns an extra WIQL AND-clause limiting to the team's area paths ("" if no team set).
func (d *DevOps) areaClause(alias string) (string, error) {
	if d.t.Project.Team == "" {
		return "", nil
	}
	ta, err := d.teamAreas()
	if err != nil {
		return "", err
	}
	var parts []string
	for _, v := range ta.Values {
		p := strings.ReplaceAll(v.Path, "'", "''")
		if v.IncludeChildren {
			parts = append(parts, fmt.Sprintf("%s UNDER '%s'", alias, p))
		} else {
			parts = append(parts, fmt.Sprintf("%s = '%s'", alias, p))
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	return "AND (" + strings.Join(parts, " OR ") + ")", nil
}

var (
	reBreak = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</li>`)
	reTag   = regexp.MustCompile(`<[^>]+>`)
	reNL3   = regexp.MustCompile(`\n{3,}`)
)

func plainText(h string) string {
	if h == "" {
		return ""
	}
	s := reTag.ReplaceAllString(reBreak.ReplaceAllString(h, "\n"), "")
	return strings.TrimSpace(reNL3.ReplaceAllString(html.UnescapeString(s), "\n\n"))
}
