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
// reviewCmd represents the review command
var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Review a pending deployment",
	Long: `Review and approve or reject pending deployments for GitHub Actions workflows.`,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 1 { repo = args[0] }
		review(repo, factory.New("1"))
	},
}

func init() {
	rootCmd.AddCommand(reviewCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// listCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// listCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")

	s.Suffix = " Fetching workflows..."
	s.Color("bold", "fgBlue")
}

type WorkflowRun struct {
	Name      string
	ID        int64
	Event     string
	Status    string
	CreatedAt string
}

type Deployment struct {
	Action      string
	Name        string
	Environment string
	Event       string
	Status      string
	CreatedAt   string
}

var actions = []string{"Approve", "Reject"}

var s = spinner.New(spinner.CharSets[11], 100*time.Millisecond, spinner.WithWriter(os.Stderr))

func review(repoArg string, f *cmdutil.Factory) {
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
	pendingWorkflows, _, err := client.Actions.ListRepositoryWorkflowRuns(ctx, repo.Owner, repo.Name, &github.ListWorkflowRunsOptions{
		Status: "waiting",
	})
	if err != nil {
		log.Fatalf("failed to list workflows: %v", err)
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
		Comment:        fmt.Sprintf("%s via gh-deployments", strings.ToTitle(action)),
	})
	if err != nil {
		log.Fatalf("failed to approve/reject deployment: %v", err)
	}

	log.Printf("Job \"%s\" %s!", *pendingWorkflows.WorkflowRuns[workflowIdx].Name, action)
}
