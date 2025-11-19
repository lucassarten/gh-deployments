package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/google/go-github/v74/github"
)

// helper to create a github client that points to our test server
func clientForServer(s *httptest.Server) *github.Client {
	c := github.NewClient(nil)
	u, _ := url.Parse(s.URL + "/")
	c.BaseURL = u
	c.UploadURL = u
	return c
}

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	// tests run with working dir == package directory, so json is a sibling
	p := filepath.Join("json", name)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", p, err)
	}
	return b
}

func TestDetermineAction_NonInteractive(t *testing.T) {
	// approve
	v, err := DetermineAction(nil, "approve", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "approved" {
		t.Fatalf("expected approved, got %s", v)
	}

	// reject
	v, err = DetermineAction(nil, "reject", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "rejected" {
		t.Fatalf("expected rejected, got %s", v)
	}

	// invalid
	_, err = DetermineAction(nil, "invalid", true)
	if err == nil {
		t.Fatalf("expected error for invalid action")
	}
}

func TestDetermineTargetEnvIDs_NonInteractive_EnvAll(t *testing.T) {
	// create two pending deployments
	deployments := []*github.PendingDeployment{
		{Environment: &github.PendingDeploymentEnvironment{ID: github.Ptr(int64(11)), Name: github.Ptr("env1")}},
		{Environment: &github.PendingDeploymentEnvironment{ID: github.Ptr(int64(22)), Name: github.Ptr("env2")}},
	}

	// non-interactive with envAll should return both IDs
	ids, disp, err := DetermineTargetEnvIDs(context.Background(), nil, nil, "owner", "repo", &github.WorkflowRun{ID: github.Ptr(int64(123))}, deployments, true, true, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %d (%v)", len(ids), ids)
	}
	if disp == "" {
		t.Fatalf("expected non-empty display")
	}
}

func TestDetermineTargetEnvIDs_NonInteractive_JobNames(t *testing.T) {
	// two deployments in the same order jobs will be returned
	deployments := []*github.PendingDeployment{
		{Environment: &github.PendingDeploymentEnvironment{ID: github.Ptr(int64(101)), Name: github.Ptr("env-A")}},
		{Environment: &github.PendingDeploymentEnvironment{ID: github.Ptr(int64(202)), Name: github.Ptr("env-B")}},
	}

	// create a test server that returns a jobs listing for run 123
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/actions/runs/123/jobs", func(w http.ResponseWriter, r *http.Request) {
		// craft jobs JSON such that index 0 -> jobA, index 1 -> jobB
		type job struct {
			ID     int64  `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		}
		resp := struct {
			TotalCount int   `json:"total_count"`
			Jobs       []job `json:"jobs"`
		}{
			TotalCount: 2,
			Jobs:       []job{{ID: 1, Name: "jobA", Status: "waiting"}, {ID: 2, Name: "jobB", Status: "waiting"}},
		}
		b, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write(b)
	})

	s := httptest.NewServer(mux)
	defer s.Close()

	client := clientForServer(s)

	// ask for jobA only
	ids, disp, err := DetermineTargetEnvIDs(context.Background(), client, nil, "owner", "repo", &github.WorkflowRun{ID: github.Ptr(int64(123))}, deployments, true, false, "jobA")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected 1 id, got %d (%v)", len(ids), ids)
	}
	if ids[0] != 101 {
		t.Fatalf("expected id 101, got %d", ids[0])
	}
	if disp == "" {
		t.Fatalf("expected non-empty display")
	}
}

func TestListPendingWorkflowRuns_UsesFixture(t *testing.T) {
	// serve fixture for repository workflow runs
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/actions/runs", func(w http.ResponseWriter, r *http.Request) {
		b := loadFixture(t, "client.Actions.ListRepositoryWorkflowRuns.json")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write(b)
	})

	s := httptest.NewServer(mux)
	defer s.Close()

	client := clientForServer(s)

	runs, err := ListPendingWorkflowRuns(context.Background(), client, "owner", "repo", "", "", false)
	if err != nil {
		t.Fatalf("unexpected error listing runs: %v", err)
	}
	if len(runs) == 0 {
		t.Fatalf("expected some runs from fixture, got 0")
	}
	// basic sanity check: every run should have an ID
	for _, r := range runs {
		if r.ID == nil {
			t.Fatalf("run has nil ID: %+v", r)
		}
	}
	// also ensure our helper client base URL was used (sanity)
	if client.BaseURL == nil {
		t.Fatalf("client BaseURL unexpectedly nil")
	}
}

func TestSelectWorkflowRun_NonInteractive_MultipleError(t *testing.T) {
	runs := []RunEntry{
		{Run: &github.WorkflowRun{ID: github.Ptr(int64(1))}},
		{Run: &github.WorkflowRun{ID: github.Ptr(int64(2))}},
	}
	// non-interactive without runID should error when multiple
	_, err := SelectWorkflowRun(nil, runs, []string{"a", "b"}, 0, true)
	if err == nil {
		t.Fatalf("expected error when multiple runs present in non-interactive mode")
	}
}

func TestSelectWorkflowRun_RunIDLookup(t *testing.T) {
	runs := []RunEntry{
		{Run: &github.WorkflowRun{ID: github.Ptr(int64(10))}},
		{Run: &github.WorkflowRun{ID: github.Ptr(int64(20))}},
	}
	r, err := SelectWorkflowRun(nil, runs, nil, 20, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.GetID() != 20 {
		t.Fatalf("expected run id 20, got %d", r.GetID())
	}

	_, err = SelectWorkflowRun(nil, runs, nil, 999, false)
	if err == nil {
		t.Fatalf("expected error for missing run id")
	}
}

func TestSortRunEntries_ByActorNameAndCreated(t *testing.T) {
	now := time.Now()
	entries := []RunEntry{
		{Run: &github.WorkflowRun{ID: github.Ptr(int64(1)), DisplayTitle: github.Ptr("B Title"), WorkflowID: github.Ptr(int64(1)), Actor: &github.User{Login: github.Ptr("bob")}, CreatedAt: &github.Timestamp{Time: now.Add(-2 * time.Hour)}}},
		{Run: &github.WorkflowRun{ID: github.Ptr(int64(2)), DisplayTitle: github.Ptr("A Title"), WorkflowID: github.Ptr(int64(2)), Actor: &github.User{Login: github.Ptr("alice")}, CreatedAt: &github.Timestamp{Time: now.Add(-1 * time.Hour)}}},
	}

	// sort by actor asc
	SortRunEntries(entries, "actor", "asc")
	if entries[0].Run.GetActor().GetLogin() != "alice" {
		t.Fatalf("expected alice first when sorting actor asc")
	}

	// sort by name asc
	SortRunEntries(entries, "name", "asc")
	if entries[0].Run.GetDisplayTitle() != "A Title" {
		t.Fatalf("expected A Title first when sorting name asc")
	}

	// sort by created_at desc
	SortRunEntries(entries, "created_at", "desc")
	a := entries[0].Run.GetCreatedAt().Time
	b := entries[1].Run.GetCreatedAt().Time
	if !a.After(b) {
		t.Fatalf("expected most recent first when sorting created_at desc")
	}
}

func TestBuildCandidates_WithFixtures(t *testing.T) {
	// prepare run entry
	run := &github.WorkflowRun{ID: github.Ptr(int64(29679449)), DisplayTitle: github.Ptr("Update README.md"), Actor: &github.User{Login: github.Ptr("octocat")}, WorkflowID: github.Ptr(int64(159038))}
	entries := []RunEntry{{Run: run, WorkflowFile: "build.yml"}}

	mux := http.NewServeMux()
	// jobs endpoint
	mux.HandleFunc("/repos/owner/repo/actions/runs/29679449/jobs", func(w http.ResponseWriter, r *http.Request) {
		b := loadFixture(t, "client.Actions.ListWorkflowJobs.json")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write(b)
	})
	// pending deployments endpoint
	mux.HandleFunc("/repos/owner/repo/actions/runs/29679449/pending_deployments", func(w http.ResponseWriter, r *http.Request) {
		b := loadFixture(t, "client.Actions.GetPendingDeployments.json")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write(b)
	})

	s := httptest.NewServer(mux)
	defer s.Close()

	client := clientForServer(s)
	candidates, err := BuildCandidates(context.Background(), client, "owner", "repo", entries)
	if err != nil {
		t.Fatalf("BuildCandidates errored: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	c := candidates[0]
	if !strings.Contains(c, "staging") {
		t.Fatalf("expected candidate to contain staging env, got: %s", c)
	}
	if !strings.Contains(c, "build.yml") {
		t.Fatalf("expected candidate to contain workflow file name, got: %s", c)
	}
	if !strings.Contains(c, "octocat") {
		t.Fatalf("expected candidate to contain actor login, got: %s", c)
	}
}

func TestFilterRunEntries_Matching(t *testing.T) {
	entries := []RunEntry{
		{WorkflowFile: "deploy.yml", Run: &github.WorkflowRun{DisplayTitle: github.Ptr("one")}},
		{WorkflowFile: "build.yml", Run: &github.WorkflowRun{DisplayTitle: github.Ptr("two")}},
		{WorkflowFile: "deploy", Run: &github.WorkflowRun{DisplayTitle: github.Ptr("three")}},
		{WorkflowFile: "other-deploy.yml", Run: &github.WorkflowRun{DisplayTitle: github.Ptr("four")}},
	}

	// exact match / suffix match (deploy.yml appears in two filenames)
	out := FilterRunEntries(entries, "deploy.yml")
	if len(out) != 2 {
		t.Fatalf("expected 2 matches for deploy.yml, got %d", len(out))
	}

	// basename match (without extension)
	out = FilterRunEntries(entries, "deploy")
	if len(out) != 3 {
		t.Fatalf("expected 3 matches for 'deploy' basename/substring, got %d", len(out))
	}

	// substring
	out = FilterRunEntries(entries, "other")
	if len(out) != 1 || out[0].WorkflowFile != "other-deploy.yml" {
		t.Fatalf("expected match for other-deploy.yml, got %v", out)
	}
}

func TestBuildWorkflowMap_UsesFixture(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/actions/workflows", func(w http.ResponseWriter, r *http.Request) {
		b := loadFixture(t, "client.Actions.ListWorkflows.json")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write(b)
	})
	s := httptest.NewServer(mux)
	defer s.Close()

	client := clientForServer(s)
	wfMap, err := BuildWorkflowMap(context.Background(), client, "owner", "repo")
	if err != nil {
		t.Fatalf("BuildWorkflowMap error: %v", err)
	}
	if len(wfMap) == 0 {
		t.Fatalf("expected workflow map to have entries")
	}
	// fixtures include workflow id 161335 with path .github/workflows/blank.yaml
	if wfMap[161335] != "blank.yaml" {
		t.Fatalf("expected id 161335 -> blank.yaml, got %v", wfMap[161335])
	}
}

// fakePrompter implements a minimal Prompter interface used by cmdutil.Factory
type fakePrompter struct {
	idx int
	err error
}

func (f *fakePrompter) Select(label, def string, opts []string) (int, error) {
	return f.idx, f.err
}

func (f *fakePrompter) AuthToken() (string, error) {
	return "", nil
}

func (f *fakePrompter) Confirm(label string, def bool) (bool, error) {
	return def, nil
}

func (f *fakePrompter) MultiSelect(prompt string, defaults []string, options []string) ([]int, error) {
	// return empty selection
	return []int{}, nil
}

func (f *fakePrompter) Input(prompt string, def string) (string, error) {
	return def, nil
}

func (f *fakePrompter) Password(prompt string) (string, error) {
	return "", nil
}

func (f *fakePrompter) ConfirmDeletion(requiredValue string) error {
	return nil
}

func (f *fakePrompter) InputHostname() (string, error) {
	return "", nil
}

func (f *fakePrompter) MarkdownEditor(prompt, def string, blankAllowed bool) (string, error) {
	return def, nil
}

func TestSelectWorkflowRun_Interactive(t *testing.T) {
	runs := []RunEntry{
		{Run: &github.WorkflowRun{ID: github.Ptr(int64(100)), DisplayTitle: github.Ptr("one")}},
		{Run: &github.WorkflowRun{ID: github.Ptr(int64(200)), DisplayTitle: github.Ptr("two")}},
	}
	candidates := []string{"first", "second"}

	var f cmdutil.Factory
	f.Prompter = &fakePrompter{idx: 1}

	sel, err := SelectWorkflowRun(&f, runs, candidates, 0, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.GetID() != 200 {
		t.Fatalf("expected selected run id 200, got %d", sel.GetID())
	}
}

func TestDetermineTargetEnvIDs_Interactive_SelectSpecificJob(t *testing.T) {
	// two deployments corresponding to two jobs
	deployments := []*github.PendingDeployment{
		{Environment: &github.PendingDeploymentEnvironment{ID: github.Ptr(int64(301)), Name: github.Ptr("env-1")}},
		{Environment: &github.PendingDeploymentEnvironment{ID: github.Ptr(int64(302)), Name: github.Ptr("env-2")}},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/actions/runs/555/jobs", func(w http.ResponseWriter, r *http.Request) {
		// return two waiting jobs
		resp := map[string]interface{}{
			"total_count": 2,
			"jobs": []map[string]interface{}{
				{"id": 1, "name": "job-one", "status": "waiting"},
				{"id": 2, "name": "job-two", "status": "waiting"},
			},
		}
		b, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write(b)
	})

	s := httptest.NewServer(mux)
	defer s.Close()

	client := clientForServer(s)

	var f cmdutil.Factory
	// select second job (env index 2 in options -> maps to deployments[1])
	f.Prompter = &fakePrompter{idx: 2}

	ids, disp, err := DetermineTargetEnvIDs(context.Background(), client, &f, "owner", "repo", &github.WorkflowRun{ID: github.Ptr(int64(555))}, deployments, false, false, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 1 || ids[0] != 302 {
		t.Fatalf("expected selected env id 302, got %v", ids)
	}
	if disp != "env-2" {
		t.Fatalf("expected display env-2, got %s", disp)
	}
}

func TestListPendingWorkflowRuns_ForwardsActorAndEvent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/actions/runs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("actor") != "alice" {
			t.Fatalf("expected actor=alice, got %s", q.Get("actor"))
		}
		if q.Get("event") != "push" {
			t.Fatalf("expected event=push, got %s", q.Get("event"))
		}
		b := loadFixture(t, "client.Actions.ListRepositoryWorkflowRuns.json")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write(b)
	})
	s := httptest.NewServer(mux)
	defer s.Close()

	client := clientForServer(s)
	runs, err := ListPendingWorkflowRuns(context.Background(), client, "owner", "repo", "alice", "push", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runs) == 0 {
		t.Fatalf("expected runs, got 0")
	}
}

func TestFilterRunEntries_UsesFilterWorkflowFlag(t *testing.T) {
	old := filterWorkflow
	defer func() { filterWorkflow = old }()
	filterWorkflow = "deploy"

	entries := []RunEntry{
		{WorkflowFile: "deploy.yml"},
		{WorkflowFile: "build.yml"},
		{WorkflowFile: "other-deploy.yml"},
	}
	out := FilterRunEntries(entries, filterWorkflow)
	if len(out) != 2 {
		t.Fatalf("expected 2 matches for filterWorkflow=deploy, got %d", len(out))
	}
}

func TestSortRunEntries_OrderFlagCreatedAtAsc(t *testing.T) {
	oldOrder := sortOrder
	defer func() { sortOrder = oldOrder }()
	sortOrder = "asc"

	now := time.Now()
	entries := []RunEntry{
		{Run: &github.WorkflowRun{ID: github.Ptr(int64(1)), CreatedAt: &github.Timestamp{Time: now.Add(-1 * time.Hour)}}},
		{Run: &github.WorkflowRun{ID: github.Ptr(int64(2)), CreatedAt: &github.Timestamp{Time: now.Add(-2 * time.Hour)}}},
	}
	SortRunEntries(entries, "created_at", sortOrder)
	if entries[0].Run.GetID() != 2 {
		t.Fatalf("expected older run (id 2) first for asc, got %d", entries[0].Run.GetID())
	}
}

func TestDetermineTargetEnvIDs_NonInteractive_Flags_Global(t *testing.T) {
	// preserve and restore globals
	oldJobNames := jobNamesFlag
	oldEnvAll := envAll
	defer func() { jobNamesFlag = oldJobNames; envAll = oldEnvAll }()

	deployments := []*github.PendingDeployment{
		{Environment: &github.PendingDeploymentEnvironment{ID: github.Ptr(int64(401)), Name: github.Ptr("e1")}},
		{Environment: &github.PendingDeploymentEnvironment{ID: github.Ptr(int64(402)), Name: github.Ptr("e2")}},
	}

	// job-names non-interactive
	jobNamesFlag = "jobA"
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/actions/runs/777/jobs", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{"total_count": 2, "jobs": []map[string]interface{}{{"id": 1, "name": "jobA", "status": "waiting"}, {"id": 2, "name": "jobB", "status": "waiting"}}}
		b, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write(b)
	})
	s := httptest.NewServer(mux)
	defer s.Close()
	client := clientForServer(s)

	ids, disp, err := DetermineTargetEnvIDs(context.Background(), client, nil, "owner", "repo", &github.WorkflowRun{ID: github.Ptr(int64(777))}, deployments, true, false, jobNamesFlag)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 1 || ids[0] != 401 {
		t.Fatalf("expected env id 401 for jobA, got %v", ids)
	}
	if disp == "" {
		t.Fatalf("expected non-empty display")
	}

	// env-all non-interactive
	envAll = true
	ids2, disp2, err := DetermineTargetEnvIDs(context.Background(), client, nil, "owner", "repo", &github.WorkflowRun{ID: github.Ptr(int64(777))}, deployments, true, envAll, "")
	if err != nil {
		t.Fatalf("unexpected error for envAll: %v", err)
	}
	if len(ids2) != 2 {
		t.Fatalf("expected 2 env ids for envAll, got %v", ids2)
	}
	if disp2 == "" {
		t.Fatalf("expected non-empty display for envAll")
	}
}

func TestSelectWorkflowRun_RunID_Global(t *testing.T) {
	oldRun := runID
	defer func() { runID = oldRun }()
	runID = 20
	runs := []RunEntry{{Run: &github.WorkflowRun{ID: github.Ptr(int64(10))}}, {Run: &github.WorkflowRun{ID: github.Ptr(int64(20))}}}
	sel, err := SelectWorkflowRun(nil, runs, nil, runID, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.GetID() != 20 {
		t.Fatalf("expected selected run id 20, got %d", sel.GetID())
	}
}

func TestDetermineTargetEnvIDs_NonInteractive_MissingFlagsError(t *testing.T) {
	deployments := []*github.PendingDeployment{
		{Environment: &github.PendingDeploymentEnvironment{ID: github.Ptr(int64(501)), Name: github.Ptr("e1")}},
		{Environment: &github.PendingDeploymentEnvironment{ID: github.Ptr(int64(502)), Name: github.Ptr("e2")}},
	}

	// non-interactive but no envAll and no jobNamesFlag should error when multiple deployments
	_, _, err := DetermineTargetEnvIDs(context.Background(), nil, nil, "owner", "repo", &github.WorkflowRun{ID: github.Ptr(int64(888))}, deployments, true, false, "")
	if err == nil {
		t.Fatalf("expected error when non-interactive with multiple deployments and no flags")
	}
}

func TestSortRunEntries_TableDriven(t *testing.T) {
	now := time.Now()
	base := []RunEntry{
		{Run: &github.WorkflowRun{ID: github.Ptr(int64(1)), DisplayTitle: github.Ptr("Z Title"), Actor: &github.User{Login: github.Ptr("bob")}, CreatedAt: &github.Timestamp{Time: now}}},
		{Run: &github.WorkflowRun{ID: github.Ptr(int64(2)), DisplayTitle: github.Ptr("A Title"), Actor: &github.User{Login: github.Ptr("alice")}, CreatedAt: &github.Timestamp{Time: now.Add(-2 * time.Hour)}}},
		{Run: &github.WorkflowRun{ID: github.Ptr(int64(3)), DisplayTitle: github.Ptr("M Title"), Actor: &github.User{Login: github.Ptr("carol")}, CreatedAt: &github.Timestamp{Time: now.Add(-1 * time.Hour)}}},
	}

	cases := []struct {
		name      string
		sortBy    string
		sortOrder string
		expected  []int64
	}{
		{"actor asc", "actor", "asc", []int64{2, 1, 3}},
		{"actor desc", "actor", "desc", []int64{3, 1, 2}},
		{"name asc", "name", "asc", []int64{2, 3, 1}},
		{"name desc", "name", "desc", []int64{1, 3, 2}},
		{"created asc", "created_at", "asc", []int64{2, 3, 1}},
		{"created desc", "created_at", "desc", []int64{1, 3, 2}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// copy base
			entries := make([]RunEntry, len(base))
			copy(entries, base)
			SortRunEntries(entries, c.sortBy, c.sortOrder)
			got := make([]int64, len(entries))
			for i, e := range entries {
				got[i] = e.Run.GetID()
			}
			for i := range got {
				if got[i] != c.expected[i] {
					t.Fatalf("case %s: expected order %v, got %v", c.name, c.expected, got)
				}
			}
		})
	}
}
