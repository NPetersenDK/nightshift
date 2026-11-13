package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type Window struct {
	Name  string  `json:"name"`
	Used  float64 `json:"used"`
	Reset string  `json:"reset"`
}

type Usage struct {
	Source  string   `json:"source"`
	Windows []Window `json:"windows"`
}

func (u *Usage) get(name string) (float64, bool) {
	for _, w := range u.Windows {
		if w.Name == name {
			return w.Used, true
		}
	}
	return 0, false
}

var reUsage = regexp.MustCompile(`^\s*(.+?):\s*(\d+(?:\.\d+)?)%\s*used\s*·\s*resets\s*(.+?)\s*$`)

func (u *Usage) text() string {
	parts := make([]string, 0, len(u.Windows))
	reTZ := regexp.MustCompile(`\s*\(.*\)$`)
	for _, w := range u.Windows {
		parts = append(parts, fmt.Sprintf("%s %.0f %% %s", strings.TrimPrefix(w.Name, "Current "), w.Used*100, reTZ.ReplaceAllString(w.Reset, "")))
	}
	return strings.Join(parts, " | ")
}

var probeArgs = []string{"--safe-mode", "--no-session-persistence"}

// getUsage reads subscription usage via the free local /usage command; falls back to a haiku probe.
func getUsage(c *Config) (*Usage, error) {
	lastDiag = ""
	if u := usageFromCommand(c); u != nil {
		return u, nil
	}
	d1 := lastDiag
	if u := usageFromProbe(c); u != nil {
		return u, nil
	}
	return nil, fmt.Errorf("could not read usage\n--- /usage:\n%s\n--- probe:\n%s", d1, lastDiag)
}

func usageFromCommand(c *Config) *Usage {
	lines, err := c.runClaude(RunSpec{Args: append([]string{"-p", "/usage", "--output-format", "json"}, probeArgs...), Timeout: 2 * time.Minute})
	if err != nil {
		return nil
	}
	var obj struct {
		Result string `json:"result"`
	}
	if json.Unmarshal([]byte(strings.Join(lines, "\n")), &obj) != nil {
		return nil
	}
	u := &Usage{Source: "/usage"}
	for _, l := range strings.Split(obj.Result, "\n") {
		if m := reUsage.FindStringSubmatch(l); m != nil {
			var pct float64
			fmt.Sscanf(m[2], "%f", &pct)
			u.Windows = append(u.Windows, Window{Name: m[1], Used: pct / 100, Reset: m[3]})
		}
	}
	if len(u.Windows) == 0 {
		return nil
	}
	return u
}

func usageFromProbe(c *Config) *Usage {
	args := append([]string{"-p", "ok", "--model", "haiku", "--output-format", "stream-json", "--verbose", "--tools", ""}, probeArgs...)
	lines, err := c.runClaude(RunSpec{Args: args, Stdin: "", Timeout: 2 * time.Minute})
	if err != nil {
		return nil
	}
	rl := lastEvent(parseEvents(lines), "rate_limit_event")
	if rl == nil {
		return nil
	}
	names := map[string]string{"five_hour": "Current session", "seven_day": "Current week (all models)"}
	u := &Usage{Source: "probe"}
	for k, w := range rl.RateLimitInfo.UnifiedWindows {
		n := k
		if v, ok := names[k]; ok {
			n = v
		}
		u.Windows = append(u.Windows, Window{Name: n, Used: w.Utilization, Reset: time.Unix(int64(w.ResetsAt), 0).Format("2006-01-02 15:04")})
	}
	return u
}
