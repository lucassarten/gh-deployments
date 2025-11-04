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

	reviewCmd.Flags().BoolVarP(&poll, "poll", "p", false, "Poll for deployments")

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
		log.Printf("No pending workflows in %s/%s", repo.Owner, repo.Name)
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

	if len(deployments) != 1 {
		os.Exit(1) // havent handled multiple deployments yet
	}

	deployment := deployments[0]

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

	_, _, err = client.Actions.PendingDeployments(ctx, repo.Owner, repo.Name, *pendingWorkflows.WorkflowRuns[workflowIdx].ID, &github.PendingDeploymentsRequest{
		EnvironmentIDs: []int64{*deployment.Environment.ID},
		State:          action,
		Comment:        fmt.Sprintf("%s via gh-deployments", action),
	})
	if err != nil {
		log.Fatalf("failed to approve/reject deployment: %v", err)
	}

	out := f.IOStreams.Out
	cs := f.IOStreams.ColorScheme()
	fmt.Fprintf(out, "%s Job %s %s", cs.SuccessIcon(), cs.Cyan(*pendingWorkflows.WorkflowRuns[workflowIdx].Name), action)
	fmt.Fprintln(out)
}
