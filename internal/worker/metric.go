package worker

import (
	"encoding/json"
	"time"
)

// taskMetric is the one-line JSON record of a finished task, for the log: the
// measurement of what a worker did. It never carries a page's content.
func taskMetric(perimeter, replica, kb string, t TaskSpec, model, key string, d time.Duration, res Result, err error) string {
	m := map[string]any{
		"metric": "worker_task", "perimeter": perimeter, "replica": replica, "kb": kb,
		"kind": t.Kind, "profile": t.Profile, "model": model, "key": key,
		"seconds": d.Round(time.Millisecond).Seconds(),
		"tokens":  res.Tokens, "units": res.Units, "commits": res.Commits, "summary": res.Summary,
	}
	if err != nil {
		m["error"] = oneLine(err.Error())
	}
	b, _ := json.Marshal(m)
	return string(b)
}
