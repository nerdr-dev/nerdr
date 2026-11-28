package gitlab

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/nerdr-dev/nerdr/internal/forge"
)

// rawPipeline is a pipeline as the API has it.
type rawPipeline struct {
	ID        int    `json:"id"`
	ProjectID int    `json:"project_id"`
	Status    string `json:"status"`
	WebURL    string `json:"web_url"`
	SHA       string `json:"sha"`
}

// rawJob is a pipeline's job as the API has it.
type rawJob struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Stage        string `json:"stage"`
	Status       string `json:"status"`
	AllowFailure bool   `json:"allow_failure"`
	WebURL       string `json:"web_url"`
}

// checkStatus is a job's or a pipeline's status in forge's words. A job
// allowed to fail does not fail the pipeline, so its failure is NEUTRAL;
// so is a manual job you may run or not. One that is manual and not
// allowed to fail holds the pipeline up: PENDING, as one still to run.
func checkStatus(status string, allowFailure bool) string {
	switch status {
	case "success":
		return "SUCCESS"
	case "failed":
		if allowFailure {
			return "NEUTRAL"
		}
		return "FAILURE"
	case "canceled", "canceling":
		return "CANCELLED"
	case "skipped":
		return "SKIPPED"
	case "manual":
		if allowFailure {
			return "NEUTRAL"
		}
		return "PENDING"
	}
	return "PENDING" // created, pending, running, preparing, scheduled, waiting_for_resource…
}

// pipelineCheck is the pipeline as one check, for a list that does not
// ask for its jobs.
func pipelineCheck(p rawPipeline) forge.Check {
	return forge.Check{Name: "pipeline", Status: checkStatus(p.Status, false), DetailsURL: p.WebURL}
}

// latestPipeline is the newest pipeline of merge request n of the project
// at path; ok is false when it has none. err is that GitLab could not say:
// no pipeline is not known then.
func latestPipeline(dir, host, path string, n int) (p rawPipeline, ok bool, err error) {
	var ps []rawPipeline
	err = getJSON(dir, host, query(project(path)+"/merge_requests/"+strconv.Itoa(n)+"/pipelines", url.Values{"per_page": {"1"}}), &ps)
	if err != nil {
		return rawPipeline{}, false, err
	}
	if len(ps) == 0 {
		return rawPipeline{}, false, nil
	}
	if ps[0].ProjectID == 0 {
		// The list leaves it out on older GitLab: it is in the URL.
		if path2, ok := projectOfURL(ps[0].WebURL, "/-/pipelines/"); ok {
			if pr, err := projectAt(dir, host, path2); err == nil {
				ps[0].ProjectID = pr.ID
			}
		}
	}
	return ps[0], true, nil
}

// jobChecks is p's jobs, each a check named by its stage and name, with
// its trigger jobs (bridges), which run a pipeline of their own and are
// not among its jobs. A pipeline of a merge request from a fork may run
// in the fork, so its jobs are asked for of the project it ran in.
//
// When its jobs cannot be read it is the pipeline as one check, and err
// says why: that is not the jobs as they are, which a watch on them must
// not take for news. A failed pipeline none of whose jobs read failed has
// itself as a failed check too: what failed is not among them.
func jobChecks(dir, host string, p rawPipeline) ([]forge.Check, error) {
	if p.ProjectID == 0 {
		return []forge.Check{pipelineCheck(p)}, nil
	}
	base := "projects/" + strconv.Itoa(p.ProjectID) + "/pipelines/" + strconv.Itoa(p.ID)
	jobs, err := getAll[rawJob](dir, host, query(base+"/jobs", url.Values{"per_page": {"100"}}), glabTimeout)
	if NotFound(err) || forbidden(err) {
		// Jobs you may not see (a private fork's): the pipeline is all
		// there is to know, this time and the next.
		return []forge.Check{pipelineCheck(p)}, nil
	}
	if err != nil {
		return []forge.Check{pipelineCheck(p)}, err
	}
	bridges, err := getAll[rawJob](dir, host, query(base+"/bridges", url.Values{"per_page": {"100"}}), glabTimeout)
	switch {
	case NotFound(err) || forbidden(err):
		// GitLab before 13.x has no bridges to list: the failed
		// pipeline's own check below says it failed all the same.
	case err != nil:
		return []forge.Check{pipelineCheck(p)}, err
	}
	jobs = append(jobs, bridges...)
	if len(jobs) == 0 {
		return []forge.Check{pipelineCheck(p)}, nil
	}
	checks := make([]forge.Check, 0, len(jobs)+1)
	failed := false
	for _, j := range jobs {
		name := j.Name
		if j.Stage != "" {
			name = j.Stage + " / " + j.Name
		}
		c := forge.Check{Name: name, Status: checkStatus(j.Status, j.AllowFailure), DetailsURL: j.WebURL}
		failed = failed || c.Status == "FAILURE"
		checks = append(checks, c)
	}
	if pc := pipelineCheck(p); pc.Status == "FAILURE" && !failed {
		checks = append(checks, pc)
	}
	return checks, nil
}

// projectOfURL is the project path in a GitLab page's URL, the part
// before sep ("/-/jobs/"): https://host/group/sub/name/-/jobs/12.
func projectOfURL(u, sep string) (string, bool) {
	rest, ok := strings.CutPrefix(u, "https://")
	if !ok {
		rest, ok = strings.CutPrefix(u, "http://")
	}
	if !ok {
		return "", false
	}
	_, rest, _ = strings.Cut(rest, "/")
	path, _, found := strings.Cut(rest, sep)
	if !found || path == "" {
		return "", false
	}
	return path, true
}

// jobRe takes a job's ID out of its URL.
var jobRe = regexp.MustCompile(`/-/jobs/(\d+)`)

// FailedRunLogs returns the logs of the failed jobs among checks, trimmed
// to what an agent can be given, as the github package's are.
func (f Forge) FailedRunLogs(dir string, checks []forge.Check, limit int) (string, error) {
	if !Available() {
		return "", errors.New(NotInstalled)
	}
	seen := map[string]bool{}
	var out strings.Builder
	for _, c := range checks {
		if c.Status != "FAILURE" && c.Status != "ERROR" {
			continue
		}
		m := jobRe.FindStringSubmatch(c.DetailsURL)
		path, ok := projectOfURL(c.DetailsURL, "/-/jobs/")
		if m == nil || !ok || seen[c.DetailsURL] {
			continue
		}
		seen[c.DetailsURL] = true
		raw, err := api(dir, call{Host: f.host(), Path: project(path) + "/jobs/" + m[1] + "/trace", Timeout: glabLongTimeout})
		if err != nil {
			out.WriteString("(could not read the log for " + c.Name + ": " + err.Error() + ")\n")
			continue
		}
		out.WriteString("--- " + c.Name + " (job " + m[1] + ") ---\n")
		out.WriteString(forge.CondenseLog(forge.StripANSI(string(raw)), limit, "ERROR: "))
		out.WriteString("\n")
	}
	if out.Len() == 0 {
		return "", errors.New("no failed job to read")
	}
	return out.String(), nil
}
