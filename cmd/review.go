/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"fmt"
	"os"
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
)

// reviewCmd represents the review command
var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Review a pending deployment",
	Long:  `Review and approve or reject pending deployments for GitHub Actions workflows.`,
	Run: func(cmd *cobra.Command, args []string) {
		review(repo, poll, factory.New("1"))
	},
}

func init() {
	rootCmd.AddCommand(reviewCmd)

	reviewCmd.Flags().BoolVarP(&poll, "poll", "p", false, "Poll for pending deployments if none are found")

	s.Suffix = " Fetching workflows..."
	s.Color("bold", "fgBlue")
}

var actions = []string{"Approve", "Reject"}

var s = spinner.New(spinner.CharSets[11], 100*time.Millisecond, spinner.WithWriter(os.Stderr))

func review(repoArg string, poll bool, f *cmdutil.Factory) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		log.Fatal("Unauthorized: No token present")
	}

	ctx := context.Background()
	client := github.NewClient(nil).WithAuthToken(token)

	if repoArg == "" {
		fullName, err := repository.Current()
		if err != nil {
			log.Fatalf("failed to get current repository: %v", err)
		}
		repoArg = fullName.Owner + "/" + fullName.Name
	}

	repo, err := repository.Parse(repoArg)
	if err != nil {
		log.Fatalf("failed to parse repository: %v", err)
	}

	s.Start()
	var pendingWorkflows *github.WorkflowRuns
	for {
		pendingWorkflows, _, err = client.Actions.ListRepositoryWorkflowRuns(ctx, repo.Owner, repo.Name, &github.ListWorkflowRunsOptions{
			Status: "waiting",
		})
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

	candidates := make([]string, len(pendingWorkflows.WorkflowRuns))

	for i, run := range pendingWorkflows.WorkflowRuns {
		createdLocal := run.GetCreatedAt().Local().Format("02/01/06 15:04:05")
		candidates[i] = fmt.Sprintf("%s - %s - %s", run.GetDisplayTitle(), run.GetActor().GetLogin(), createdLocal)
	}
	s.Stop()

	workflowIdx, err := f.Prompter.Select("Select Workflow", "", candidates)

	if err != nil {
		if strings.Contains(err.Error(), "interrupt") {
			return
		}
		log.Fatal(err.Error())
		return
	}

	deployments, _, err := client.Actions.GetPendingDeployments(ctx, repo.Owner, repo.Name, *pendingWorkflows.WorkflowRuns[workflowIdx].ID)
	if err != nil {
		log.Fatalf("failed to get pending deployments: %v", err)
	}

	if len(deployments) == 0 {
		log.Fatalf("no pending deployments found for workflow %s", *pendingWorkflows.WorkflowRuns[workflowIdx].Name)
	}

	var targetEnvIDs []int64
	var envDisplay string
	if len(deployments) == 1 {
		// Single deployment, use it directly
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
			// All selected
			for _, d := range deployments {
				if d.Environment != nil && d.Environment.ID != nil {
					targetEnvIDs = append(targetEnvIDs, *d.Environment.ID)
				}
			}
			envDisplay = fmt.Sprintf("%d environments", len(targetEnvIDs))
		} else {
			sel := deployments[envIdx]
			targetEnvIDs = []int64{*sel.Environment.ID}
			envDisplay = *sel.Environment.Name
		}
	}

	actionIdx, err := f.Prompter.Select("Select Action", "Approve", actions)

	if err != nil {
		if strings.Contains(err.Error(), "interrupt") {
			return
		}
		log.Fatal(err.Error())
		return
	}

	action := "approved"
	if actionIdx == 1 {
		action = "rejected"
	}

	req := &github.PendingDeploymentsRequest{
		EnvironmentIDs: targetEnvIDs,
		State:          action,
		Comment:        fmt.Sprintf("%s via gh-deployments", action),
	}

	_, _, err = client.Actions.PendingDeployments(ctx, repo.Owner, repo.Name, *pendingWorkflows.WorkflowRuns[workflowIdx].ID, req)
	if err != nil {
		log.Fatalf("failed to approve/reject deployment: %v", err)
	}

	out := f.IOStreams.Out
	cs := f.IOStreams.ColorScheme()
	fmt.Fprintf(out, "%s Job %s to %s %s", cs.SuccessIcon(), cs.Cyan(*pendingWorkflows.WorkflowRuns[workflowIdx].Name), cs.Cyan(envDisplay), action)
	fmt.Fprintln(out)
}
