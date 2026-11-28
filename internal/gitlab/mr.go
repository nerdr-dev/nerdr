package gitlab

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nerdr-dev/nerdr/internal/forge"
	"github.com/nerdr-dev/nerdr/internal/gitsafe"
	"github.com/nerdr-dev/nerdr/internal/proc"
)

// rawMR is a merge request as the API has it.
type rawMR struct {
	IID                 int          `json:"iid"`
	Title               string       `json:"title"`
	WebURL              string       `json:"web_url"`
	State               string       `json:"state"`
	Draft               bool         `json:"draft"`
	WorkInProgress      bool         `json:"work_in_progress"`
	Author              rawUser      `json:"author"`
	SourceBranch        string       `json:"source_branch"`
	TargetBranch        string       `json:"target_branch"`
	SHA                 string       `json:"sha"`
	UpdatedAt           time.Time    `json:"updated_at"`
	HasConflicts        bool         `json:"has_conflicts"`
	DetailedMergeStatus string       `json:"detailed_merge_status"`
	SourceProjectID     int          `json:"source_project_id"`
	TargetProjectID     int          `json:"target_project_id"`
	HeadPipeline        *rawPipeline `json:"head_pipeline"`
	References          struct {
		Full string `json:"full"`
	} `json:"references"`
}

// prState is GitLab's state of a merge request in forge's words.
func prState(s string) string {
	switch s {
	case "opened":
		return "OPEN"
	case "merged":
		return "MERGED"
	}
	return "CLOSED" // closed, and locked: closed to discussion too
}

// mergeable is whether the merge request conflicts with its target, in
// forge's words. GitLab's detailed status says much else besides (not
// approved yet, a pipeline to pass): none of it is a conflict.
func mergeable(r rawMR) string {
	switch {
	// need_rebase is only that the target moved on, as a project that
	// merges fast-forward asks: no conflict, nor work for an agent.
	case r.HasConflicts || r.DetailedMergeStatus == "conflict":
		return "CONFLICTING"
	case r.DetailedMergeStatus == "", r.DetailedMergeStatus == "unchecked", r.DetailedMergeStatus == "checking",
		r.DetailedMergeStatus == "preparing", r.DetailedMergeStatus == "approvals_syncing":
		return "UNKNOWN"
	}
	return "MERGEABLE"
}

// toPR is r in forge's words; the fork's owner, approvals, discussions
// and jobs are filled in after, by asking for them.
func (r rawMR) toPR() forge.PR {
	return forge.PR{
		Kind:      forge.GitLab,
		Number:    r.IID,
		Title:     r.Title,
		URL:       r.WebURL,
		State:     prState(r.State),
		IsDraft:   r.Draft || r.WorkInProgress,
		Author:    r.Author.Username,
		Branch:    r.SourceBranch,
		Head:      r.SHA,
		Base:      r.TargetBranch,
		UpdatedAt: r.UpdatedAt,
		Mergeable: mergeable(r),
	}
}

// fromFork is whether r's branch is in another project than the one it
// is to be merged into.
func (r rawMR) fromFork() bool {
	return r.SourceProjectID != 0 && r.TargetProjectID != 0 && r.SourceProjectID != r.TargetProjectID
}

// Forge is a GitLab host, as forges asks a repository's service. Origin
// is the repository's origin; Upstream, when it has one on the same host,
// is the project origin is a fork of, which a merge request from the fork
// is in.
type Forge struct {
	Origin   forge.Remote
	Upstream forge.Remote
}

func (Forge) Kind() forge.Kind { return forge.GitLab }
func (Forge) Available() bool  { return Available() }

// NoRepo is whether err says the repository is not a project GitLab
// knows, or not one you may see: asking again will not help.
func (Forge) NoRepo(err error) bool {
	var part partError
	return !errors.As(err, &part) && NotFound(err)
}

// PRRef is the ref GitLab keeps merge request n's head at, in the project
// it is to be merged into: a fork's too.
func (Forge) PRRef(n int) string { return fmt.Sprintf("refs/merge-requests/%d/head", n) }

// host is the GitLab host the repository is on.
func (f Forge) host() string { return f.Origin.Host }

// targets is the projects a merge request of the branch can be in: origin,
// and the project it is a fork of.
func (f Forge) targets() []string {
	out := []string{f.Origin.Path}
	if f.Upstream.Path != "" && f.Upstream.Host == f.Origin.Host && !strings.EqualFold(f.Upstream.Path, f.Origin.Path) {
		out = append(out, f.Upstream.Path)
	}
	return out
}

// branchMRs is the merge requests from branch in origin into target, the
// newest first: every open one, or, with all, the newest 100 in any
// state. Many forks' merge requests can be from a branch of that name, so
// every page of the open ones is read.
func (f Forge) branchMRs(dir, target, branch string, all bool) ([]rawMR, error) {
	origin, err := projectAt(dir, f.host(), f.Origin.Path)
	if err != nil {
		return nil, err
	}
	v := url.Values{"source_branch": {branch}, "order_by": {"created_at"}, "sort": {"desc"}, "per_page": {"100"}}
	var mrs []rawMR
	if all {
		v.Set("state", "all")
		err = getJSON(dir, f.host(), query(project(target)+"/merge_requests", v), &mrs)
	} else {
		v.Set("state", "opened")
		mrs, err = getAll[rawMR](dir, f.host(), query(project(target)+"/merge_requests", v), glabTimeout)
	}
	if err != nil {
		return nil, err
	}
	var out []rawMR
	for _, mr := range mrs {
		if mr.SourceProjectID == origin.ID {
			out = append(out, mr)
		}
	}
	return out, nil
}

// findBranchMR is the merge request of branch: the open one when there is
// one, else the newest, from origin into origin or into the project it is
// a fork of; and the project it is in.
func (f Forge) findBranchMR(dir, branch string) (rawMR, string, error) {
	var newest rawMR
	newestIn := ""
	for _, all := range []bool{false, true} {
		for _, target := range f.targets() {
			mrs, err := f.branchMRs(dir, target, branch, all)
			if err != nil {
				if target == f.Origin.Path {
					return rawMR{}, "", err
				}
				continue // the upstream may be one you cannot see
			}
			for _, mr := range mrs {
				if mr.State == "opened" {
					return mr, target, nil
				}
				if newestIn == "" || mr.UpdatedAt.After(newest.UpdatedAt) {
					newest, newestIn = mr, target
				}
			}
		}
	}
	if newestIn == "" {
		return rawMR{}, "", fmt.Errorf("no merge requests found for branch %q", branch)
	}
	return newest, newestIn, nil
}

// currentMR is the merge request of the branch checked out in dir, and
// the project it is in: for a branch fetched from a fork's merge request
// (see workspace.FetchForkPR), that one.
func (f Forge) currentMR(dir string) (rawMR, string, error) {
	branch := currentBranch(dir)
	if branch == "" {
		return rawMR{}, "", errors.New("no branch is checked out")
	}
	if n := forkMRNumber(dir, branch); n > 0 {
		var mr rawMR
		if err := getJSON(dir, f.host(), project(f.Origin.Path)+"/merge_requests/"+strconv.Itoa(n), &mr); err != nil {
			return rawMR{}, "", err
		}
		return mr, f.Origin.Path, nil
	}
	return f.findBranchMR(dir, branch)
}

// CurrentPR fetches the merge request for dir's checked-out branch. ok is
// false when glab isn't installed, or there's no merge request for the
// branch; msg says which.
func (f Forge) CurrentPR(dir string) (forge.PR, bool, string) {
	if !Available() {
		return forge.PR{}, false, NotInstalled
	}
	mr, in, err := f.currentMR(dir)
	if err != nil {
		return forge.PR{}, false, err.Error()
	}
	pr, err := f.fetch(dir, in, mr.IID)
	if err != nil {
		return forge.PR{}, false, err.Error()
	}
	if !forge.OwnPR(dir, pr.State, pr.Head) {
		return forge.PR{}, false, forge.Unowned(dir, pr)
	}
	return pr, true, ""
}

// FetchPR fetches merge request n of origin in full, with its
// discussions and jobs. Origin is the project of every list and fetch,
// as it is the remote a fork's merge request is fetched from (see
// workspace.FetchForkPRRef): a number of a list is the one fetched.
func (f Forge) FetchPR(dir string, n int) (forge.PR, error) {
	if !Available() {
		return forge.PR{}, errors.New(NotInstalled)
	}
	return f.fetch(dir, f.Origin.Path, n)
}

// fetch is merge request n of the project at path, in full.
func (f Forge) fetch(dir, path string, n int) (forge.PR, error) {
	var mr rawMR
	if err := getJSON(dir, f.host(), project(path)+"/merge_requests/"+strconv.Itoa(n), &mr); err != nil {
		return forge.PR{}, err
	}
	pr := mr.toPR()
	f.fillFork(dir, mr, &pr)
	// A part that cannot be read is left out: the rest of the merge
	// request still can be.
	approvals, _ := f.fillReviews(dir, path, mr, &pr)
	if mr.HeadPipeline != nil {
		pr.Checks, _ = jobChecks(dir, f.host(), *mr.HeadPipeline)
	}
	var commenters []int
	pr.Comments, commenters = f.discussions(dir, path, mr)
	f.fillAssociations(dir, path, &pr, commenters, approvals)
	return pr, nil
}

// fillFork names the owner of the fork r is from in pr.
func (f Forge) fillFork(dir string, r rawMR, pr *forge.PR) {
	if !r.fromFork() {
		return
	}
	pr.Fork = "a fork"
	if p, err := projectByID(dir, f.host(), r.SourceProjectID); err == nil && p.Namespace.FullPath != "" {
		pr.Fork = p.Namespace.FullPath
	}
}

// rawApprovals is who approved a merge request, and how many approvals it
// still needs.
type rawApprovals struct {
	Approved          bool `json:"approved"`
	ApprovalsRequired int  `json:"approvals_required"`
	ApprovalsLeft     int  `json:"approvals_left"`
	ApprovedBy        []struct {
		User       rawUser   `json:"user"`
		ApprovedAt time.Time `json:"approved_at"`
	} `json:"approved_by"`
}

// rawReviewer is a reviewer of a merge request, and what they have done.
type rawReviewer struct {
	User      rawUser   `json:"user"`
	State     string    `json:"state"` // unreviewed, reviewed, requested_changes, approved, unapproved
	CreatedAt time.Time `json:"created_at"`
}

// fillReviews fills in pr's reviews and review decision: an approval is
// an APPROVED review, a reviewer who asked for changes CHANGES_REQUESTED.
// It returns who approved and reviewed, by user ID, for
// fillAssociations; and err when a part could not be read, which is left
// out then.
func (f Forge) fillReviews(dir, path string, r rawMR, pr *forge.PR) (map[string]int, error) {
	ids := map[string]int{}
	base := project(path) + "/merge_requests/" + strconv.Itoa(r.IID)
	var ap rawApprovals
	apErr := getJSON(dir, f.host(), base+"/approvals", &ap)
	var reviewers []rawReviewer
	rvErr := getJSON(dir, f.host(), base+"/reviewers", &reviewers)
	if NotFound(rvErr) {
		rvErr = nil // a GitLab too old to list them: there are none to say
	}

	for _, a := range ap.ApprovedBy {
		ids[a.User.Username] = a.User.ID
		pr.Reviews = append(pr.Reviews, forge.Review{Author: a.User.Username, State: "APPROVED", SubmittedAt: a.ApprovedAt})
	}
	changes := false
	for _, rv := range reviewers {
		ids[rv.User.Username] = rv.User.ID
		// A reviewer's state is their latest: one who approves again is
		// approved, not requested_changes.
		if rv.State == "requested_changes" {
			changes = true
			// GitLab does not say when: no news is made of it (see
			// core's SeenOf), but the decision shows it.
			pr.Reviews = append(pr.Reviews, forge.Review{Author: rv.User.Username, State: "CHANGES_REQUESTED"})
		}
	}
	switch {
	case changes:
		pr.ReviewDecision = "CHANGES_REQUESTED"
	case apErr != nil || ap.ApprovalsRequired == 0:
		// Nobody has to approve: GitHub says nothing then either.
	case ap.Approved && ap.ApprovalsLeft <= 0:
		// Approved says every rule is met (code owners' too), where
		// ApprovalsLeft counts only the approvals.
		pr.ReviewDecision = "APPROVED"
	default:
		pr.ReviewDecision = "REVIEW_REQUIRED"
	}
	if apErr != nil {
		return ids, apErr
	}
	return ids, rvErr
}

// rawNote is a comment, or a thing GitLab did (a system note), in a
// discussion.
type rawNote struct {
	ID        int       `json:"id"`
	Body      string    `json:"body"`
	Author    rawUser   `json:"author"`
	CreatedAt time.Time `json:"created_at"`
	System    bool      `json:"system"`
	Position  *struct {
		NewPath string `json:"new_path"`
		OldPath string `json:"old_path"`
		NewLine int    `json:"new_line"`
		OldLine int    `json:"old_line"`
	} `json:"position"`
}

type rawDiscussion struct {
	Notes []rawNote `json:"notes"`
}

// discussions is the comments of r, in its conversation and on the lines
// of its code, oldest first; none when they cannot be read. A reply in a
// thread on a line is on that line too. A system note ("added 2
// commits") is no one's comment.
// The second answer is each comment's author's user ID.
func (f Forge) discussions(dir, path string, r rawMR) ([]forge.Comment, []int) {
	ds, err := getAll[rawDiscussion](dir, f.host(), query(project(path)+"/merge_requests/"+strconv.Itoa(r.IID)+"/discussions",
		url.Values{"per_page": {"100"}}), glabLongTimeout)
	if err != nil {
		return nil, nil
	}
	type idComment struct {
		forge.Comment
		id int
	}
	var cs []idComment
	for _, d := range ds {
		where, line := "", 0
		for _, n := range d.Notes {
			if n.Position != nil {
				where, line = n.Position.NewPath, n.Position.NewLine
				if line == 0 && n.Position.OldLine != 0 {
					// A line taken out: it is on the old side.
					line = n.Position.OldLine
					if n.Position.OldPath != "" {
						where = n.Position.OldPath
					}
				}
				if where == "" {
					where = n.Position.OldPath
				}
			}
			if n.System {
				continue
			}
			cs = append(cs, idComment{forge.Comment{
				Author: n.Author.Username, Body: n.Body, CreatedAt: n.CreatedAt,
				URL: r.WebURL + "#note_" + strconv.Itoa(n.ID), Path: where, Line: line,
			}, n.Author.ID})
		}
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].CreatedAt.Before(cs[j].CreatedAt) })
	out, ids := make([]forge.Comment, len(cs)), make([]int, len(cs))
	for i, c := range cs {
		out[i], ids[i] = c.Comment, c.id
	}
	return out, ids
}

// fillAssociations says, of each who commented on or reviewed pr, how
// they stand to the project, in GitHub's words: OWNER, MEMBER for a
// developer or maintainer, CONTRIBUTOR for a guest or reporter, NONE for
// anyone else. One call each, so only for a merge request fetched by
// itself.
func (f Forge) fillAssociations(dir, path string, pr *forge.PR, commenters []int, ids map[string]int) {
	role := map[int]string{}
	roleOf := func(id int) string {
		if id == 0 {
			return ""
		}
		if r, ok := role[id]; ok {
			return r
		}
		var m struct {
			AccessLevel int `json:"access_level"`
		}
		r := "NONE"
		if err := getJSON(dir, f.host(), project(path)+"/members/all/"+strconv.Itoa(id), &m); err == nil {
			switch {
			case m.AccessLevel >= 50:
				r = "OWNER"
			case m.AccessLevel >= 30:
				r = "MEMBER"
			case m.AccessLevel > 0:
				r = "CONTRIBUTOR"
			}
		} else if !NotFound(err) {
			r = "" // not known: not taken for a stranger's, nor for a member's
		}
		role[id] = r
		return r
	}
	for i := range pr.Comments {
		if i < len(commenters) {
			pr.Comments[i].Association = roleOf(commenters[i])
		}
	}
	for i, rv := range pr.Reviews {
		pr.Reviews[i].Association = roleOf(ids[rv.Author])
	}
}

// ListMyPRs returns the open merge requests you opened in origin, most recently updated first, with their approvals and jobs:
// what the watch on them tells news of. limit caps how many are asked
// for. A merge request whose approvals or jobs cannot be read just now
// fails the list: the watch must not take what is missing for none.
func (f Forge) ListMyPRs(dir string, limit int) ([]forge.PR, error) {
	return f.list(dir, limit, true)
}

// ListPRs returns origin's open merge requests, whoever opened
// them, most recently updated first, each with its latest pipeline's
// state.
func (f Forge) ListPRs(dir string, limit int) ([]forge.PR, error) {
	return f.list(dir, limit, false)
}

// listers is how many merge requests of a list are asked about at once.
const listers = 4

func (f Forge) list(dir string, limit int, mine bool) ([]forge.PR, error) {
	if !Available() {
		return nil, errors.New(NotInstalled)
	}
	path := f.Origin.Path
	v := url.Values{"state": {"opened"}, "order_by": {"updated_at"}, "sort": {"desc"}, "per_page": {strconv.Itoa(min(limit, 100))}}
	if mine {
		v.Set("scope", "created_by_me")
		// Has GitLab work out whether each conflicts now, not only when
		// someone looked at it; the next look has it.
		v.Set("with_merge_status_recheck", "true")
	}
	var mrs []rawMR
	if err := getJSON(dir, f.host(), query(project(path)+"/merge_requests", v), &mrs); err != nil {
		return nil, err
	}
	prs := make([]forge.PR, len(mrs))
	errs := make([]error, len(mrs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, listers)
	for i, mr := range mrs {
		prs[i] = mr.toPR()
		wg.Add(1)
		go func(i int, mr rawMR) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			f.fillFork(dir, mr, &prs[i])
			if !mine {
				// Only to show: a pipeline not read is shown as none.
				if p, ok, _ := latestPipeline(dir, f.host(), path, mr.IID); ok {
					prs[i].Checks = []forge.Check{pipelineCheck(p)}
				}
				return
			}
			if _, err := f.fillReviews(dir, path, mr, &prs[i]); err != nil {
				errs[i] = partError{err}
				return
			}
			p, ok, err := latestPipeline(dir, f.host(), path, mr.IID)
			switch {
			case err != nil:
				errs[i] = partError{err}
			case ok:
				if prs[i].Checks, err = jobChecks(dir, f.host(), p); err != nil {
					errs[i] = partError{err}
				}
			}
		}(i, mr)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err // one is said: they are likely all the one
		}
	}
	sort.SliceStable(prs, func(i, j int) bool { return prs[j].UpdatedAt.Before(prs[i].UpdatedAt) })
	return prs, nil
}

// BranchPR is the newest merge request, in any state, from branch in
// origin, and the commit its head was at last. ok is false when there is
// none, or GitLab cannot say.
func (f Forge) BranchPR(dir, branch string) (number int, state, head string, ok bool) {
	if !Available() {
		return 0, "", "", false
	}
	mr, _, err := f.findBranchMR(dir, branch)
	if err != nil {
		return 0, "", "", false
	}
	return mr.IID, prState(mr.State), mr.SHA, true
}

// ForkPR is the open merge request into origin from a fork's branch named
// branch: a branch of that name made here would not be it. ok is false
// when there is none, or GitLab cannot say.
func (f Forge) ForkPR(dir, branch string) (number int, owner string, ok bool) {
	if !Available() {
		return 0, "", false
	}
	var mrs []rawMR
	err := getJSON(dir, f.host(), query(project(f.Origin.Path)+"/merge_requests", url.Values{
		"source_branch": {branch}, "state": {"opened"}, "per_page": {"10"},
	}), &mrs)
	if err != nil {
		return 0, "", false
	}
	for _, mr := range mrs {
		if mr.fromFork() {
			var pr forge.PR
			f.fillFork(dir, mr, &pr)
			return mr.IID, pr.Fork, true
		}
	}
	return 0, "", false
}

// BranchStatus looks up the merge request for the branch checked out in
// dir, lightly, for the tab bar. ok is false when there is none (or
// GitLab cannot tell).
func (f Forge) BranchStatus(dir string) (forge.Status, bool) {
	if !Available() {
		return forge.Status{}, false
	}
	mr, in, err := f.currentMR(dir)
	if err != nil {
		return forge.Status{}, false
	}
	st := forge.Status{Kind: forge.GitLab, Number: mr.IID, URL: mr.WebURL, State: prState(mr.State), HeadOID: mr.SHA, Base: mr.TargetBranch}
	if p, ok, _ := latestPipeline(dir, f.host(), in, mr.IID); ok {
		st.CI = forge.CISummary([]forge.Check{pipelineCheck(p)})
	}
	return forge.Recheck(dir, st)
}

// CreatePR opens a merge request for the branch checked out in dir, in
// origin, the project at the URL nerdr recorded, titled and described
// from its commits as `gh pr create --fill` does, and returns its URL. The
// branch must be pushed first. A stacked branch's merge request is into
// the branch it is on, as long as origin has that.
func (f Forge) CreatePR(dir, origin string) (string, error) {
	if !Available() {
		return "", errors.New(NotInstalled)
	}
	r, ok := forge.ParseRemote(origin)
	if !ok {
		return "", errors.New("cannot tell which project origin (" + origin + ") is")
	}
	branch := currentBranch(dir)
	if branch == "" {
		return "", errors.New("no branch is checked out")
	}
	p, err := projectAt(dir, r.Host, r.Path)
	if err != nil {
		return "", err
	}
	target := p.DefaultBranch
	if b := stackedBase(dir); b != "" && remoteHas(dir, b) {
		target = b
	}
	if target == "" {
		target = "main"
	}
	title, body := fill(dir, branch, target)
	out, err := api(dir, call{Host: r.Host, Path: project(r.Path) + "/merge_requests", Method: "POST", Fields: []string{
		"source_branch=" + branch, "target_branch=" + target, "title=" + title, "description=" + body,
	}})
	if err != nil {
		return "", err
	}
	var mr rawMR
	if jsonErr := jsonUnmarshal(out, &mr); jsonErr != nil || mr.WebURL == "" {
		return "", errors.New("could not parse the output from glab")
	}
	return mr.WebURL, nil
}

// fill is a merge request's title and description from the commits on
// branch that target has not got: one commit's subject and body, or, for
// several, the branch's name and their subjects.
func fill(dir, branch, target string) (title, body string) {
	// The commits target has not got; with no copy of target here, those
	// origin has on no branch.
	not := []string{"^origin/" + target}
	if !remoteHas(dir, target) {
		not = []string{"^refs/heads/" + target}
		if proc.Run(gitsafe.Command(dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+target)) != nil {
			not = []string{"--not", "--remotes=origin"}
		}
	}
	out, err := proc.Output(gitsafe.Command(dir, append([]string{"log", "--reverse", "--format=%s%x00%b%x1e", "HEAD"}, not...)...))
	if err != nil {
		return branch, ""
	}
	var commits [][2]string
	for _, c := range strings.Split(string(out), "\x1e") {
		c = strings.TrimLeft(c, "\n")
		if c == "" {
			continue
		}
		subj, b, _ := strings.Cut(c, "\x00")
		commits = append(commits, [2]string{strings.TrimSpace(subj), strings.TrimSpace(b)})
	}
	switch len(commits) {
	case 0:
		return branch, ""
	case 1:
		return commits[0][0], commits[0][1]
	}
	var lines []string
	for _, c := range commits {
		lines = append(lines, "- "+c[0])
	}
	return strings.ReplaceAll(branch, "-", " "), strings.Join(lines, "\n")
}
