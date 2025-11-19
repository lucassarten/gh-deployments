/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/briandowns/spinner"
	"github.com/cli/cli/v2/pkg/cmd/factory"
	"github.com/cli/cli/v2/pkg/cmdutil"
	repository "github.com/cli/go-gh/v2/pkg/repository"
	"github.com/google/go-github/v74/github"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	poll bool
	// filters and non-interactive flags
	filterActor    string
	filterEvent    string
	filterWorkflow string
	sortBy         string
	sortOrder      string
	jobNamesFlag   string
	envAll         bool
	runID          int64
	actionFlag     string
)

// reviewCmd represents the review command
var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Review a pending deployment",
	Long:  `Review and approve or reject pending deployments for GitHub Actions workflows.`,
	Run: func(cmd *cobra.Command, args []string) {
		review(factory.New("1"))
	},
}

func init() {
	rootCmd.AddCommand(reviewCmd)

	reviewCmd.Flags().BoolVarP(&poll, "poll", "p", false, "Poll for pending deployments if none are found")
	// filtering and sorting
	reviewCmd.Flags().StringVar(&filterActor, "actor", "", "Filter workflows by actor login")
	reviewCmd.Flags().StringVar(&filterEvent, "event", "", "Filter workflows by event (e.g. push, pull_request)")
	reviewCmd.Flags().StringVar(&filterWorkflow, "workflow", "", "Workflow yaml file name to act on (e.g. deploy.yml)")
	reviewCmd.Flags().StringVar(&sortBy, "sort", "created_at", "Sort by: created_at, actor, name")
	reviewCmd.Flags().StringVar(&sortOrder, "order", "desc", "Sort order: asc or desc")

	// non-interactive / batch flags
	reviewCmd.Flags().StringVar(&jobNamesFlag, "job-names", "", "Comma-separated job names to act on (non-interactive)")
	reviewCmd.Flags().BoolVar(&envAll, "env-all", false, "Act on all pending environments for the selected workflow (non-interactive)")
	reviewCmd.Flags().StringVar(&actionFlag, "action", "", "Action to perform in non-interactive mode: approve or reject")
	reviewCmd.Flags().Int64Var(&runID, "run", 0, "Workflow run id to act on")

	s.Suffix = " Fetching workflows..."
	s.Color("bold", "fgBlue")
}

var actions = []string{"Approve", "Reject"}

var s = spinner.New(spinner.CharSets[11], 100*time.Millisecond, spinner.WithWriter(os.Stderr))

func review(f *cmdutil.Factory) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		log.Fatal("Unauthorized: No token present")
	}

	ctx := context.Background()
	client := NewGitHubClient(token)

	// determine repository (string -> parsed repo)
	repoStr := repo
	if repoStr == "" {
		fullName, err := repository.Current()
		if err != nil {
			log.Fatalf("failed to get current repository: %v", err)
		}
		repoStr = fullName.Owner + "/" + fullName.Name
	}

	parsedRepo, err := repository.Parse(repoStr)
	if err != nil {
		log.Fatalf("failed to parse repository: %v", err)
	}

	// fetch pending workflow runs
	s.Start()
	pendingRuns, err := ListPendingWorkflowRuns(ctx, client, parsedRepo.Owner, parsedRepo.Name, filterActor, filterEvent, poll)
	if err != nil {
		s.Stop()
		log.Fatalf("failed to list workflows: %v", err)
	}
	if len(pendingRuns) == 0 {
		s.Stop()
		fmt.Printf("No pending workflows in %s/%s", parsedRepo.Owner, parsedRepo.Name)
		return
	}

	wfMap, _ := BuildWorkflowMap(ctx, client, parsedRepo.Owner, parsedRepo.Name)
	entries := BuildRunEntries(pendingRuns, wfMap)
	filtered := FilterRunEntries(entries, filterWorkflow)
	SortRunEntries(filtered, sortBy, sortOrder)

	candidates, err := BuildCandidates(ctx, client, parsedRepo.Owner, parsedRepo.Name, filtered)
	if err != nil {
		s.Stop()
		log.Fatalf("failed to build candidate list: %v", err)
	}
	s.Stop()

	nonInteractive := actionFlag != ""

	// determine workflow run
	selectedRun, err := SelectWorkflowRun(f, filtered, candidates, runID, nonInteractive)
	if err != nil {
		log.Fatalf("failed to select workflow run: %v", err)
	}

	// determine target environments to act on
	deployments, _, err := client.Actions.GetPendingDeployments(ctx, parsedRepo.Owner, parsedRepo.Name, *selectedRun.ID)
	if err != nil {
		log.Fatalf("failed to get pending deployments: %v", err)
	}
	if len(deployments) == 0 {
		log.Fatalf("no pending deployments found for workflow %s", *selectedRun.Name)
	}

	targetEnvIDs, envDisplay, err := DetermineTargetEnvIDs(ctx, client, f, parsedRepo.Owner, parsedRepo.Name, selectedRun, deployments, nonInteractive, envAll, jobNamesFlag)
	if err != nil {
		log.Fatalf("%v", err)
	}

	// determine action
	actionVal, err := DetermineAction(f, actionFlag, nonInteractive)
	if err != nil {
		log.Fatalf("%v", err)
	}

	req := &github.PendingDeploymentsRequest{
		EnvironmentIDs: targetEnvIDs,
		State:          actionVal,
		Comment:        fmt.Sprintf("%s via gh-deployments", actionVal),
	}

	_, _, err = client.Actions.PendingDeployments(ctx, parsedRepo.Owner, parsedRepo.Name, *selectedRun.ID, req)
	if err != nil {
		log.Fatalf("failed to approve/reject deployment: %v", err)
	}

	out := f.IOStreams.Out
	cs := f.IOStreams.ColorScheme()
	fmt.Fprintf(out, "%s Job %s to %s %s", cs.SuccessIcon(), cs.Cyan(*selectedRun.Name), cs.Cyan(envDisplay), actionVal)
	fmt.Fprintln(out)
}

type RunEntry struct {
	Run          *github.WorkflowRun
	WorkflowFile string
}

func NewGitHubClient(token string) *github.Client {
	c := github.NewClient(nil)
	if token != "" {
		c = c.WithAuthToken(token)
	}
	return c
}

func ListPendingWorkflowRuns(ctx context.Context, client *github.Client, owner, name, actor, event string, poll bool) ([]*github.WorkflowRun, error) {
	var pendingWorkflows *github.WorkflowRuns
	for {
		opts := &github.ListWorkflowRunsOptions{Status: "waiting"}
		if actor != "" {
			opts.Actor = actor
		}
		if event != "" {
			opts.Event = event
		}
		var err error
		pendingWorkflows, _, err = client.Actions.ListRepositoryWorkflowRuns(ctx, owner, name, opts)
		if err != nil {
			return nil, err
		}
		if len(pendingWorkflows.WorkflowRuns) > 0 || !poll {
			break
		}
		s.Suffix = " No pending workflows found, polling..."
		time.Sleep(5 * time.Second)
	}
	return pendingWorkflows.WorkflowRuns, nil
}

func BuildWorkflowMap(ctx context.Context, client *github.Client, owner, name string) (map[int64]string, error) {
	wfMap := map[int64]string{}
	list, _, err := client.Actions.ListWorkflows(ctx, owner, name, &github.ListOptions{})
	if err != nil {
		return wfMap, err
	}
	if list == nil {
		return wfMap, nil
	}
	for _, w := range list.Workflows {
		if w.ID != nil && w.Path != nil {
			wfMap[*w.ID] = path.Base(*w.Path)
		}
	}
	return wfMap, nil
}

func BuildRunEntries(runs []*github.WorkflowRun, wfMap map[int64]string) []RunEntry {
	entries := make([]RunEntry, 0, len(runs))
	for _, r := range runs {
		wfFile := ""
		if r.WorkflowID != nil {
			if v, ok := wfMap[*r.WorkflowID]; ok {
				wfFile = v
			}
		}
		entries = append(entries, RunEntry{Run: r, WorkflowFile: wfFile})
	}
	return entries
}

func FilterRunEntries(entries []RunEntry, filterWorkflow string) []RunEntry {
	if filterWorkflow == "" { // no need to
		return entries
	}
	filtered := make([]RunEntry, 0, len(entries))
	q := strings.ToLower(filterWorkflow)
	for _, e := range entries {
		wf := strings.ToLower(e.WorkflowFile)
		if wf == q || strings.TrimSuffix(wf, path.Ext(wf)) == q || strings.Contains(wf, q) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

func SortRunEntries(filtered []RunEntry, sortBy, sortOrder string) {
	switch strings.ToLower(sortBy) {
	case "actor":
		sort.Slice(filtered, func(i, j int) bool {
			a := strings.ToLower(filtered[i].Run.GetActor().GetLogin())
			b := strings.ToLower(filtered[j].Run.GetActor().GetLogin())
			if strings.ToLower(sortOrder) == "asc" {
				return a < b
			}
			return a > b
		})
	case "name":
		sort.Slice(filtered, func(i, j int) bool {
			a := strings.ToLower(filtered[i].Run.GetDisplayTitle())
			b := strings.ToLower(filtered[j].Run.GetDisplayTitle())
			if strings.ToLower(sortOrder) == "asc" {
				return a < b
			}
			return a > b
		})
	default:
		sort.Slice(filtered, func(i, j int) bool {
			ai := filtered[i].Run.GetCreatedAt()
			aj := filtered[j].Run.GetCreatedAt()
			if strings.ToLower(sortOrder) == "asc" {
				return ai.Before(aj.Time)
			}
			return ai.After(aj.Time)
		})
	}
}

func BuildCandidates(ctx context.Context, client *github.Client, owner, name string, filtered []RunEntry) ([]string, error) {
	candidates := make([]string, len(filtered))
	for i, e := range filtered {
		createdLocal := e.Run.GetCreatedAt().Local().Format("02/01/06 15:04:05")
		displayFile := e.WorkflowFile
		// fetch job names
		jobs, _, jerr := client.Actions.ListWorkflowJobs(ctx, owner, name, *e.Run.ID, &github.ListWorkflowJobsOptions{})
		if jerr != nil {
			log.Debugf("failed to list workflow jobs for run %d: %v", *e.Run.ID, jerr)
			return nil, jerr
		}
		jobNames := []string{}
		for _, j := range jobs.Jobs {
			if j.Status != nil && *j.Status == "waiting" {
				jobNames = append(jobNames, j.GetName())
			}
		}
		jobName := strings.Join(jobNames, ", ")
		// fetch environment names
		deps, _, derr := client.Actions.GetPendingDeployments(ctx, owner, name, *e.Run.ID)
		envNames := []string{}
		if derr == nil && deps != nil {
			for _, d := range deps {
				if d.Environment != nil && d.Environment.Name != nil {
					envNames = append(envNames, *d.Environment.Name)
				}
			}
		}
		envDisplay := strings.Join(envNames, ", ")
		// build candidate string
		if envDisplay != "" {
			candidates[i] = fmt.Sprintf("%s [%s] (%s) | %s - %s", e.Run.GetDisplayTitle(), jobName, envDisplay, displayFile, e.Run.GetActor().GetLogin())
		} else {
			candidates[i] = fmt.Sprintf("%s [%s] | %s - %s - %s", e.Run.GetDisplayTitle(), jobName, displayFile, e.Run.GetActor().GetLogin(), createdLocal)
		}
	}
	return candidates, nil
}

func SelectWorkflowRun(f *cmdutil.Factory, filtered []RunEntry, candidates []string, runID int64, nonInteractive bool) (*github.WorkflowRun, error) {
	// if a specific run ID is provided, use that
	if runID != 0 {
		for _, e := range filtered {
			if e.Run.ID != nil && *e.Run.ID == runID {
				return e.Run, nil
			}
		}
		return nil, fmt.Errorf("failed to find workflow run %d", runID)
	}

	// if non-interactive mode is enabled we need to ensure only one candidate
	if nonInteractive {
		if len(filtered) == 1 {
			return filtered[0].Run, nil
		}
		return nil, fmt.Errorf("multiple pending workflows found — in non-interactive mode provide --workflow to disambiguate")
	}

	// otherwise prompt user to select
	idx, err := f.Prompter.Select("Select Workflow", "", candidates)
	if err != nil {
		if strings.Contains(err.Error(), "interrupt") {
			return nil, fmt.Errorf("interrupt")
		}
		return nil, err
	}
	return filtered[idx].Run, nil
}

func DetermineTargetEnvIDs(ctx context.Context, client *github.Client, f *cmdutil.Factory, owner, name string, selectedRun *github.WorkflowRun, deployments []*github.PendingDeployment, nonInteractive, envAll bool, jobNamesFlag string) ([]int64, string, error) {
	var targetEnvIDs []int64
	var envDisplay string

	// single deployment
	if len(deployments) == 1 {
		if deployments[0].Environment != nil && deployments[0].Environment.ID != nil {
			targetEnvIDs = []int64{*deployments[0].Environment.ID}
			if deployments[0].Environment.Name != nil {
				envDisplay = *deployments[0].Environment.Name
			}
		}
		return targetEnvIDs, envDisplay, nil
	}

	if nonInteractive {
		if envAll { // all jobs
			for _, d := range deployments {
				if d.Environment != nil && d.Environment.ID != nil {
					targetEnvIDs = append(targetEnvIDs, *d.Environment.ID)
				}
			}
			envDisplay = fmt.Sprintf("%d environments", len(targetEnvIDs))
		} else if jobNamesFlag != "" { // find jobs by name
			parts := strings.Split(jobNamesFlag, ",")
			jobs, _, jerr := client.Actions.ListWorkflowJobs(ctx, owner, name, *selectedRun.ID, &github.ListWorkflowJobsOptions{})
			if jerr != nil {
				log.Debugf("failed to list jobs for run %d: %v", *selectedRun.ID, jerr)
			}
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				found := false
				for idx, j := range jobs.Jobs {
					if j.Status != nil && *j.Status == "waiting" {
						if strings.EqualFold(j.GetName(), p) {
							targetEnvIDs = append(targetEnvIDs, *deployments[idx].Environment.ID)
							found = true
							break
						}
					}
				}
				if !found {
					return nil, "", fmt.Errorf("job name '%s' not found among workflow jobs", p)
				}
			}
			envDisplay = fmt.Sprintf("%d environment(s)", len(targetEnvIDs))
		} else {
			return nil, "", fmt.Errorf("non-interactive mode requires --job-names or --env-all when multiple deployments are present")
		}
		return targetEnvIDs, envDisplay, nil
	}

	// otherwise we have multiple deployments and need to narrow down

	jobs, _, jerr := client.Actions.ListWorkflowJobs(ctx, owner, name, *selectedRun.ID, &github.ListWorkflowJobsOptions{})
	if jerr != nil {
		log.Debugf("failed to list workflow jobs for run %d: %v", *selectedRun.ID, jerr)
	}

	if len(jobs.Jobs) > 1 {
		jobOptions := make([]string, len(jobs.Jobs)+1)
		jobOptions[0] = fmt.Sprintf("All (%d jobs)", len(jobs.Jobs))
		for i, j := range jobs.Jobs {
			if j.Status != nil && *j.Status == "waiting" {
				jobName := j.GetName()
				mappedEnv := ""
				if deployments[i].Environment != nil && deployments[i].Environment.Name != nil {
					mappedEnv = *deployments[i].Environment.Name
				}
				if mappedEnv != "" {
					jobOptions[i+1] = fmt.Sprintf("%s (%s)", jobName, mappedEnv)
				} else {
					jobOptions[i+1] = jobName
				}
			}
		}

		envIdx, err := f.Prompter.Select("Select Job", "", jobOptions)
		if err != nil {
			if strings.Contains(err.Error(), "interrupt") {
				return nil, "", fmt.Errorf("interrupt")
			}
			return nil, "", err
		}

		// all jobs
		if envIdx == 0 {
			for _, d := range deployments {
				if d.Environment != nil && d.Environment.ID != nil {
					targetEnvIDs = append(targetEnvIDs, *d.Environment.ID)
				}
			}
			envDisplay = fmt.Sprintf("%d environments", len(targetEnvIDs))
			return targetEnvIDs, envDisplay, nil
		}

		// specific job selected
		if deployments[envIdx-1].Environment != nil && deployments[envIdx-1].Environment.ID != nil {
			targetEnvIDs = []int64{*deployments[envIdx-1].Environment.ID}
			if deployments[envIdx-1].Environment.Name != nil {
				envDisplay = *deployments[envIdx-1].Environment.Name
			}
			return targetEnvIDs, envDisplay, nil
		}
	}

	return nil, "", fmt.Errorf("unable to determine target environments")
}

func DetermineAction(f *cmdutil.Factory, actionFlag string, nonInteractive bool) (string, error) {
	if nonInteractive {
		if strings.ToLower(actionFlag) != "approve" && strings.ToLower(actionFlag) != "reject" {
			return "", fmt.Errorf("invalid --action value '%s', must be 'approve' or 'reject'", actionFlag)
		}
		if strings.ToLower(actionFlag) == "approve" {
			return "approved", nil
		}
		return "rejected", nil
	}
	actionIdx, err := f.Prompter.Select("Select Action", "Approve", actions)
	if err != nil {
		if strings.Contains(err.Error(), "interrupt") {
			return "", fmt.Errorf("interrupt")
		}
		return "", err
	}
	if actionIdx == 1 {
		return "rejected", nil
	}
	return "approved", nil
}
