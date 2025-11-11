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
	envNamesFlag   string
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
	reviewCmd.Flags().StringVar(&envNamesFlag, "env-names", "", "Comma-separated environment names to act on (non-interactive)")
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
	client := github.NewClient(nil).WithAuthToken(token)

	if repo == "" {
		fullName, err := repository.Current()
		if err != nil {
			log.Fatalf("failed to get current repository: %v", err)
		}
		repo = fullName.Owner + "/" + fullName.Name
	}

	repo, err := repository.Parse(repo)
	if err != nil {
		log.Fatalf("failed to parse repository: %v", err)
	}

	s.Start()
	var pendingWorkflows *github.WorkflowRuns
	for {
		opts := &github.ListWorkflowRunsOptions{Status: "waiting"}
		if filterActor != "" {
			opts.Actor = filterActor
		}
		if filterEvent != "" {
			opts.Event = filterEvent
		}

		pendingWorkflows, _, err = client.Actions.ListRepositoryWorkflowRuns(ctx, repo.Owner, repo.Name, opts)
		if err != nil {
			log.Fatalf("failed to list workflows: %v", err)
		}
		if len(pendingWorkflows.WorkflowRuns) > 0 || !poll {
			break
		} else {
			s.Suffix = " No pending workflows found, polling..."
			time.Sleep(5 * time.Second)
		}
	}

	if len(pendingWorkflows.WorkflowRuns) == 0 {
		s.Stop()
		fmt.Printf("No pending workflows in %s/%s", repo.Owner, repo.Name)
		return
	}

	// build run entries that include the workflow filename (yaml path base)
	type runEntry struct {
		run          *github.WorkflowRun
		workflowFile string // base filename of the workflow yml
	}

	// fetch repository workflows once and build a map from workflow ID to path
	wfMap := map[int64]string{}
	if list, _, lerr := client.Actions.ListWorkflows(ctx, repo.Owner, repo.Name, &github.ListOptions{}); lerr == nil && list != nil {
		for _, w := range list.Workflows {
			wfMap[*w.ID] = path.Base(*w.Path)
		}
	}

	entries := make([]runEntry, 0, len(pendingWorkflows.WorkflowRuns))
	for _, r := range pendingWorkflows.WorkflowRuns {
		wfFile := ""
		if v, ok := wfMap[*r.WorkflowID]; ok {
			wfFile = v
		}
		entries = append(entries, runEntry{run: r, workflowFile: wfFile})
	}

	// filter by workflow filename if requested (match base filename, case-insensitive, allow passing without extension)
	filtered := make([]runEntry, 0, len(entries))
	for _, e := range entries {
		if filterWorkflow == "" {
			filtered = append(filtered, e)
			continue
		}
		// match against workflow file (e.g. deploy.yml) or name without extension (e.g. deploy)
		wf := strings.ToLower(e.workflowFile)
		query := strings.ToLower(filterWorkflow)
		if wf == query || strings.TrimSuffix(wf, path.Ext(wf)) == query || strings.Contains(wf, query) {
			filtered = append(filtered, e)
		}
	}

	// sort entries
	switch strings.ToLower(sortBy) {
	case "actor":
		sort.Slice(filtered, func(i, j int) bool {
			a := strings.ToLower(filtered[i].run.GetActor().GetLogin())
			b := strings.ToLower(filtered[j].run.GetActor().GetLogin())
			if strings.ToLower(sortOrder) == "asc" {
				return a < b
			}
			return a > b
		})
	case "name":
		sort.Slice(filtered, func(i, j int) bool {
			a := strings.ToLower(filtered[i].run.GetDisplayTitle())
			b := strings.ToLower(filtered[j].run.GetDisplayTitle())
			if strings.ToLower(sortOrder) == "asc" {
				return a < b
			}
			return a > b
		})
	default: // created_at
		sort.Slice(filtered, func(i, j int) bool {
			ai := filtered[i].run.GetCreatedAt()
			aj := filtered[j].run.GetCreatedAt()
			if strings.ToLower(sortOrder) == "asc" {
				return ai.Before(aj.Time)
			}
			return ai.After(aj.Time)
		})
	}

	candidates := make([]string, len(filtered))
	for i, e := range filtered {
		createdLocal := e.run.GetCreatedAt().Local().Format("02/01/06 15:04:05")
		// include workflow filename in candidate to help users
		displayFile := e.workflowFile
		candidates[i] = fmt.Sprintf("%s [%s] - %s - %s", e.run.GetDisplayTitle(), displayFile, e.run.GetActor().GetLogin(), createdLocal)
	}
	s.Stop()

	// non-interactive mode if --action was provided
	nonInteractive := actionFlag != ""

	var selectedRun *github.WorkflowRun

	// if --run was provided, target that run id directly (try to find among filtered runs first)
	if runID != 0 {
		found := false
		for _, e := range filtered {
			if *e.run.ID == runID {
				selectedRun = e.run
				found = true
				break
			}
		}
		if !found {
			log.Fatalf("failed to find workflow run %d", runID)
		}
	} else {
		// select interactively or use non-interactive rules
		var workflowIdx int
		if nonInteractive {
			if len(filtered) == 1 {
				workflowIdx = 0
			} else {
				log.Fatalf("multiple pending workflows found — in non-interactive mode provide --workflow to disambiguate")
			}
			selectedRun = filtered[workflowIdx].run
		} else {
			var err error
			workflowIdx, err = f.Prompter.Select("Select Workflow", "", candidates)
			if err != nil {
				if strings.Contains(err.Error(), "interrupt") {
					return
				}
				log.Fatal(err.Error())
				return
			}
			selectedRun = filtered[workflowIdx].run
		}
	}
	deployments, _, err := client.Actions.GetPendingDeployments(ctx, repo.Owner, repo.Name, *selectedRun.ID)
	if err != nil {
		log.Fatalf("failed to get pending deployments: %v", err)
	}

	if len(deployments) == 0 {
		log.Fatalf("no pending deployments found for workflow %s", *selectedRun.Name)
	}

	var targetEnvIDs []int64
	var envDisplay string
	if nonInteractive {
		if envAll {
			for _, d := range deployments {
				targetEnvIDs = append(targetEnvIDs, *d.Environment.ID)
			}
			envDisplay = fmt.Sprintf("%d environments", len(targetEnvIDs))
		} else if envNamesFlag != "" {
			parts := strings.Split(envNamesFlag, ",")
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}
				// find matching deployment by environment name (case-insensitive)
				found := false
				for _, d := range deployments {
					if strings.EqualFold(*d.Environment.Name, p) {
						targetEnvIDs = append(targetEnvIDs, *d.Environment.ID)
						found = true
						break
					}
				}
				if !found {
					log.Fatalf("environment name '%s' not found among pending deployments", p)
				}
			}
			envDisplay = fmt.Sprintf("%d environment(s)", len(targetEnvIDs))
		} else if len(deployments) == 1 {
			targetEnvIDs = []int64{*deployments[0].Environment.ID}
			envDisplay = *deployments[0].Environment.Name
		} else {
			log.Fatalf("non-interactive mode requires --env-names or --env-all when multiple deployments are present")
		}
	} else {
		if len(deployments) == 1 {
			targetEnvIDs = []int64{*deployments[0].Environment.ID}
			envDisplay = *deployments[0].Environment.Name
		} else {
			envOptions := make([]string, len(deployments)+1)
			envOptions[0] = fmt.Sprintf("All (%d environments)", len(deployments))
			for i, d := range deployments {
				envOptions[i+1] = *d.Environment.Name
			}

			envIdx, err := f.Prompter.Select("Select Environment", "", envOptions)
			if err != nil {
				if strings.Contains(err.Error(), "interrupt") {
					return
				}
				log.Fatal(err.Error())
				return
			}

			if envIdx == 0 {
				for _, d := range deployments {
					targetEnvIDs = append(targetEnvIDs, *d.Environment.ID)
				}
				envDisplay = fmt.Sprintf("%d environments", len(targetEnvIDs))
			} else {
				sel := deployments[envIdx-1]
				targetEnvIDs = []int64{*sel.Environment.ID}
				envDisplay = *sel.Environment.Name
			}
		}
	}

	var action string
	if nonInteractive {
		if strings.ToLower(actionFlag) != "approve" && strings.ToLower(actionFlag) != "reject" {
			log.Fatalf("invalid --action value '%s', must be 'approve' or 'reject'", actionFlag)
		}
		if strings.ToLower(actionFlag) == "approve" {
			action = "approved"
		} else {
			action = "rejected"
		}
	} else {
		actionIdx, err := f.Prompter.Select("Select Action", "Approve", actions)
		if err != nil {
			if strings.Contains(err.Error(), "interrupt") {
				return
			}
			log.Fatal(err.Error())
			return
		}
		action = "approved"
		if actionIdx == 1 {
			action = "rejected"
		}
	}

	req := &github.PendingDeploymentsRequest{
		EnvironmentIDs: targetEnvIDs,
		State:          action,
		Comment:        fmt.Sprintf("%s via gh-deployments", action),
	}

	_, _, err = client.Actions.PendingDeployments(ctx, repo.Owner, repo.Name, *selectedRun.ID, req)
	if err != nil {
		log.Fatalf("failed to approve/reject deployment: %v", err)
	}

	out := f.IOStreams.Out
	cs := f.IOStreams.ColorScheme()
	fmt.Fprintf(out, "%s Job %s to %s %s", cs.SuccessIcon(), cs.Cyan(*selectedRun.Name), cs.Cyan(envDisplay), action)
	fmt.Fprintln(out)
}
