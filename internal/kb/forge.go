package kb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// PullRequest is the non-secret forge state retained for a server-profile KB.
type PullRequest struct {
	Number   int    `json:"number"`
	URL      string `json:"url"`
	HeadSHA  string `json:"head_sha"`
	BaseSHA  string `json:"base_sha,omitempty"`
	State    string `json:"state"`
	Merged   bool   `json:"merged"`
	MergeSHA string `json:"merge_sha,omitempty"`
}

// MergeResult is the forge-confirmed squash merge outcome. SHA is the commit
// GitHub reports as merged and must later be observed on the protected base.
type MergeResult struct {
	Merged bool
	SHA    string
}

// Forge isolates the review boundary from Git transport. Implementations must
// not retain or expose credentials in errors.
type Forge interface {
	FindOpenPR(context.Context, string, string, string, string) ([]PullRequest, error)
	GetPR(context.Context, string, string, int) (PullRequest, error)
	CreatePR(context.Context, string, string, string, string, string, string) (PullRequest, error)
	PRReady(context.Context, string, string, int) (bool, error)
	MergeSquash(context.Context, string, string, int, string) (MergeResult, error)
}

// GitHubForge is GitHub's REST implementation of Forge. The stdlib client is
// intentionally injected so tests use httptest and production has no gh CLI
// dependency.
type GitHubForge struct {
	APIURL string
	Token  string
	Client *http.Client
}

func (g *GitHubForge) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (g *GitHubForge) request(ctx context.Context, method, endpoint string, body any, out any) error {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("github request: marshal: %w", err)
		}
		r = bytes.NewReader(data)
	}
	u, err := url.Parse(strings.TrimRight(g.APIURL, "/") + endpoint)
	if err != nil {
		return fmt.Errorf("github request URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), r)
	if err != nil {
		return fmt.Errorf("github request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "cartographer-server-git")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client().Do(req)
	if err != nil {
		return fmt.Errorf("github %s %s: %w", method, endpoint, err)
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return fmt.Errorf("github %s %s: read response: %w", method, endpoint, readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := redactForgeError(strings.TrimSpace(string(data)), g.Token)
		return fmt.Errorf("github %s %s: HTTP %d: %s", method, endpoint, resp.StatusCode, message)
	}
	if out != nil && len(data) != 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("github %s %s: decode response: %w", method, endpoint, err)
		}
	}
	return nil
}

func redactForgeError(message, token string) string {
	if token == "" {
		return message
	}
	return strings.ReplaceAll(message, token, "[redacted]")
}

type githubPR struct {
	Number         int    `json:"number"`
	HTMLURL        string `json:"html_url"`
	State          string `json:"state"`
	Merged         bool   `json:"merged"`
	MergeCommitSHA string `json:"merge_commit_sha"`
	Head           struct {
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		SHA string `json:"sha"`
	} `json:"base"`
}

func asPR(p githubPR) PullRequest {
	return PullRequest{Number: p.Number, URL: p.HTMLURL, HeadSHA: p.Head.SHA, BaseSHA: p.Base.SHA, State: p.State, Merged: p.Merged, MergeSHA: p.MergeCommitSHA}
}

func (g *GitHubForge) FindOpenPR(ctx context.Context, owner, repo, head, base string) ([]PullRequest, error) {
	var all []PullRequest
	for page := 1; ; page++ {
		var prs []githubPR
		endpoint := fmt.Sprintf("/repos/%s/%s/pulls?state=open&head=%s&base=%s&per_page=100&page=%d", pathEscape(owner), pathEscape(repo), url.QueryEscape(owner+":"+head), url.QueryEscape(base), page)
		if err := g.request(ctx, http.MethodGet, endpoint, nil, &prs); err != nil {
			return nil, err
		}
		for _, pr := range prs {
			all = append(all, asPR(pr))
		}
		if len(prs) < 100 {
			return all, nil
		}
	}
}

func (g *GitHubForge) CreatePR(ctx context.Context, owner, repo, head, base, title, body string) (PullRequest, error) {
	var result githubPR
	if err := g.request(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/pulls", pathEscape(owner), pathEscape(repo)), map[string]string{"head": head, "base": base, "title": title, "body": body}, &result); err != nil {
		return PullRequest{}, err
	}
	// The label is harmless metadata, but is part of the operational contract.
	if err := g.request(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/issues/%d/labels", pathEscape(owner), pathEscape(repo), result.Number), map[string][]string{"labels": {"cartographer"}}, nil); err != nil {
		return PullRequest{}, err
	}
	return asPR(result), nil
}

func (g *GitHubForge) GetPR(ctx context.Context, owner, repo string, number int) (PullRequest, error) {
	var pr githubPR
	if err := g.request(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/pulls/%d", pathEscape(owner), pathEscape(repo), number), nil, &pr); err != nil {
		return PullRequest{}, err
	}
	return asPR(pr), nil
}

func (g *GitHubForge) PRReady(ctx context.Context, owner, repo string, number int) (bool, error) {
	type review struct {
		State string `json:"state"`
		User  struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	latest := map[string]string{}
	for page := 1; ; page++ {
		var reviews []review
		if err := g.request(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews?per_page=100&page=%d", pathEscape(owner), pathEscape(repo), number, page), nil, &reviews); err != nil {
			return false, err
		}
		// GitHub returns reviews newest first; retain the first observation of
		// each reviewer across pages so a historic approval cannot mask a later
		// changes-requested/dismissal state.
		for _, r := range reviews {
			if _, seen := latest[r.User.Login]; !seen {
				latest[r.User.Login] = strings.ToUpper(r.State)
			}
		}
		if len(reviews) < 100 {
			break
		}
	}
	approved := false
	for _, state := range latest {
		if state == "APPROVED" {
			approved = true
		}
	}
	if !approved {
		return false, nil
	}
	pr, err := g.GetPR(ctx, owner, repo, number)
	if err != nil {
		return false, err
	}
	var checks struct {
		TotalCount int `json:"total_count"`
		CheckRuns  []struct {
			Conclusion string `json:"conclusion"`
			Status     string `json:"status"`
		} `json:"check_runs"`
	}
	if err := g.request(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs?per_page=100", pathEscape(owner), pathEscape(repo), pathEscape(pr.HeadSHA)), nil, &checks); err != nil {
		return false, err
	}
	if checks.TotalCount == 0 {
		return true, nil
	}
	for _, c := range checks.CheckRuns {
		if c.Status != "completed" || c.Conclusion != "success" {
			return false, nil
		}
	}
	return true, nil
}

func (g *GitHubForge) MergeSquash(ctx context.Context, owner, repo string, number int, sha string) (MergeResult, error) {
	var response struct {
		Merged bool   `json:"merged"`
		SHA    string `json:"sha"`
	}
	err := g.request(ctx, http.MethodPut, fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", pathEscape(owner), pathEscape(repo), number), map[string]string{"sha": sha, "merge_method": "squash"}, &response)
	if err != nil {
		return MergeResult{}, err
	}
	if !response.Merged || response.SHA == "" {
		return MergeResult{}, fmt.Errorf("github squash merge was not confirmed")
	}
	return MergeResult{Merged: true, SHA: response.SHA}, nil
}

func pathEscape(s string) string { return url.PathEscape(s) }
