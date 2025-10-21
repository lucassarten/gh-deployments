/*
Copyright © 2025 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"io"
	"log"
	"os"

	repository "github.com/cli/go-gh/v2/pkg/repository"
	"github.com/google/go-github/v74/github"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

// listCmd represents the list command
var listCmd = &cobra.Command{
	Use:   "list",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Run: func(cmd *cobra.Command, args []string) {
		list()
	},
}

func init() {
	rootCmd.AddCommand(listCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// listCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// listCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
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

var actions = []string{"approve", "reject"}

func list() {
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

	repoObj, err := repository.Parse(repo)
	if err != nil {
		log.Fatalf("failed to parse repository: %v", err)
	}
	// workflows, _, err := client.Actions.ListWorkflows(ctx, repo.Owner, repo.Name, &github.ListOptions{})
	// if err != nil {
	// 	log.Fatalf("failed to list workflows: %v", err)
	// }

	// log.Printf("Workflows: %+v", workflows)

	pending_workflows, _, err := client.Actions.ListRepositoryWorkflowRuns(ctx, repoObj.Owner, repoObj.Name, &github.ListWorkflowRunsOptions{
		Status: "waiting",
	})
	if err != nil {
		log.Fatalf("failed to list workflows: %v", err)
	}

	//log.Printf("Checking for pending workflows in %s/%s", repoObj.Owner, repoObj.Name)

	workflowRuns := make([]*WorkflowRun, len(pending_workflows.WorkflowRuns))

	for i, run := range pending_workflows.WorkflowRuns {
		workflowRuns[i] = &WorkflowRun{
			Name:      *run.Name,
			ID:        *run.ID,
			Event:     *run.Event,
			Status:    *run.Status,
			CreatedAt: run.CreatedAt.String(),
		}
	}

	templates := &promptui.SelectTemplates{
		Label:    "{{ .Name }} - {{ .CreatedAt }}",
		Active:   `{{"▸" | green }} "{{ .Name }} - {{ .CreatedAt }}"`,
		Inactive: "  {{ .Name }} - {{ .CreatedAt }}",
		Selected: `{{"▸" | green }} "{{ .Name }} - {{ .CreatedAt }}"`,
		Details: `
		--------- Workflow ----------
{{ "Name:" | faint }}	{{ .Name }}
{{ "ID:" | faint }}	{{ .ID }}
{{ "Event:" | faint }}	{{ .Event }}
{{ "Status:" | faint }}	{{ .Status }}
{{ "Created At:" | faint }}	{{ .CreatedAt }}`,
	}

	prompt := promptui.Select{
		Label:     "Select Workflow",
		Items:     workflowRuns,
		Templates: templates,
	}

	idx, _, err := prompt.Run()

	if err != nil {
		log.Printf("Prompt failed %v\n", err)
		return
	}

	deployments, _, err := client.Actions.GetPendingDeployments(ctx, repoObj.Owner, repoObj.Name, *pending_workflows.WorkflowRuns[idx].ID)
	if err != nil {
		log.Fatalf("failed to get pending deployments: %v", err)
	}

	if len(deployments) != 1 {
		os.Exit(1) // havent handled multiple deployments yet
	}

	deployment := deployments[0]

	deploymentOptions := make([]*Deployment, len(actions))

	// Create 2x objects one for approve, one for reject both with deployment details
	for i, action := range actions {
		deploymentOptions[i] = &Deployment{
			Action:      action,
			Name:        *pending_workflows.WorkflowRuns[idx].Name,
			Environment: *deployment.Environment.Name,
			Event:       *pending_workflows.WorkflowRuns[idx].Event,
			Status:      *pending_workflows.WorkflowRuns[idx].Status,
			CreatedAt:   pending_workflows.WorkflowRuns[idx].CreatedAt.String(),
		}
	}

	// log.Printf("Deployment options: %d", len(deploymentOptions))

	templates2 := &promptui.SelectTemplates{
		Label:    "{{ .Action }}",
		Active:   `{{"▸" | green }} "{{ .Action }}"`,
		Inactive: "  {{ .Action }}",
		Selected: `{{"▸" | green }} "{{ .Action }}"`,
		Details: `
		--------- Deployment ----------
{{ "Name:" | faint }}	{{ .Name }}
{{ "Environment:" | faint }}	{{ .Environment }}
{{ "Event:" | faint }}	{{ .Event }}
{{ "Status:" | faint }}	{{ .Status }}
{{ "Created At:" | faint }}	{{ .CreatedAt }}`,
	}

	promptDeploy := promptui.Select{
		Label:     "Select Approval Action",
		Items:     deploymentOptions,
		Templates: templates2,
	}

	idx2, _, err := promptDeploy.Run()

	if err != nil {
		log.Printf("Prompt failed %v\n", err)
		return
	}

	//log.Printf("You chose to %s deployment to %s for workflow %s", deploymentOptions[idx].Action, *deployment.Environment.Name, *pending_workflows.WorkflowRuns[idx].Name)

	transformedAction := deploymentOptions[idx2].Action
	if transformedAction == "reject" {
		transformedAction = "rejected"
	} else if transformedAction == "approve" {
		transformedAction = "approved"
	}

	_, resp, err := client.Actions.PendingDeployments(ctx, repoObj.Owner, repoObj.Name, *pending_workflows.WorkflowRuns[idx].ID, &github.PendingDeploymentsRequest{
		EnvironmentIDs: []int64{*deployment.Environment.ID},
		State: transformedAction,
		Comment: "Approved via gh-deployments",
	})
	if err != nil {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("failed to approve/reject deployment: %v", string(body))
		log.Fatalf("failed to approve/reject deployment: %v", err)
	}

	log.Printf("Job %s %s!", *pending_workflows.WorkflowRuns[idx].Name, transformedAction)

	// log.Printf("Deployment %s: %+v", action, dep)
}
